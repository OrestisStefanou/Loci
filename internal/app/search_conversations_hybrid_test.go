package app_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/embed"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

// Messages the fake places on separate axes.
const (
	askCutoff   = "Where do payments stop?"
	answeredCut = "Settlement cut-off is 16:00 CET"
	runFound    = "Found the token rotation job in cron.go"
	askTokens   = "Show me the tokens config"
	answerRot   = "Rotation runs daily"
)

// conversationsApp holds two sessions and one subagent run, all embedded:
// s1 on fees-v2 asks about the cut-off, and its run reports a finding about
// token rotation; s2 has nothing attached and talks about tokens.
type conversationsApp struct {
	a           *app.App
	fake        *embed.Fake
	s1, run, s2 domain.ID
}

func newConversationsApp(t *testing.T, maxDistance float64) conversationsApp {
	t.Helper()
	fake := embed.NewFake("fake-model")
	fake.Set(askCutoff, []float32{0, 0, 1})
	fake.Set(answeredCut, []float32{1, 0, 0})
	fake.Set(runFound, []float32{0, 1, 0})
	fake.Set(askTokens, []float32{0, 0, 1})
	fake.Set(answerRot, []float32{0, 0.8, 0.6})
	a := app.New(app.Deps{
		Store: testutil.NewStore(t), Clock: testutil.Clock{}, IDs: &testutil.SeqIDs{},
		UserHandle: testutil.UserHandle, Embedder: fake, MaxVectorDistance: maxDistance,
	})
	c := conversationsApp{a: a, fake: fake}
	c.s1 = captured(t, a, app.CaptureStartInput{CaptureKey: key("s1", ""), Project: "fees-v2"}, askCutoff, answeredCut)
	c.run = captured(t, a, app.CaptureStartInput{CaptureKey: key("s1", "a1"), AgentType: "Explore"}, runFound)
	c.s2 = captured(t, a, app.CaptureStartInput{CaptureKey: key("s2", "")}, askTokens, answerRot)
	if _, err := a.Embed(context.Background(), app.EmbedInput{}); err != nil {
		t.Fatal(err)
	}
	return c
}

// messageID returns the id of the message with content in conversation id.
func messageID(t *testing.T, a *app.App, id domain.ID, content string) domain.ID {
	t.Helper()
	out, err := a.GetConversation(context.Background(), app.GetConversationInput{ID: string(id)})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range out.Messages {
		if m.Content == content {
			return m.ID
		}
	}
	t.Fatalf("no message %q in %s", content, id)
	return ""
}

// matchOf returns the match of conversation id in cs, or nil.
func matchOf(cs []domain.ConversationSummary, id domain.ID) *domain.MessageMatch {
	for _, c := range cs {
		if c.ID == id {
			return c.Match
		}
	}
	return nil
}

func TestSearchConversationsHybrid(t *testing.T) {
	ctx := context.Background()

	t.Run("finds a session by meaning with no substring hit", func(t *testing.T) {
		c := newConversationsApp(t, 0)
		c.fake.Set("when is the deadline", []float32{0.9, 0.1, 0})
		out, err := c.a.SearchConversations(ctx, app.SearchConversationsInput{Query: "when is the deadline"})
		if err != nil {
			t.Fatal(err)
		}
		if out.Ranking != app.RankingHybrid || out.SemanticUnavailable {
			t.Fatalf("ranking %q, unavailable %v", out.Ranking, out.SemanticUnavailable)
		}
		if len(out.Conversations) == 0 || out.Conversations[0].ID != c.s1 {
			t.Fatalf("got %v", conversationIDs(out.Conversations))
		}
		want := domain.MessageMatch{Message: messageID(t, c.a, c.s1, answeredCut), Conversation: c.s1, Snippet: answeredCut}
		if m := out.Conversations[0].Match; m == nil || *m != want {
			t.Fatalf("match %+v, want %+v", m, want)
		}
	})

	t.Run("a subagent run's message finds its session and names the run", func(t *testing.T) {
		c := newConversationsApp(t, 0.4)
		c.fake.Set("credential refresh", []float32{0, 1, 0})
		out, err := c.a.SearchConversations(ctx, app.SearchConversationsInput{Query: "credential refresh"})
		if err != nil {
			t.Fatal(err)
		}
		if got := conversationIDs(out.Conversations); !slices.Equal(got, []domain.ID{c.s1, c.s2}) {
			t.Fatalf("got %v", got)
		}
		if m := out.Conversations[0].Match; m == nil || m.Conversation != c.run || m.Message != messageID(t, c.a, c.run, runFound) {
			t.Fatalf("match %+v", m)
		}
	})

	t.Run("a conversation in both lists ranks first and keeps its nearest message", func(t *testing.T) {
		c := newConversationsApp(t, 0.4)
		c.fake.Set("tokens", []float32{0, 0.8, 0.6})
		out, err := c.a.SearchConversations(ctx, app.SearchConversationsInput{Query: "tokens"})
		if err != nil {
			t.Fatal(err)
		}
		// s2 is the only substring hit (askTokens) and the nearest by vector
		// (answerRot); s1 is a vector match only, through its run.
		if got := conversationIDs(out.Conversations); !slices.Equal(got, []domain.ID{c.s2, c.s1}) {
			t.Fatalf("got %v", got)
		}
		if m := out.Conversations[0].Match; m == nil || m.Message != messageID(t, c.a, c.s2, answerRot) {
			t.Fatalf("match %+v", m)
		}
	})

	t.Run("filters bound the vector side", func(t *testing.T) {
		c := newConversationsApp(t, 0)
		c.fake.Set("rotation", []float32{0, 0.8, 0.6})
		out, err := c.a.SearchConversations(ctx, app.SearchConversationsInput{Query: "rotation", Project: "fees-v2"})
		if err != nil {
			t.Fatal(err)
		}
		if got := conversationIDs(out.Conversations); !slices.Equal(got, []domain.ID{c.s1}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("max distance drops conversations with no close message", func(t *testing.T) {
		c := newConversationsApp(t, 0.4)
		c.fake.Set("deadline", []float32{1, 0, 0})
		out, err := c.a.SearchConversations(ctx, app.SearchConversationsInput{Query: "deadline"})
		if err != nil {
			t.Fatal(err)
		}
		if got := conversationIDs(out.Conversations); !slices.Equal(got, []domain.ID{c.s1}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("subagent runs are listed with their own matches on request", func(t *testing.T) {
		c := newConversationsApp(t, 0.4)
		c.fake.Set("credential refresh", []float32{0, 1, 0})
		out, err := c.a.SearchConversations(ctx, app.SearchConversationsInput{Query: "credential refresh", IncludeSubagents: true})
		if err != nil {
			t.Fatal(err)
		}
		if m := matchOf(out.Conversations, c.run); m == nil || m.Conversation != c.run {
			t.Fatalf("run match %+v in %v", m, conversationIDs(out.Conversations))
		}
	})

	t.Run("unembedded messages are searched by substring only", func(t *testing.T) {
		c := newConversationsApp(t, 0.4)
		s3 := captured(t, c.a, app.CaptureStartInput{CaptureKey: key("s3", "")}, "a fresh prompt about tokens")
		c.fake.Set("fresh", []float32{-1, 0, 0}) // far from every embedded message
		out, err := c.a.SearchConversations(ctx, app.SearchConversationsInput{Query: "fresh"})
		if err != nil {
			t.Fatal(err)
		}
		if got := conversationIDs(out.Conversations); !slices.Equal(got, []domain.ID{s3}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("unavailable provider falls back to text and keeps the substring match", func(t *testing.T) {
		c := newConversationsApp(t, 0.4)
		c.fake.Fail(domain.ErrUnavailable)
		out, err := c.a.SearchConversations(ctx, app.SearchConversationsInput{Query: "TOKENS"})
		if err != nil {
			t.Fatal(err)
		}
		if out.Ranking != app.RankingText || !out.SemanticUnavailable {
			t.Fatalf("ranking %q, unavailable %v", out.Ranking, out.SemanticUnavailable)
		}
		want := domain.MessageMatch{Message: messageID(t, c.a, c.s2, askTokens), Conversation: c.s2, Snippet: askTokens}
		if got := conversationIDs(out.Conversations); !slices.Equal(got, []domain.ID{c.s2}) || *out.Conversations[0].Match != want {
			t.Fatalf("got %v, match %+v", got, out.Conversations[0].Match)
		}
	})

	t.Run("no query has no match and never calls the provider", func(t *testing.T) {
		c := newConversationsApp(t, 0.4)
		calls := len(c.fake.Calls())
		out, err := c.a.SearchConversations(ctx, app.SearchConversationsInput{})
		if err != nil {
			t.Fatal(err)
		}
		if out.Ranking != app.RankingRecency || len(c.fake.Calls()) != calls {
			t.Fatalf("ranking %q, calls %d -> %d", out.Ranking, calls, len(c.fake.Calls()))
		}
		for _, cs := range out.Conversations {
			if cs.Match != nil {
				t.Fatalf("%s has match %+v", cs.ID, cs.Match)
			}
		}
	})
}

func TestSearchConversationsSnippet(t *testing.T) {
	ctx := context.Background()
	long := strings.Repeat("lead in text ", 20) + "the Cut-Off moved to 17:00\n\nbecause " + strings.Repeat("of reasons ", 20)
	tests := []struct {
		name, content, query, want string
	}{
		{name: "a short message is kept whole on one line", content: "cut-off\n  moved", query: "moved", want: "cut-off moved"},
		{name: "a long message is cut around the query", content: long, query: "cut-off moved",
			want: "…lead in text lead in text the Cut-Off moved to 17:00 because of reasons of reasons of reasons of reasons of reasons of…"},
		{name: "a query near the start keeps the start", content: "the cut-off " + strings.Repeat("x", 200), query: "cut-off",
			want: "the cut-off " + strings.Repeat("x", 107) + "…"},
		{name: "a query near the end keeps the end", content: strings.Repeat("x", 200) + " the cut-off", query: "cut-off",
			want: "…" + strings.Repeat("x", 107) + " the cut-off"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := testutil.NewApp(t)
			captured(t, a, app.CaptureStartInput{CaptureKey: key("s1", "")}, tt.content)
			out, err := a.SearchConversations(ctx, app.SearchConversationsInput{Query: tt.query})
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Conversations) != 1 || out.Conversations[0].Match == nil {
				t.Fatalf("got %+v", out.Conversations)
			}
			got := out.Conversations[0].Match.Snippet
			if got != tt.want {
				t.Fatalf("snippet\n got %q\nwant %q", got, tt.want)
			}
			if n := len([]rune(got)); n > 120 {
				t.Fatalf("snippet has %d characters", n)
			}
		})
	}
}
