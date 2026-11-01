package app_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

// captured starts a conversation and adds messages, alternating user and
// assistant (a subagent run's are all assistant), and returns its id.
func captured(t *testing.T, a *app.App, start app.CaptureStartInput, msgs ...string) domain.ID {
	t.Helper()
	ctx := context.Background()
	out, err := a.CaptureStart(ctx, start)
	if err != nil {
		t.Fatal(err)
	}
	for i, content := range msgs {
		role := domain.RoleUser
		if i%2 == 1 || start.Agent != "" {
			role = domain.RoleAssistant
		}
		if _, err := a.CaptureMessage(ctx, app.CaptureMessageInput{CaptureKey: start.CaptureKey, Role: role, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	return out.Conversation.ID
}

func conversationIDs(cs []domain.ConversationSummary) []domain.ID {
	ids := make([]domain.ID, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	return ids
}

func TestSearchConversations(t *testing.T) {
	ctx := context.Background()
	a := testutil.NewApp(t)
	if _, err := a.SetTaskProject(ctx, app.SetTaskProjectInput{Task: "EPD-2", Project: ptr("fees-v2")}); err != nil {
		t.Fatal(err)
	}
	s1 := captured(t, a, app.CaptureStartInput{CaptureKey: key("s1", ""), Project: "fees-v2", Task: "EPD-1"},
		"Where is the\n  settlement cut-off?", "In settle.go.")
	run := captured(t, a, app.CaptureStartInput{CaptureKey: key("s1", "a1"), AgentType: "Explore"},
		"Found the CUTOFF constant.")
	s2 := captured(t, a, app.CaptureStartInput{CaptureKey: key("s2", "")}, "unrelated")
	s3 := captured(t, a, app.CaptureStartInput{CaptureKey: key("s3", ""), Task: "EPD-2"}, strings.Repeat("long ", 40))
	if _, err := a.Attach(ctx, app.AttachInput{Item: s3, Tags: tagList("decided")}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		in      app.SearchConversationsInput
		want    []domain.ID
		ranking string
	}{
		{name: "lists sessions only, most recent first", want: []domain.ID{s3, s2, s1}, ranking: app.RankingRecency},
		{name: "subagent runs on request", in: app.SearchConversationsInput{IncludeSubagents: true}, want: []domain.ID{s3, s2, run, s1}},
		{name: "project includes its tasks' conversations", in: app.SearchConversationsInput{Project: "Fees-V2"}, want: []domain.ID{s3, s1}},
		{name: "a subagent run matches its session's attachment", in: app.SearchConversationsInput{Task: "epd-1", IncludeSubagents: true}, want: []domain.ID{run, s1}},
		{name: "tags must be on the conversation", in: app.SearchConversationsInput{Tags: []string{"Decided"}}, want: []domain.ID{s3}},
		{name: "query matches a message", in: app.SearchConversationsInput{Query: "unrelated"}, want: []domain.ID{s2}, ranking: app.RankingText},
		{name: "query finds a session through its subagent run", in: app.SearchConversationsInput{Query: "cutoff"}, want: []domain.ID{s1}},
		{name: "harness and session find the session", in: app.SearchConversationsInput{Harness: "Claude-Code", Session: "s2"}, want: []domain.ID{s2}},
		{name: "a session of another harness is not found", in: app.SearchConversationsInput{Harness: "codex", Session: "s2"}, want: []domain.ID{}},
		{name: "limit caps the result", in: app.SearchConversationsInput{Limit: 1}, want: []domain.ID{s3}},
		{name: "unknown project is empty", in: app.SearchConversationsInput{Project: "nope"}, want: []domain.ID{}},
		{name: "unknown tag is empty", in: app.SearchConversationsInput{Tags: []string{"nope"}}, want: []domain.ID{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := a.SearchConversations(ctx, tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if got := conversationIDs(out.Conversations); !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			if tt.ranking != "" && out.Ranking != tt.ranking {
				t.Fatalf("ranking %q, want %q", out.Ranking, tt.ranking)
			}
		})
	}

	t.Run("summaries carry counts and a one-line opening", func(t *testing.T) {
		out, err := a.SearchConversations(ctx, app.SearchConversationsInput{})
		if err != nil {
			t.Fatal(err)
		}
		long, first := out.Conversations[0], out.Conversations[2]
		if first.Messages != 2 || first.Subagents != 1 || first.Opening != "Where is the settlement cut-off?" {
			t.Fatalf("s1 summary: %+v", first)
		}
		if first.Project != "fees-v2" || first.Task != "EPD-1" {
			t.Fatalf("s1 attachment: %+v", first)
		}
		if r := []rune(long.Opening); len(r) != 120 || r[len(r)-1] != '…' {
			t.Fatalf("long opening not cut to 120 characters: %q", long.Opening)
		}
	})

	t.Run("invalid input", func(t *testing.T) {
		for _, in := range []app.SearchConversationsInput{{Limit: -1}, {Project: "a/b"}, {Task: "EPD 1"}, {Tags: []string{"a b"}}, {Session: "s2"}} {
			if _, err := a.SearchConversations(ctx, in); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("%+v: want ErrInvalid, got %v", in, err)
			}
		}
	})
}

func TestGetConversation(t *testing.T) {
	ctx := context.Background()
	a := testutil.NewApp(t)
	msgs := []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8", "m9", "m10", "m11", "m12", "m13", "m14", "m15"}
	s1 := captured(t, a, app.CaptureStartInput{CaptureKey: key("s1", ""), Task: "EPD-1"}, msgs...)
	run := captured(t, a, app.CaptureStartInput{CaptureKey: key("s1", "a1"), AgentType: "Explore"}, "found it")
	other := captured(t, a, app.CaptureStartInput{CaptureKey: key("s2", "")}, "elsewhere")

	all, err := a.GetConversation(ctx, app.GetConversationInput{ID: " " + s1 + " "})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Messages) != 15 || all.Messages[0].Content != "m1" || all.Messages[14].Seq != 15 {
		t.Fatalf("messages: %+v", all.Messages)
	}
	if all.Conversation.Messages != 15 || all.Conversation.Task != "EPD-1" {
		t.Fatalf("conversation: %+v", all.Conversation)
	}
	if len(all.Subagents) != 1 || all.Subagents[0].ID != run || all.Subagents[0].AgentType != "Explore" || all.Subagents[0].Messages != 1 {
		t.Fatalf("subagents: %+v", all.Subagents)
	}
	byContent := map[string]domain.ID{}
	for _, m := range all.Messages {
		byContent[m.Content] = m.ID
	}

	t.Run("a subagent run names its session and the attachment it shares", func(t *testing.T) {
		out, err := a.GetConversation(ctx, app.GetConversationInput{ID: run})
		if err != nil {
			t.Fatal(err)
		}
		if out.Conversation.Parent != s1 || out.Conversation.Task != "EPD-1" || len(out.Subagents) != 0 || out.Subagents == nil {
			t.Fatalf("got %+v", out)
		}
	})

	windows := []struct {
		name   string
		around string
		window int
		want   []int
	}{
		{name: "default window is five either side", around: "m8", want: []int{3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}},
		{name: "window clipped at the start", around: "m2", window: 2, want: []int{1, 2, 3, 4}},
		{name: "window clipped at the end", around: "m15", window: 1, want: []int{14, 15}},
	}
	for _, tt := range windows {
		t.Run(tt.name, func(t *testing.T) {
			out, err := a.GetConversation(ctx, app.GetConversationInput{ID: s1, AroundMessage: byContent[tt.around], Window: tt.window})
			if err != nil {
				t.Fatal(err)
			}
			var got []int
			for _, m := range out.Messages {
				got = append(got, m.Seq)
			}
			if !slices.Equal(got, tt.want) || out.Conversation.Messages != 15 {
				t.Fatalf("seqs %v (of %d), want %v", got, out.Conversation.Messages, tt.want)
			}
		})
	}

	otherMsg, err := a.GetConversation(ctx, app.GetConversationInput{ID: other})
	if err != nil {
		t.Fatal(err)
	}
	errs := []struct {
		name string
		in   app.GetConversationInput
		want error
	}{
		{name: "empty id", in: app.GetConversationInput{ID: " "}, want: domain.ErrInvalid},
		{name: "negative window", in: app.GetConversationInput{ID: s1, AroundMessage: byContent["m1"], Window: -1}, want: domain.ErrInvalid},
		{name: "window without around", in: app.GetConversationInput{ID: s1, Window: 3}, want: domain.ErrInvalid},
		{name: "around a message of another conversation", in: app.GetConversationInput{ID: s1, AroundMessage: otherMsg.Messages[0].ID}, want: domain.ErrInvalid},
		{name: "unknown message", in: app.GetConversationInput{ID: s1, AroundMessage: "nope"}, want: domain.ErrNotFound},
		{name: "unknown conversation", in: app.GetConversationInput{ID: "nope"}, want: domain.ErrNotFound},
	}
	for _, tt := range errs {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := a.GetConversation(ctx, tt.in); !errors.Is(err, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, err)
			}
		})
	}
}

func TestAttachConversation(t *testing.T) {
	ctx := context.Background()
	a := testutil.NewApp(t)
	s1 := captured(t, a, app.CaptureStartInput{CaptureKey: key("s1", "")}, "hi")
	run := captured(t, a, app.CaptureStartInput{CaptureKey: key("s1", "a1"), AgentType: "Explore"}, "found")

	get := func(id domain.ID) domain.Conversation {
		t.Helper()
		out, err := a.GetConversation(ctx, app.GetConversationInput{ID: id})
		if err != nil {
			t.Fatal(err)
		}
		return out.Conversation.Conversation
	}

	out, err := a.Attach(ctx, app.AttachInput{Item: s1, Project: ptr("Fees-V2"), Task: ptr("epd-1"), Tags: tagList("decided")})
	if err != nil {
		t.Fatal(err)
	}
	if out.Memory != nil || out.Conversation == nil {
		t.Fatalf("want a conversation only, got %+v", out)
	}
	if c := out.Conversation; c.ID != s1 || c.Project != "fees-v2" || c.Task != "EPD-1" || !slices.Equal(c.Tags, []string{"decided"}) {
		t.Fatalf("attached: %+v", c)
	}
	if c := get(run); c.Project != "fees-v2" || c.Task != "EPD-1" || len(c.Tags) != 0 {
		t.Fatalf("subagent run should share its session's attachment, not its tags: %+v", c)
	}

	if _, err := a.Attach(ctx, app.AttachInput{Item: run, Task: ptr("EPD-9")}); err != nil {
		t.Fatal(err)
	}
	if c := get(run); c.Project != "" || c.Task != "EPD-9" {
		t.Fatalf("a run's own task replaces its session's attachment: %+v", c)
	}
	if _, err := a.Attach(ctx, app.AttachInput{Item: run, Task: ptr("")}); err != nil {
		t.Fatal(err)
	}
	if c := get(run); c.Project != "fees-v2" || c.Task != "EPD-1" {
		t.Fatalf("a run with nothing of its own shares its session's again: %+v", c)
	}

	if _, err := a.Attach(ctx, app.AttachInput{Item: s1, Project: ptr(""), Task: ptr(""), Tags: tagList()}); err != nil {
		t.Fatal(err)
	}
	found, err := a.SearchConversations(ctx, app.SearchConversationsInput{Project: "fees-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Conversations) != 0 {
		t.Fatalf("detached conversation still found: %+v", found.Conversations)
	}
	if c := get(s1); c.Project != "" || c.Task != "" || len(c.Tags) != 0 {
		t.Fatalf("detached: %+v", c)
	}
}
