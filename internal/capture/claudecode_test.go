package capture_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/capture"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

// recorder is the real App, keeping what each CaptureMessage stored so tests
// can read it back before a read operation for conversations exists.
type recorder struct {
	*app.App
	messages []domain.Message
}

func (r *recorder) CaptureMessage(ctx context.Context, in app.CaptureMessageInput) (app.CaptureMessageOutput, error) {
	out, err := r.App.CaptureMessage(ctx, in)
	if err == nil {
		r.messages = append(r.messages, out.Message)
	}
	return out, err
}

func newRecorder(t *testing.T) *recorder { return &recorder{App: testutil.NewApp(t)} }

// loci is the command the context block tells the agent to run.
const loci = "/usr/local/bin/loci"

func send(t *testing.T, svc app.Service, ev capture.Event) string {
	t.Helper()
	out, err := capture.ClaudeCode(context.Background(), svc, ev, loci)
	if err != nil {
		t.Fatalf("%s: %v", ev.HookEventName, err)
	}
	return out
}

func TestClaudeCode(t *testing.T) {
	ctx := context.Background()

	t.Run("a session's turns are stored in order, between the person and the agent", func(t *testing.T) {
		r := newRecorder(t)
		repo := t.TempDir()
		writeGit(t, repo, "ref: refs/heads/feat/fees\n", "[remote \"origin\"]\n\turl = git@github.com:Northwind/API.git\n")

		block := send(t, r, capture.Event{HookEventName: "SessionStart", SessionID: "s1", CWD: repo})
		send(t, r, capture.Event{HookEventName: "UserPromptSubmit", SessionID: "s1", PromptID: "p1", Prompt: "What is the cut-off?"})
		send(t, r, capture.Event{HookEventName: "Stop", SessionID: "s1", PromptID: "p1", LastAssistantMessage: "16:00 CET"})

		if len(r.messages) != 2 {
			t.Fatalf("got %d messages", len(r.messages))
		}
		p, a := r.messages[0], r.messages[1]
		if p.Role != domain.RoleUser || p.Content != "What is the cut-off?" || p.Speaker != testutil.UserHandle || p.Addressee != capture.Harness || p.PromptID != "p1" {
			t.Fatalf("prompt %+v", p)
		}
		if a.Role != domain.RoleAssistant || a.Content != "16:00 CET" || a.Speaker != capture.Harness || a.Seq != 2 {
			t.Fatalf("answer %+v", a)
		}
		out, err := r.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: app.CaptureKey{Harness: capture.Harness, Session: "s1"}})
		if err != nil {
			t.Fatal(err)
		}
		if c := out.Conversation; out.Created || c.Repo != "github.com/northwind/api" || c.Branch != "feat/fees" {
			t.Fatalf("conversation %+v created=%v", c, out.Created)
		}
		if want := "Loci: this session is conversation " + out.Conversation.ID + ", not attached to a project or task.\n"; !strings.HasPrefix(block, want) {
			t.Fatalf("block %q, want it to start %q", block, want)
		}
	})

	t.Run("an unattached session is told how to attach it with its first prompt, not at start", func(t *testing.T) {
		r := newRecorder(t)
		start := send(t, r, capture.Event{HookEventName: "SessionStart", SessionID: "s1"})
		first := send(t, r, capture.Event{HookEventName: "UserPromptSubmit", SessionID: "s1", PromptID: "p1", Prompt: "Fix the cut-off."})
		send(t, r, capture.Event{HookEventName: "Stop", SessionID: "s1", PromptID: "p1", LastAssistantMessage: "Done."})
		second := send(t, r, capture.Event{HookEventName: "UserPromptSubmit", SessionID: "s1", PromptID: "p2", Prompt: "Thanks."})
		out, err := r.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: app.CaptureKey{Harness: capture.Harness, Session: "s1"}})
		if err != nil {
			t.Fatal(err)
		}
		id := out.Conversation.ID
		if strings.Contains(start, " attach ") ||
			!strings.Contains(start, "`"+loci+" remember \"<statement>\" --conversation "+id+" --author claude-code") {
			t.Fatalf("start block %q: want remember, not attach", start)
		}
		for _, want := range []string{
			"Loci: this session, conversation " + id + ", is still not attached to a project or task.\n",
			"`" + loci + " ls tasks <query>`",
			"`" + loci + " attach " + id + " --task <KEY>`",
			"Say /loci to change.",
			"do not guess",
		} {
			if !strings.Contains(first, want) {
				t.Fatalf("first prompt's block %q lacks %q", first, want)
			}
		}
		if strings.Contains(first, "{") {
			t.Fatalf("a placeholder was left in %q", first)
		}
		if second != "" {
			t.Fatalf("a later prompt printed %q", second)
		}
	})

	t.Run("a session the person attached before the first prompt is not told to attach", func(t *testing.T) {
		r := newRecorder(t)
		send(t, r, capture.Event{HookEventName: "SessionStart", SessionID: "s1"})
		out, err := r.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: app.CaptureKey{Harness: capture.Harness, Session: "s1"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Attach(ctx, app.AttachInput{Item: out.Conversation.ID, Task: ptr("EPD-1")}); err != nil {
			t.Fatal(err)
		}
		if block := send(t, r, capture.Event{HookEventName: "UserPromptSubmit", SessionID: "s1", PromptID: "p1", Prompt: "Fix it."}); block != "" {
			t.Fatalf("first prompt printed %q", block)
		}
	})

	t.Run("an answer captured again for the same prompt replaces the first", func(t *testing.T) {
		r := newRecorder(t)
		send(t, r, capture.Event{HookEventName: "Stop", SessionID: "s1", PromptID: "p1", LastAssistantMessage: "draft"})
		send(t, r, capture.Event{HookEventName: "Stop", SessionID: "s1", PromptID: "p1", LastAssistantMessage: "final"})
		if r.messages[0].ID != r.messages[1].ID || r.messages[1].Content != "final" {
			t.Fatalf("got %+v", r.messages)
		}
	})

	t.Run("an answer with no text is skipped", func(t *testing.T) {
		r := newRecorder(t)
		send(t, r, capture.Event{HookEventName: "Stop", SessionID: "s1", PromptID: "p1", LastAssistantMessage: " \n"})
		if len(r.messages) != 0 {
			t.Fatalf("got %+v", r.messages)
		}
	})

	t.Run("a subagent run is its own conversation under the session", func(t *testing.T) {
		r := newRecorder(t)
		main := send(t, r, capture.Event{HookEventName: "SessionStart", SessionID: "s1"})
		send(t, r, capture.Event{HookEventName: "SubagentStart", SessionID: "s1", AgentID: "a1", AgentType: "review-kit:code-reviewer"})
		send(t, r, capture.Event{HookEventName: "SubagentStop", SessionID: "s1", AgentID: "a1", AgentType: "review-kit:code-reviewer",
			PromptID: "p1", LastAssistantMessage: "No findings."})

		m := r.messages[0]
		if m.Speaker != "review-kit-code-reviewer" || m.Addressee != capture.Harness || m.Content != "No findings." {
			t.Fatalf("message %+v", m)
		}
		out, err := r.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: app.CaptureKey{Harness: capture.Harness, Session: "s1", Agent: "a1"}})
		if err != nil {
			t.Fatal(err)
		}
		if c := out.Conversation; c.ID != m.ConversationID || c.AgentType != "review-kit:code-reviewer" || !strings.Contains(main, c.Parent) {
			t.Fatalf("subagent conversation %+v, main block %q", c, main)
		}
	})

	t.Run("Claude Code's background helpers, which have no agent type, are not stored", func(t *testing.T) {
		r := newRecorder(t)
		// The prompt suggestion agent fires SubagentStop with no type and no
		// SubagentStart, carrying a guess at the person's next prompt.
		send(t, r, capture.Event{HookEventName: "SubagentStart", SessionID: "s1", AgentID: "a1"})
		send(t, r, capture.Event{HookEventName: "SubagentStop", SessionID: "s1", AgentID: "a1", PromptID: "p1",
			LastAssistantMessage: "yes add it to gitignore"})
		if len(r.messages) != 0 {
			t.Fatalf("got %+v", r.messages)
		}
		out, err := r.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: app.CaptureKey{Harness: capture.Harness, Session: "s1", Agent: "a1"}})
		if err != nil || !out.Created {
			t.Fatalf("a conversation exists for the helper: %+v, %v", out, err)
		}
	})

	t.Run("an attached session is told how to remember but not to attach", func(t *testing.T) {
		r := newRecorder(t)
		out, err := r.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: app.CaptureKey{Harness: capture.Harness, Session: "s1"},
			Project: "fees-v2", Task: "EPD-1"})
		if err != nil {
			t.Fatal(err)
		}
		block := send(t, r, capture.Event{HookEventName: "SessionStart", SessionID: "s1"})
		if want := "Loci: this session is conversation " + out.Conversation.ID + ", project fees-v2, task EPD-1.\n"; !strings.HasPrefix(block, want) {
			t.Fatalf("block %q, want it to start %q", block, want)
		}
		if strings.Contains(block, " attach ") || !strings.Contains(block, "`"+loci+" ls tags`") ||
			!strings.Contains(block, "--conversation "+out.Conversation.ID+" --author claude-code") {
			t.Fatalf("block %q", block)
		}
	})

	t.Run("events capture does not use are ignored", func(t *testing.T) {
		r := newRecorder(t)
		for _, name := range []string{"SessionEnd", "PreToolUse", ""} {
			if out := send(t, r, capture.Event{HookEventName: name, SessionID: "s1"}); out != "" {
				t.Fatalf("%q printed %q", name, out)
			}
		}
		if len(r.messages) != 0 {
			t.Fatalf("got %+v", r.messages)
		}
	})

	t.Run("an event the operations reject is an error", func(t *testing.T) {
		_, err := capture.ClaudeCode(ctx, newRecorder(t), capture.Event{HookEventName: "UserPromptSubmit", Prompt: "hi"}, loci)
		if err == nil {
			t.Fatal("no session id: want an error")
		}
	})
}

// writeGit creates dir/.git with the given HEAD and config.
func writeGit(t *testing.T, dir, head, config string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(gitDir, "HEAD"), head)
	if config != "" {
		writeFile(t, filepath.Join(gitDir, "config"), config)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }
