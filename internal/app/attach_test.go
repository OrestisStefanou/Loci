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

func tagList(tags ...string) *[]string { return &tags }

func TestAttach(t *testing.T) {
	// Each case starts from one memory on project "northwind", task "EPD-1"
	// (which has no project) and tags [auth, perf].
	tests := []struct {
		name        string
		in          app.AttachInput // Item is filled in
		wantProject string
		wantTask    string
		wantTags    []string
		wantErr     error
	}{
		{
			name:        "sets project, task and tags, normalising and creating them",
			in:          app.AttachInput{Project: ptr(" Settlements "), Task: ptr("epd-2"), Tags: tagList("Decided", "decided", "new")},
			wantProject: "settlements", wantTask: "EPD-2", wantTags: []string{"decided", "new"},
		},
		{
			name:        "leaves omitted fields unchanged",
			in:          app.AttachInput{Task: ptr("EPD-2")},
			wantProject: "northwind", wantTask: "EPD-2", wantTags: []string{"auth", "perf"},
		},
		{
			name:     "empty project removes it",
			in:       app.AttachInput{Project: ptr("")},
			wantTask: "EPD-1", wantTags: []string{"auth", "perf"},
		},
		{
			name:        "empty task removes it",
			in:          app.AttachInput{Task: ptr(" ")},
			wantProject: "northwind", wantTags: []string{"auth", "perf"},
		},
		{
			name:        "tags replace the set",
			in:          app.AttachInput{Tags: tagList("perf", "decided")},
			wantProject: "northwind", wantTask: "EPD-1", wantTags: []string{"decided", "perf"},
		},
		{
			name:        "empty tags remove them all",
			in:          app.AttachInput{Tags: tagList()},
			wantProject: "northwind", wantTask: "EPD-1", wantTags: []string{},
		},
		{name: "nothing to change is invalid", in: app.AttachInput{}, wantErr: domain.ErrInvalid},
		{name: "bad project is invalid", in: app.AttachInput{Project: ptr("a/b")}, wantErr: domain.ErrInvalid},
		{name: "bad task is invalid", in: app.AttachInput{Task: ptr("EPD 1")}, wantErr: domain.ErrInvalid},
		{name: "bad tag is invalid", in: app.AttachInput{Tags: tagList("ok", "a b")}, wantErr: domain.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			a := testutil.NewApp(t)
			mem, err := a.Remember(ctx, app.RememberInput{Content: "x", Project: "northwind", Task: "EPD-1", Tags: []string{"auth", "perf"}})
			if err != nil {
				t.Fatal(err)
			}
			tt.in.Item = " " + mem.Memory.ID + " "
			out, err := a.Attach(ctx, tt.in)
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
			if got.ID != mem.Memory.ID || got.Project != tt.wantProject || got.Task != tt.wantTask || !slices.Equal(got.Tags, tt.wantTags) {
				t.Fatalf("got %s %q/%q %v, want %q/%q %v",
					got.ID, got.Project, got.Task, got.Tags, tt.wantProject, tt.wantTask, tt.wantTags)
			}
		})
	}
}

func TestAttachErrors(t *testing.T) {
	ctx := context.Background()
	a := testutil.NewApp(t)
	if _, err := a.Attach(ctx, app.AttachInput{Item: "nope", Task: ptr("EPD-1")}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown item: want ErrNotFound, got %v", err)
	}
	if _, err := a.Attach(ctx, app.AttachInput{Item: " ", Task: ptr("EPD-1")}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty item: want ErrInvalid, got %v", err)
	}
	// A failed attach creates nothing.
	out, err := a.List(ctx, app.ListInput{Kind: domain.KindTask})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("tasks created by a failed attach: %+v", out.Items)
	}
}

func TestAttachedMemoryIsFoundUnderItsNewAttachment(t *testing.T) {
	ctx := context.Background()
	a := testutil.NewApp(t)
	if _, err := a.SetTaskProject(ctx, app.SetTaskProjectInput{Task: "EPD-2", Project: ptr("settlements")}); err != nil {
		t.Fatal(err)
	}
	mem, err := a.Remember(ctx, app.RememberInput{Content: "x", Project: "northwind", Tags: []string{"auth"}})
	if err != nil {
		t.Fatal(err)
	}
	// The memory keeps its own project, but the task's project wins.
	out, err := a.Attach(ctx, app.AttachInput{Item: mem.Memory.ID, Task: ptr("EPD-2"), Tags: tagList("decided")})
	if err != nil {
		t.Fatal(err)
	}
	if out.Memory.Project != "settlements" {
		t.Fatalf("effective project %q, want the task's (settlements)", out.Memory.Project)
	}
	count := func(in app.SearchMemoriesInput) int {
		res, err := a.SearchMemories(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return len(res.Memories)
	}
	for _, c := range []struct {
		name string
		in   app.SearchMemoriesInput
		want int
	}{
		{"new project", app.SearchMemoriesInput{Project: "settlements"}, 1},
		{"old project", app.SearchMemoriesInput{Project: "northwind"}, 0},
		{"new task", app.SearchMemoriesInput{Task: "EPD-2"}, 1},
		{"new tag", app.SearchMemoriesInput{Tags: []string{"decided"}}, 1},
		{"old tag", app.SearchMemoriesInput{Tags: []string{"auth"}}, 0},
	} {
		if n := count(c.in); n != c.want {
			t.Errorf("%s: got %d, want %d", c.name, n, c.want)
		}
	}
	// Removing the task falls back to the memory's own project.
	out, err = a.Attach(ctx, app.AttachInput{Item: mem.Memory.ID, Task: ptr("")})
	if err != nil {
		t.Fatal(err)
	}
	if out.Memory.Project != "northwind" || out.Memory.Task != "" {
		t.Fatalf("after removing the task: %+v", out.Memory)
	}
}

func TestAttachInactiveMemory(t *testing.T) {
	ctx := context.Background()
	a := testutil.NewApp(t)
	mem, err := a.Remember(ctx, app.RememberInput{Content: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Mark(ctx, app.MarkInput{ID: mem.Memory.ID, Status: domain.MemoryInactive}); err != nil {
		t.Fatal(err)
	}
	out, err := a.Attach(ctx, app.AttachInput{Item: mem.Memory.ID, Task: ptr("EPD-1")})
	if err != nil {
		t.Fatal(err)
	}
	if out.Memory.Task != "EPD-1" || out.Memory.Status != domain.MemoryInactive {
		t.Fatalf("got %+v", out.Memory)
	}
}
