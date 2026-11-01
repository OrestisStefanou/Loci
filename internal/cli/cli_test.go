package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/cli"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/embed"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

// run executes the CLI against svc in-process and returns stdout, stderr and
// the exit code. The socket path to loci serve is covered by internal/rpc,
// internal/daemon and cmd/loci tests.
func run(t *testing.T, svc app.Service, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := cli.Env{
		Stdout:  &stdout,
		Stderr:  &stderr,
		Service: func(context.Context, string) (app.Service, error) { return svc, nil },
	}
	code := cli.Execute(context.Background(), env, append([]string{"--db", "unused.db"}, args...))
	return stdout.String(), stderr.String(), code
}

func TestTaskAndLs(t *testing.T) {
	svc := testutil.NewApp(t)

	out, errOut, code := run(t, svc, "task", "epd-1271", "--project", "Northwind")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "EPD-1271") || !strings.Contains(out, "northwind") {
		t.Fatalf("unexpected output %q", out)
	}

	out, _, code = run(t, svc, "ls", "tasks")
	if code != 0 || !strings.Contains(out, "EPD-1271") || !strings.Contains(out, "northwind") {
		t.Fatalf("ls tasks: exit %d, output %q", code, out)
	}

	out, _, code = run(t, svc, "--json", "ls", "projects")
	if code != 0 {
		t.Fatalf("ls projects --json: exit %d", code)
	}
	var list struct {
		Kind  string `json:"kind"`
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if list.Kind != "projects" || len(list.Items) != 1 || list.Items[0].Name != "northwind" {
		t.Fatalf("got %+v", list)
	}

	out, _, code = run(t, svc, "task", "EPD-1271", "--no-project")
	if code != 0 || !strings.Contains(out, "(no project)") {
		t.Fatalf("--no-project: exit %d, output %q", code, out)
	}
}

func TestExitCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"empty task key", []string{"task", "", "--project", "x"}, 2},
		{"neither project flag", []string{"task", "EPD-1"}, 2},
		{"both project flags", []string{"task", "EPD-1", "--project", "x", "--no-project"}, 2},
		{"missing argument", []string{"task"}, 2},
		{"unknown flag", []string{"ls", "tasks", "--nope"}, 2},
		{"unknown kind", []string{"ls", "repos"}, 2},
		{"empty memory content", []string{"remember", " "}, 2},
		{"remember without content", []string{"remember"}, 2},
		{"bad tag", []string{"remember", "x", "--tag", "a b"}, 2},
		{"remember in an unknown conversation", []string{"remember", "x", "--conversation", "nope"}, 1},
		{"negative limit", []string{"memories", "--limit", "-1"}, 2},
		{"non-numeric limit", []string{"memories", "--limit", "x"}, 2},
		{"memories takes no arguments", []string{"memories", "EPD-1"}, 2},
		{"mark without a status", []string{"mark", "x"}, 2},
		{"mark with two statuses", []string{"mark", "x", "--inactive", "--active"}, 2},
		{"mark without an id", []string{"mark", "--inactive"}, 2},
		{"mark unknown memory", []string{"mark", "nope", "--inactive"}, 1},
		{"attach without changes", []string{"attach", "x"}, 2},
		{"attach with both project flags", []string{"attach", "x", "--project", "a", "--no-project"}, 2},
		{"attach with both task flags", []string{"attach", "x", "--task", "EPD-1", "--no-task"}, 2},
		{"attach with both tag flags", []string{"attach", "x", "--tag", "a", "--no-tags"}, 2},
		{"attach unknown memory", []string{"attach", "nope", "--task", "EPD-1"}, 1},
		{"capture start without harness", []string{"capture", "start", "--session", "s1"}, 2},
		{"capture start without session", []string{"capture", "start", "--harness", "h"}, 2},
		{"capture message without role", []string{"capture", "message", "x", "--harness", "h", "--session", "s1"}, 2},
		{"capture message with bad role", []string{"capture", "message", "x", "--harness", "h", "--session", "s1", "--role", "system"}, 2},
		{"capture message without content", []string{"capture", "message", "--harness", "h", "--session", "s1", "--role", "user"}, 2},
		{"help", []string{"--help"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, code := run(t, testutil.NewApp(t), tt.args...)
			if code != tt.want {
				t.Fatalf("exit %d, want %d (stderr %q)", code, tt.want, stderr)
			}
			if tt.want != 0 && stderr == "" {
				t.Fatal("want an error message on stderr")
			}
		})
	}
}

func TestRememberAndMemories(t *testing.T) {
	svc := testutil.NewApp(t)

	out, errOut, code := run(t, svc, "remember", "Settlement cut-off is 16:00", "--task", "epd-1", "--tag", "Decided", "--tag", "auth")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"EPD-1", "[auth, decided]", "tester", "  Settlement cut-off is 16:00"} {
		if !strings.Contains(out, want) {
			t.Fatalf("remember output %q lacks %q", out, want)
		}
	}

	out, _, code = run(t, svc, "remember", "second", "--project", "northwind", "--author", "claude")
	if code != 0 || !strings.Contains(out, "claude") || !strings.Contains(out, "northwind") {
		t.Fatalf("remember with author: exit %d, output %q", code, out)
	}

	out, _, code = run(t, svc, "memories")
	if code != 0 {
		t.Fatalf("memories: exit %d", code)
	}
	if strings.Index(out, "second") > strings.Index(out, "Settlement") {
		t.Fatalf("want newest first:\n%s", out)
	}

	out, _, code = run(t, svc, "memories", "--task", "EPD-1", "--tag", "decided", "--query", "CUT-OFF")
	if code != 0 || !strings.Contains(out, "Settlement") || strings.Contains(out, "second") {
		t.Fatalf("filtered memories: exit %d, output %q", code, out)
	}

	out, _, code = run(t, svc, "--json", "memories", "--project", "northwind")
	if code != 0 {
		t.Fatalf("memories --json: exit %d", code)
	}
	var got struct {
		Memories []struct {
			Content string   `json:"content"`
			Project string   `json:"project"`
			Tags    []string `json:"tags"`
			Author  string   `json:"author"`
			Status  string   `json:"status"`
		} `json:"memories"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if len(got.Memories) != 1 || got.Memories[0].Content != "second" || got.Memories[0].Project != "northwind" ||
		got.Memories[0].Author != "claude" || got.Memories[0].Status != "active" || got.Memories[0].Tags == nil {
		t.Fatalf("got %+v", got)
	}

	out, _, code = run(t, svc, "--json", "memories", "--project", "nope")
	if code != 0 || !strings.Contains(out, `"memories": []`) {
		t.Fatalf("unknown project: exit %d, output %q", code, out)
	}
}

func TestRememberInConversation(t *testing.T) {
	svc := testutil.NewApp(t)
	c, err := svc.CaptureStart(context.Background(), app.CaptureStartInput{CaptureKey: app.CaptureKey{Harness: "claude-code", Session: "s1"}})
	if err != nil {
		t.Fatal(err)
	}
	id := c.Conversation.ID

	out, errOut, code := run(t, svc, "remember", "Refunds over 500 need a second approver", "--conversation", id, "--author", "claude-code")
	if code != 0 || !strings.Contains(out, "from "+id) {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if _, errOut, code := run(t, svc, "attach", id, "--task", "EPD-9"); code != 0 {
		t.Fatalf("attach: exit %d: %s", code, errOut)
	}
	out, _, code = run(t, svc, "--json", "memories", "--task", "EPD-9")
	if code != 0 || !strings.Contains(out, `"conversation": "`+id+`"`) || !strings.Contains(out, `"task": "EPD-9"`) {
		t.Fatalf("exit %d, output %q", code, out)
	}
}

// remember saves a memory through the CLI and returns its ID.
func remember(t *testing.T, svc app.Service, args ...string) string {
	t.Helper()
	out, errOut, code := run(t, svc, append([]string{"--json", "remember"}, args...)...)
	if code != 0 {
		t.Fatalf("remember: exit %d: %s", code, errOut)
	}
	var got struct {
		Memory struct {
			ID string `json:"id"`
		} `json:"memory"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Memory.ID == "" {
		t.Fatalf("remember output %q: %v", out, err)
	}
	return got.Memory.ID
}

func TestFlagErrorsNameFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"mark without a status", []string{"mark", "x"}, "--inactive or --active"},
		{"mark with two statuses", []string{"mark", "x", "--inactive", "--active"}, "--inactive or --active"},
		{"attach without changes", []string{"attach", "x"}, "--project, --no-project, --task, --no-task, --tag or --no-tags"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, code := run(t, testutil.NewApp(t), tt.args...)
			if code != 2 || !strings.Contains(stderr, tt.want) {
				t.Fatalf("exit %d, stderr %q; want exit 2 naming %q", code, stderr, tt.want)
			}
		})
	}
}

func TestMark(t *testing.T) {
	svc := testutil.NewApp(t)
	old := remember(t, svc, "cut-off is 15:00", "--task", "EPD-1")
	remember(t, svc, "cut-off is 16:00", "--task", "EPD-1")

	out, errOut, code := run(t, svc, "mark", old, "--inactive")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "(inactive)") {
		t.Fatalf("mark output %q", out)
	}

	out, _, _ = run(t, svc, "memories", "--task", "EPD-1")
	if strings.Contains(out, "15:00") || !strings.Contains(out, "16:00") {
		t.Fatalf("want only the active memory:\n%s", out)
	}
	out, _, _ = run(t, svc, "memories", "--task", "EPD-1", "--all")
	if !strings.Contains(out, "15:00") {
		t.Fatalf("--all lacks the inactive memory:\n%s", out)
	}

	out, _, code = run(t, svc, "--json", "mark", old, "--active")
	if code != 0 {
		t.Fatalf("mark --active --json: exit %d", code)
	}
	var got struct {
		Memory struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"memory"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if got.Memory.ID != old || got.Memory.Status != "active" {
		t.Fatalf("got %+v", got)
	}
}

func TestAttach(t *testing.T) {
	svc := testutil.NewApp(t)
	id := remember(t, svc, "cut-off is 16:00", "--project", "northwind", "--tag", "auth")

	out, errOut, code := run(t, svc, "attach", id, "--task", "epd-1", "--tag", "Decided", "--tag", "perf")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "northwind/EPD-1 [decided, perf]") {
		t.Fatalf("attach output %q", out)
	}

	out, _, code = run(t, svc, "--json", "attach", id, "--no-project", "--no-tags")
	if code != 0 {
		t.Fatalf("attach --json: exit %d", code)
	}
	var got struct {
		Memory struct {
			Project string   `json:"project"`
			Task    string   `json:"task"`
			Tags    []string `json:"tags"`
		} `json:"memory"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if got.Memory.Project != "" || got.Memory.Task != "EPD-1" || got.Memory.Tags == nil || len(got.Memory.Tags) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestRememberReadsStdin(t *testing.T) {
	svc := testutil.NewApp(t)
	var stdout, stderr bytes.Buffer
	env := cli.Env{
		Stdout:  &stdout,
		Stderr:  &stderr,
		Stdin:   strings.NewReader("from stdin\nsecond line\n"),
		Service: func(context.Context, string) (app.Service, error) { return svc, nil },
	}
	code := cli.Execute(context.Background(), env, []string{"--db", "unused.db", "remember", "-"})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "  from stdin\n  second line\n") {
		t.Fatalf("output %q", stdout.String())
	}
}

func TestEmbed(t *testing.T) {
	t.Setenv("LOCI_CONFIG", filepath.Join(t.TempDir(), "absent.toml"))
	rememberAll := func(t *testing.T, svc app.Service, n int) {
		t.Helper()
		for i := range n {
			if _, err := svc.Remember(context.Background(), app.RememberInput{Content: fmt.Sprintf("memory %d", i)}); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("embeds everything pending and reports the total", func(t *testing.T) {
		svc := testutil.NewAppWithEmbedder(t, embed.NewFake("fake-model"))
		rememberAll(t, svc, 70) // more than one batch of 64
		out, errOut, code := run(t, svc, "embed")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if !strings.Contains(out, "embedded 64, 6 pending") || !strings.Contains(out, "embedded 70, everything embedded with fake-model") {
			t.Fatalf("output %q", out)
		}

		out, _, code = run(t, svc, "--json", "embed", "--backfill")
		var got app.EmbedOutput
		if err := json.Unmarshal([]byte(out), &got); code != 0 || err != nil {
			t.Fatalf("exit %d, invalid JSON %q: %v", code, out, err)
		}
		if !reflect.DeepEqual(got, app.EmbedOutput{Model: "fake-model", Embedded: 70, Pending: 0, Enabled: true}) {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("reports rows set aside", func(t *testing.T) {
		fake := embed.NewFake("fake-model")
		fake.Reject("bad", errors.New("ollama: 500 Internal Server Error"))
		svc := testutil.NewAppWithEmbedder(t, fake)
		for _, content := range []string{"bad", "good"} {
			if _, err := svc.Remember(context.Background(), app.RememberInput{Content: content}); err != nil {
				t.Fatal(err)
			}
		}
		out, errOut, code := run(t, svc, "embed")
		if code != 0 || !strings.Contains(out, "set aside 1 the provider rejected") || !strings.Contains(out, "embedded 1, everything embedded") {
			t.Fatalf("exit %d, output %q, stderr %q", code, out, errOut)
		}
	})

	t.Run("embedding off says so and succeeds", func(t *testing.T) {
		out, errOut, code := run(t, testutil.NewApp(t), "embed")
		if code != 0 || !strings.Contains(out, "embedding is off") {
			t.Fatalf("exit %d, output %q, stderr %q", code, out, errOut)
		}
	})

	t.Run("unreachable provider fails with a hint", func(t *testing.T) {
		fake := embed.NewFake("fake-model")
		fake.Fail(fmt.Errorf("refused: %w", domain.ErrUnavailable))
		svc := testutil.NewAppWithEmbedder(t, fake)
		rememberAll(t, svc, 1)
		_, errOut, code := run(t, svc, "embed")
		if code != 1 || !strings.Contains(errOut, "ollama pull nomic-embed-text") {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
	})
}

func TestMemoriesNotesTextFallback(t *testing.T) {
	fake := embed.NewFake("fake-model")
	svc := testutil.NewAppWithEmbedder(t, fake)
	if _, err := svc.Remember(context.Background(), app.RememberInput{Content: "Settlement cut-off is 16:00 CET"}); err != nil {
		t.Fatal(err)
	}
	hybridOut, _, _ := run(t, svc, "memories", "--query", "cut-off")

	fake.Fail(fmt.Errorf("refused: %w", domain.ErrUnavailable))
	out, errOut, code := run(t, svc, "memories", "--query", "cut-off")
	if code != 0 || out != hybridOut || !strings.Contains(errOut, "semantic ranking unavailable") {
		t.Fatalf("exit %d, stdout %q (was %q), stderr %q", code, out, hybridOut, errOut)
	}

	out, _, _ = run(t, svc, "--json", "memories", "--query", "cut-off")
	if !strings.Contains(out, `"ranking": "text"`) || !strings.Contains(out, `"semantic_unavailable": true`) {
		t.Fatalf("json %q", out)
	}
}

func TestCapture(t *testing.T) {
	svc := testutil.NewApp(t)
	key := []string{"--harness", "my-tool", "--session", "s1"}

	out, errOut, code := run(t, svc, append([]string{"capture", "start", "--branch", "main", "--project", "Fees-V2", "--task", "epd-1"}, key...)...)
	if code != 0 || !strings.Contains(out, "fees-v2/EPD-1") || !strings.Contains(out, "started") {
		t.Fatalf("start: exit %d, output %q, stderr %q", code, out, errOut)
	}
	out, _, code = run(t, svc, append([]string{"--json", "capture", "start"}, key...)...)
	if code != 0 {
		t.Fatalf("resume: exit %d", code)
	}
	var started app.CaptureStartOutput
	if err := json.Unmarshal([]byte(out), &started); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if started.Created || started.Conversation.Branch != "main" || started.Conversation.Task != "EPD-1" {
		t.Fatalf("resume: %+v", started)
	}

	out, errOut, code = run(t, svc, append([]string{"capture", "message", "hi", "--role", "user", "--prompt-id", "p1"}, key...)...)
	if code != 0 || !strings.Contains(out, "#1") || !strings.Contains(out, "user") {
		t.Fatalf("message: exit %d, output %q, stderr %q", code, out, errOut)
	}
	if !strings.Contains(out, testutil.UserHandle+" -> my-tool") {
		t.Fatalf("message participants: %q", out)
	}

	var stdout, stderr bytes.Buffer
	env := cli.Env{
		Stdout:  &stdout,
		Stderr:  &stderr,
		Stdin:   strings.NewReader("hello\nthere\n"),
		Service: func(context.Context, string) (app.Service, error) { return svc, nil },
	}
	args := append([]string{"--db", "unused.db", "--json", "capture", "message", "-", "--role", "assistant", "--prompt-id", "p1"}, key...)
	if code := cli.Execute(context.Background(), env, args); code != 0 {
		t.Fatalf("message from stdin: exit %d: %s", code, stderr.String())
	}
	var msg app.CaptureMessageOutput
	if err := json.Unmarshal(stdout.Bytes(), &msg); err != nil {
		t.Fatalf("invalid JSON %q: %v", stdout.String(), err)
	}
	if msg.Message.Content != "hello\nthere\n" || msg.Message.Seq != 2 || msg.Message.Role != domain.RoleAssistant {
		t.Fatalf("got %+v", msg.Message)
	}
}

func TestConversationsNotesTextFallback(t *testing.T) {
	fake := embed.NewFake("fake-model")
	svc := testutil.NewAppWithEmbedder(t, fake)
	key := []string{"--harness", "claude-code", "--session", "s1"}
	for _, args := range [][]string{
		append([]string{"capture", "start"}, key...),
		append([]string{"capture", "message", "Where is the cut-off?", "--role", "user"}, key...),
	} {
		if _, errOut, code := run(t, svc, args...); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errOut)
		}
	}
	fake.Fail(fmt.Errorf("refused: %w", domain.ErrUnavailable))
	out, errOut, code := run(t, svc, "conversations", "--query", "cut-off")
	if code != 0 || !strings.Contains(out, `match: `) || !strings.Contains(errOut, "semantic ranking unavailable") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	out, _, _ = run(t, svc, "--json", "conversations", "--query", "cut-off")
	if !strings.Contains(out, `"ranking": "text"`) || !strings.Contains(out, `"semantic_unavailable": true`) ||
		!strings.Contains(out, `"snippet": "Where is the cut-off?"`) {
		t.Fatalf("json %q", out)
	}
}

func TestConversationsShowAndAttach(t *testing.T) {
	svc := testutil.NewApp(t)
	key := []string{"--harness", "claude-code", "--session", "s1"}
	steps := [][]string{
		append([]string{"capture", "start", "--repo", "git@github.com:northwind/api.git", "--branch", "main"}, key...),
		append([]string{"capture", "message", "Where is the cut-off?", "--role", "user"}, key...),
		append([]string{"capture", "message", "In settle.go.", "--role", "assistant"}, key...),
		append([]string{"capture", "message", "Found it.", "--role", "assistant", "--agent", "a1", "--agent-type", "Explore"}, key...),
	}
	for _, args := range steps {
		if _, errOut, code := run(t, svc, args...); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errOut)
		}
	}

	out, errOut, code := run(t, svc, "--json", "conversations")
	if code != 0 {
		t.Fatalf("conversations --json: exit %d: %s", code, errOut)
	}
	var list app.SearchConversationsOutput
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if len(list.Conversations) != 1 || list.Conversations[0].Messages != 2 || list.Conversations[0].Subagents != 1 {
		t.Fatalf("got %+v", list)
	}
	id := list.Conversations[0].ID

	out, _, code = run(t, svc, "conversations")
	for _, want := range []string{id, "(unattached)", "github.com/northwind/api@main", "2 messages, 1 subagent run", "Where is the cut-off?"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("conversations: exit %d, %q missing from %q", code, want, out)
		}
	}
	// The run matches its own message, and its session matches through it.
	out, _, code = run(t, svc, "conversations", "--subagents", "--query", "found")
	if code != 0 || !strings.Contains(out, "Explore run in "+id) || !strings.Contains(out, "1 message\n") || !strings.Contains(out, "Where is") {
		t.Fatalf("conversations --subagents: exit %d, %q", code, out)
	}
	// The session's match is the run's message, named with the run.
	if !regexp.MustCompile(`\n  match: \S+ in \S+ "Found it\."\n`).MatchString(out) {
		t.Fatalf("conversations --query: no match line naming the run in %q", out)
	}

	out, errOut, code = run(t, svc, "attach", id, "--project", "Fees-V2", "--task", "epd-1")
	if code != 0 || !strings.Contains(out, id) || !strings.Contains(out, "fees-v2/EPD-1") {
		t.Fatalf("attach: exit %d, %q, %s", code, out, errOut)
	}

	out, errOut, code = run(t, svc, "show", id)
	for _, want := range []string{"# Conversation " + id, "- Attached to: fees-v2/EPD-1", "**#1 " + testutil.UserHandle + " to claude-code**",
		"Where is the cut-off?", "## Subagent runs", "Explore, 1 message"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("show: exit %d, %q missing from %q (%s)", code, want, out, errOut)
		}
	}
	out, _, code = run(t, svc, "--json", "show", id)
	var shown app.GetConversationOutput
	if err := json.Unmarshal([]byte(out), &shown); code != 0 || err != nil {
		t.Fatalf("show --json: exit %d, %v, %q", code, err, out)
	}
	if len(shown.Messages) != 2 || shown.Conversation.Task != "EPD-1" {
		t.Fatalf("show --json: %+v", shown)
	}
	out, _, code = run(t, svc, "show", id, "--around", shown.Messages[1].ID, "--window", "0")
	if code != 0 || !strings.Contains(out, "- Messages: 2") {
		t.Fatalf("show --around: exit %d, %q", code, out)
	}

	for _, tt := range []struct {
		args []string
		code int
	}{
		{[]string{"show"}, 2},
		{[]string{"show", id, "--window", "3"}, 2},
		{[]string{"show", "nope"}, 1},
		{[]string{"conversations", "--limit", "-1"}, 2},
		{[]string{"conversations", "extra"}, 2},
	} {
		if _, _, code := run(t, svc, tt.args...); code != tt.code {
			t.Fatalf("%v: exit %d, want %d", tt.args, code, tt.code)
		}
	}
}
