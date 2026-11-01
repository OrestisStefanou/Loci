package app_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

func key(session, agent string) app.CaptureKey {
	return app.CaptureKey{Harness: "claude-code", Session: session, Agent: agent}
}

func TestCaptureStart(t *testing.T) {
	ctx := context.Background()

	t.Run("attaches what it is given and normalises where it started", func(t *testing.T) {
		a := testutil.NewApp(t)
		out, err := a.CaptureStart(ctx, app.CaptureStartInput{
			CaptureKey: app.CaptureKey{Harness: " Claude-Code ", Session: " s1 "},
			Project:    " Fees-V2 ", Task: "epd-7",
			Repo: "https://token@github.com/Northwind/api.git", Branch: " main ",
		})
		if err != nil {
			t.Fatal(err)
		}
		c := out.Conversation
		if !out.Created || c.Harness != "claude-code" || c.Session != "s1" || c.Project != "fees-v2" || c.Task != "EPD-7" {
			t.Fatalf("got %+v created=%v", c, out.Created)
		}
		if c.Repo != "github.com/northwind/api" || c.Branch != "main" || c.Human != testutil.UserHandle {
			t.Fatalf("provenance: %+v", c)
		}
	})

	t.Run("nothing is inferred from where it started", func(t *testing.T) {
		a := testutil.NewApp(t)
		out, err := a.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: key("s1", ""),
			Branch: "feat/EPD-2", Repo: "git@github.com:northwind/api.git"})
		if err != nil {
			t.Fatal(err)
		}
		if out.Conversation.Project != "" || out.Conversation.Task != "" {
			t.Fatalf("got %+v", out.Conversation)
		}
	})

	t.Run("the task's project wins", func(t *testing.T) {
		a := testutil.NewApp(t)
		if _, err := a.SetTaskProject(ctx, app.SetTaskProjectInput{Task: "EPD-1", Project: ptr("trade-settlement")}); err != nil {
			t.Fatal(err)
		}
		out, err := a.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: key("s1", ""), Project: "fees-v2", Task: "EPD-1"})
		if err != nil {
			t.Fatal(err)
		}
		if out.Conversation.Project != "trade-settlement" {
			t.Fatalf("got %+v", out.Conversation)
		}
	})

	t.Run("resume returns the conversation unchanged", func(t *testing.T) {
		a := testutil.NewApp(t)
		first, err := a.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: key("s1", ""), Task: "EPD-1", Branch: "main"})
		if err != nil {
			t.Fatal(err)
		}
		again, err := a.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: key("s1", ""), Task: "EPD-2", Branch: "other"})
		if err != nil {
			t.Fatal(err)
		}
		c := again.Conversation
		if again.Created || c.ID != first.Conversation.ID || c.Task != "EPD-1" || c.Branch != "main" {
			t.Fatalf("got %+v created=%v", c, again.Created)
		}
	})

	t.Run("start attaches a conversation a message created unattached", func(t *testing.T) {
		a := testutil.NewApp(t)
		first, err := a.CaptureMessage(ctx, app.CaptureMessageInput{CaptureKey: key("s1", ""), Role: domain.RoleUser, Content: "hi"})
		if err != nil {
			t.Fatal(err)
		}
		out, err := a.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: key("s1", ""), Project: "fees-v2", Task: "EPD-1"})
		if err != nil {
			t.Fatal(err)
		}
		c := out.Conversation
		if out.Created || c.ID != first.Conversation.ID || c.Project != "fees-v2" || c.Task != "EPD-1" {
			t.Fatalf("got %+v created=%v", c, out.Created)
		}
	})

	t.Run("subagent run links to the main conversation and shares its attachment", func(t *testing.T) {
		a := testutil.NewApp(t)
		main, err := a.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: key("s1", ""), Project: "fees-v2", Task: "EPD-1"})
		if err != nil {
			t.Fatal(err)
		}
		sub, err := a.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: key("s1", "a1"), AgentType: "tech-pm-agent"})
		if err != nil {
			t.Fatal(err)
		}
		c := sub.Conversation
		if c.Parent != main.Conversation.ID || c.Agent != "a1" || c.AgentType != "tech-pm-agent" || c.Project != "fees-v2" || c.Task != "EPD-1" {
			t.Fatalf("got %+v", c)
		}
	})

	t.Run("subagent run before its session creates the main conversation", func(t *testing.T) {
		a := testutil.NewApp(t)
		sub, err := a.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: key("s1", "a1")})
		if err != nil {
			t.Fatal(err)
		}
		main, err := a.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: key("s1", "")})
		if err != nil {
			t.Fatal(err)
		}
		if main.Created || sub.Conversation.Parent != main.Conversation.ID {
			t.Fatalf("main %+v created=%v, sub %+v", main.Conversation, main.Created, sub.Conversation)
		}
	})

	t.Run("two sessions in one directory stay apart", func(t *testing.T) {
		a := testutil.NewApp(t)
		ids := map[string]bool{}
		for _, s := range []string{"s1", "s2"} {
			out, err := a.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: key(s, ""), Branch: "main"})
			if err != nil {
				t.Fatal(err)
			}
			ids[out.Conversation.ID] = true
		}
		if len(ids) != 2 {
			t.Fatalf("want two conversations, got %v", ids)
		}
	})

	invalid := []struct {
		name string
		key  app.CaptureKey
	}{
		{"empty harness", app.CaptureKey{Session: "s1"}},
		{"harness with space", app.CaptureKey{Harness: "claude code", Session: "s1"}},
		{"empty session", app.CaptureKey{Harness: "claude-code", Session: "  "}},
	}
	for _, tt := range invalid {
		t.Run(tt.name+" is invalid", func(t *testing.T) {
			_, err := testutil.NewApp(t).CaptureStart(ctx, app.CaptureStartInput{CaptureKey: tt.key})
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
	long := strings.Repeat("x", 257)
	for name, in := range map[string]app.CaptureStartInput{
		"bad project":     {CaptureKey: key("s1", ""), Project: "a/b"},
		"bad task":        {CaptureKey: key("s1", ""), Task: "EPD 1"},
		"long agent type": {CaptureKey: key("s1", "a1"), AgentType: long},
		"long branch":     {CaptureKey: key("s1", ""), Branch: long},
		"long session":    {CaptureKey: key(long, "")},
	} {
		t.Run(name+" is invalid", func(t *testing.T) {
			_, err := testutil.NewApp(t).CaptureStart(ctx, in)
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
}

func TestCaptureMessage(t *testing.T) {
	ctx := context.Background()
	msg := func(agent string, role domain.Role, content, prompt string) app.CaptureMessageInput {
		return app.CaptureMessageInput{CaptureKey: key("s1", agent), Role: role, Content: content, PromptID: prompt}
	}

	t.Run("main conversation is the person and the main agent", func(t *testing.T) {
		a := testutil.NewApp(t)
		u, err := a.CaptureMessage(ctx, msg("", domain.RoleUser, "  what is the cut-off?\n", "p1"))
		if err != nil {
			t.Fatal(err)
		}
		r, err := a.CaptureMessage(ctx, msg("", domain.RoleAssistant, "16:00 CET", "p1"))
		if err != nil {
			t.Fatal(err)
		}
		if u.Message.Seq != 1 || u.Message.Speaker != testutil.UserHandle || u.Message.Addressee != "claude-code" ||
			u.Message.Content != "  what is the cut-off?\n" || u.Message.PromptID != "p1" {
			t.Fatalf("user message: %+v", u.Message)
		}
		if r.Message.Seq != 2 || r.Message.Speaker != "claude-code" || r.Message.Addressee != testutil.UserHandle {
			t.Fatalf("assistant message: %+v", r.Message)
		}
		if u.Conversation.ID != r.Conversation.ID || u.Message.ConversationID != u.Conversation.ID {
			t.Fatal("messages in different conversations")
		}
	})

	t.Run("subagent run is the main agent and the subagent", func(t *testing.T) {
		a := testutil.NewApp(t)
		in := msg("a1", domain.RoleAssistant, "done", "")
		in.AgentType = "review-kit:Code Reviewer"
		out, err := a.CaptureMessage(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if out.Message.Speaker != "review-kit-code-reviewer" || out.Message.Addressee != "claude-code" ||
			out.Conversation.AgentType != "review-kit:Code Reviewer" || out.Conversation.Parent == "" {
			t.Fatalf("got %+v in %+v", out.Message, out.Conversation)
		}
		u, err := a.CaptureMessage(ctx, msg("a1", domain.RoleUser, "review this", ""))
		if err != nil {
			t.Fatal(err)
		}
		if u.Message.Speaker != "claude-code" || u.Message.Addressee != "review-kit-code-reviewer" {
			t.Fatalf("prompt to the subagent: %+v", u.Message)
		}
	})

	t.Run("the person is the one recorded on the conversation", func(t *testing.T) {
		st, ids := testutil.NewStore(t), &testutil.SeqIDs{}
		before := app.New(app.Deps{Store: st, Clock: testutil.Clock{}, IDs: ids, UserHandle: "alice"})
		if _, err := before.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: key("s1", "")}); err != nil {
			t.Fatal(err)
		}
		// user.handle changed and the server restarted mid-session.
		after := app.New(app.Deps{Store: st, Clock: testutil.Clock{}, IDs: ids, UserHandle: "bob"})
		out, err := after.CaptureMessage(ctx, msg("", domain.RoleUser, "hi", ""))
		if err != nil {
			t.Fatal(err)
		}
		if out.Message.Speaker != "alice" || out.Conversation.Human != "alice" {
			t.Fatalf("got %+v", out.Message)
		}
	})

	t.Run("subagent without a type is 'subagent'", func(t *testing.T) {
		out, err := testutil.NewApp(t).CaptureMessage(ctx, msg("a1", domain.RoleAssistant, "done", ""))
		if err != nil {
			t.Fatal(err)
		}
		if out.Message.Speaker != "subagent" {
			t.Fatalf("got %q", out.Message.Speaker)
		}
	})

	t.Run("same prompt and role replaces the content", func(t *testing.T) {
		a := testutil.NewApp(t)
		first, err := a.CaptureMessage(ctx, msg("", domain.RoleAssistant, "draft", "p1"))
		if err != nil {
			t.Fatal(err)
		}
		second, err := a.CaptureMessage(ctx, msg("", domain.RoleAssistant, "final", "p1"))
		if err != nil {
			t.Fatal(err)
		}
		if second.Message.ID != first.Message.ID || second.Message.Content != "final" || second.Message.Seq != 1 {
			t.Fatalf("got %+v", second.Message)
		}
		next, err := a.CaptureMessage(ctx, msg("", domain.RoleAssistant, "other", "p2"))
		if err != nil {
			t.Fatal(err)
		}
		if next.Message.Seq != 2 {
			t.Fatalf("new prompt: seq %d", next.Message.Seq)
		}
	})

	t.Run("messages without prompt ids are all kept", func(t *testing.T) {
		a := testutil.NewApp(t)
		for want := 1; want <= 2; want++ {
			out, err := a.CaptureMessage(ctx, msg("", domain.RoleUser, "again", ""))
			if err != nil {
				t.Fatal(err)
			}
			if out.Message.Seq != want {
				t.Fatalf("seq %d, want %d", out.Message.Seq, want)
			}
		}
	})

	t.Run("concurrent sessions number their messages without gaps", func(t *testing.T) {
		a := testutil.NewApp(t)
		const sessions, perSession = 4, 10
		var wg sync.WaitGroup
		errs := make(chan error, sessions*perSession)
		for s := range sessions {
			for i := range perSession {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := a.CaptureMessage(ctx, app.CaptureMessageInput{
						CaptureKey: key(fmt.Sprintf("s%d", s), ""), Role: domain.RoleUser,
						Content: "m", PromptID: fmt.Sprintf("p%d", i),
					})
					errs <- err
				}()
			}
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		for s := range sessions {
			seen := map[int]bool{}
			for i := range perSession {
				// Re-sending a prompt replaces it, which returns its seq.
				out, err := a.CaptureMessage(ctx, app.CaptureMessageInput{
					CaptureKey: key(fmt.Sprintf("s%d", s), ""), Role: domain.RoleUser,
					Content: "m", PromptID: fmt.Sprintf("p%d", i),
				})
				if err != nil {
					t.Fatal(err)
				}
				seen[out.Message.Seq] = true
			}
			for want := 1; want <= perSession; want++ {
				if !seen[want] {
					t.Fatalf("session s%d: seq %d missing from %v", s, want, seen)
				}
			}
		}
	})

	invalid := []struct {
		name string
		in   app.CaptureMessageInput
	}{
		{"unknown role", msg("", "system", "x", "")},
		{"blank content", msg("", domain.RoleUser, " \n ", "")},
		{"empty session", app.CaptureMessageInput{CaptureKey: app.CaptureKey{Harness: "claude-code"}, Role: domain.RoleUser, Content: "x"}},
	}
	for _, tt := range invalid {
		t.Run(tt.name+" is invalid", func(t *testing.T) {
			if _, err := testutil.NewApp(t).CaptureMessage(ctx, tt.in); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
}
