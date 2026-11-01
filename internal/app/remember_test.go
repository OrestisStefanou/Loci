package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

func TestRemember(t *testing.T) {
	tests := []struct {
		name    string
		in      app.RememberInput
		want    domain.Memory // ID and times are ignored
		wantErr error
	}{
		{
			name: "content alone is enough; author defaults to the configured person",
			in:   app.RememberInput{Content: "  keep this  "},
			want: domain.Memory{Content: "keep this", Tags: []string{}, Author: testutil.UserHandle, Status: domain.MemoryActive},
		},
		{
			name: "normalises project, task, tags and agent author; drops duplicate tags",
			in:   app.RememberInput{Content: "x", Project: " Northwind ", Task: "epd-1", Tags: []string{"Auth", "perf", "auth"}, Author: " Claude "},
			want: domain.Memory{Content: "x", Project: "northwind", Task: "EPD-1", Tags: []string{"auth", "perf"}, Author: "claude", Status: domain.MemoryActive},
		},
		{name: "empty content is invalid", in: app.RememberInput{Content: " \n"}, wantErr: domain.ErrInvalid},
		{name: "bad project is invalid", in: app.RememberInput{Content: "x", Project: "a/b"}, wantErr: domain.ErrInvalid},
		{name: "bad task is invalid", in: app.RememberInput{Content: "x", Task: "EPD 1"}, wantErr: domain.ErrInvalid},
		{name: "bad tag is invalid", in: app.RememberInput{Content: "x", Tags: []string{"ok", ""}}, wantErr: domain.ErrInvalid},
		{name: "bad author is invalid", in: app.RememberInput{Content: "x", Author: "a@b"}, wantErr: domain.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			a := testutil.NewApp(t)
			out, err := a.Remember(ctx, tt.in)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("want %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := out.Memory
			if got.ID == "" || !got.CreatedAt.Equal(testutil.Epoch) || !got.UpdatedAt.Equal(testutil.Epoch) {
				t.Fatalf("id/times: %+v", got)
			}
			got.ID, got.CreatedAt, got.UpdatedAt = "", tt.want.CreatedAt, tt.want.UpdatedAt
			if got.Content != tt.want.Content || got.Project != tt.want.Project || got.Task != tt.want.Task ||
				got.Author != tt.want.Author || got.Status != tt.want.Status || !slices.Equal(got.Tags, tt.want.Tags) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRememberCreatesLazilyAndKeepsTaskProject(t *testing.T) {
	ctx := context.Background()
	a := testutil.NewApp(t)
	if _, err := a.SetTaskProject(ctx, app.SetTaskProjectInput{Task: "EPD-1", Project: ptr("northwind")}); err != nil {
		t.Fatal(err)
	}
	// A different project on the memory does not move the task.
	out, err := a.Remember(ctx, app.RememberInput{Content: "x", Project: "other", Task: "EPD-1", Tags: []string{"new-tag"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Memory.Project != "northwind" {
		t.Fatalf("effective project %q, want the task's (northwind)", out.Memory.Project)
	}
	for kind, want := range map[domain.Kind][]string{
		domain.KindProject: {"northwind", "other"},
		domain.KindTask:    {"EPD-1"},
		domain.KindTag:     {"new-tag"},
	} {
		list, err := a.List(ctx, app.ListInput{Kind: kind})
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, it := range list.Items {
			names = append(names, it.Name)
		}
		if !slices.Equal(names, want) {
			t.Fatalf("%s: got %v, want %v", kind, names, want)
		}
	}
	task, _ := a.List(ctx, app.ListInput{Kind: domain.KindTask})
	if task.Items[0].Project != "northwind" {
		t.Fatalf("task project changed to %q", task.Items[0].Project)
	}
}

func TestRememberInConversation(t *testing.T) {
	ctx := context.Background()
	a := testutil.NewApp(t)
	start := func(agent, agentType string) domain.Conversation {
		t.Helper()
		out, err := a.CaptureStart(ctx, app.CaptureStartInput{
			CaptureKey: app.CaptureKey{Harness: "claude-code", Session: "s1", Agent: agent}, AgentType: agentType})
		if err != nil {
			t.Fatal(err)
		}
		return out.Conversation
	}
	remember := func(in app.RememberInput) domain.Memory {
		t.Helper()
		out, err := a.Remember(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return out.Memory
	}
	attach := func(item string, task string) {
		t.Helper()
		if _, err := a.Attach(ctx, app.AttachInput{Item: item, Task: &task}); err != nil {
			t.Fatal(err)
		}
	}
	search := func(in app.SearchMemoriesInput) []string {
		t.Helper()
		out, err := a.SearchMemories(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, m := range out.Memories {
			ids = append(ids, m.ID)
		}
		return ids
	}
	project := "fees-v2"
	if _, err := a.SetTaskProject(ctx, app.SetTaskProjectInput{Task: "EPD-1", Project: &project}); err != nil {
		t.Fatal(err)
	}

	session := start("", "")
	run := start("a1", "Explore") // a subagent run shares its session's attachment
	saved := remember(app.RememberInput{Content: "cut-off is 16:00 CET", Conversation: session.ID})
	fromRun := remember(app.RememberInput{Content: "the ledger API pages at 100", Conversation: run.ID})
	own := remember(app.RememberInput{Content: "own task wins", Conversation: session.ID, Task: "EPD-2"})

	t.Run("the source conversation is stored and returned", func(t *testing.T) {
		if saved.Conversation != session.ID || saved.Task != "" || saved.Project != "" {
			t.Fatalf("got %+v", saved)
		}
	})
	t.Run("attaching the conversation later attaches its memories", func(t *testing.T) {
		attach(session.ID, "EPD-1")
		got := search(app.SearchMemoriesInput{Task: "EPD-1"})
		if !slices.Equal(got, []string{fromRun.ID, saved.ID}) {
			t.Fatalf("by task: %v", got)
		}
		if got := search(app.SearchMemoriesInput{Project: "fees-v2"}); !slices.Equal(got, []string{fromRun.ID, saved.ID}) {
			t.Fatalf("by project: %v", got)
		}
		m, err := a.SearchMemories(ctx, app.SearchMemoriesInput{Query: "cut-off"})
		if err != nil || len(m.Memories) != 1 || m.Memories[0].Task != "EPD-1" || m.Memories[0].Project != "fees-v2" {
			t.Fatalf("shown as %+v, %v", m.Memories, err)
		}
	})
	t.Run("a memory's own attachment wins over its conversation's", func(t *testing.T) {
		if got := search(app.SearchMemoriesInput{Task: "EPD-2"}); !slices.Equal(got, []string{own.ID}) {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("re-attaching the conversation moves its memories", func(t *testing.T) {
		attach(session.ID, "EPD-3")
		if got := search(app.SearchMemoriesInput{Task: "EPD-1"}); len(got) != 0 {
			t.Fatalf("still on EPD-1: %v", got)
		}
		if got := search(app.SearchMemoriesInput{Task: "EPD-3"}); !slices.Equal(got, []string{fromRun.ID, saved.ID}) {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("removing a memory's own task makes it share its conversation's again", func(t *testing.T) {
		attach(own.ID, "")
		if got := search(app.SearchMemoriesInput{Task: "EPD-3"}); !slices.Equal(got, []string{own.ID, fromRun.ID, saved.ID}) {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("an unknown conversation is not found and nothing is saved", func(t *testing.T) {
		_, err := a.Remember(ctx, app.RememberInput{Content: "orphan", Conversation: "nope"})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("got %v", err)
		}
		if got := search(app.SearchMemoriesInput{Query: "orphan"}); len(got) != 0 {
			t.Fatalf("saved anyway: %v", got)
		}
	})
}
