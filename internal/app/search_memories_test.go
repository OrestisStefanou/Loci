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

func TestSearchMemories(t *testing.T) {
	ctx := context.Background()
	a := testutil.NewApp(t)
	must := func(in app.RememberInput) string {
		t.Helper()
		out, err := a.Remember(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return out.Memory.Content
	}
	if _, err := a.SetTaskProject(ctx, app.SetTaskProjectInput{Task: "EPD-1", Project: ptr("northwind")}); err != nil {
		t.Fatal(err)
	}
	// Inserted in this order; the fixed clock gives equal timestamps, so
	// the sequential IDs decide: newest (highest ID) first.
	onTask := must(app.RememberInput{Content: "Settlement cut-off is 16:00", Task: "EPD-1", Tags: []string{"decided", "auth"}})
	onProject := must(app.RememberInput{Content: "tokens rotate daily", Project: "northwind", Tags: []string{"auth"}})
	elsewhere := must(app.RememberInput{Content: "unrelated", Project: "web"})
	loose := must(app.RememberInput{Content: "no attachment"})

	tests := []struct {
		name    string
		in      app.SearchMemoriesInput
		want    []string
		wantErr error
	}{
		{"everything, newest first", app.SearchMemoriesInput{}, []string{loose, elsewhere, onProject, onTask}, nil},
		{"project includes its tasks' memories", app.SearchMemoriesInput{Project: "Northwind"}, []string{onProject, onTask}, nil},
		{"task only", app.SearchMemoriesInput{Task: "epd-1"}, []string{onTask}, nil},
		{"all tags must match", app.SearchMemoriesInput{Tags: []string{"auth", "Decided"}}, []string{onTask}, nil},
		{"query is a case-insensitive substring", app.SearchMemoriesInput{Query: " CUT-OFF "}, []string{onTask}, nil},
		{"filters combine", app.SearchMemoriesInput{Project: "northwind", Tags: []string{"auth"}, Query: "tokens"}, []string{onProject}, nil},
		{"limit", app.SearchMemoriesInput{Limit: 2}, []string{loose, elsewhere}, nil},
		{"unknown project is empty", app.SearchMemoriesInput{Project: "nope"}, []string{}, nil},
		{"unknown task is empty", app.SearchMemoriesInput{Task: "NOPE-1"}, []string{}, nil},
		{"unknown tag is empty", app.SearchMemoriesInput{Tags: []string{"nope"}}, []string{}, nil},
		{"bad project is invalid", app.SearchMemoriesInput{Project: "a b"}, nil, domain.ErrInvalid},
		{"negative limit is invalid", app.SearchMemoriesInput{Limit: -1}, nil, domain.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := a.SearchMemories(ctx, tt.in)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("want %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.Memories == nil {
				t.Fatal("want an empty slice, not nil")
			}
			var got []string
			for _, m := range out.Memories {
				got = append(got, m.Content)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSearchMemoriesFollowsTaskMoves(t *testing.T) {
	ctx := context.Background()
	a := testutil.NewApp(t)
	if _, err := a.Remember(ctx, app.RememberInput{Content: "x", Task: "EPD-1"}); err != nil {
		t.Fatal(err)
	}
	count := func(project string) int {
		t.Helper()
		out, err := a.SearchMemories(ctx, app.SearchMemoriesInput{Project: project})
		if err != nil {
			t.Fatal(err)
		}
		return len(out.Memories)
	}
	if _, err := a.SetTaskProject(ctx, app.SetTaskProjectInput{Task: "EPD-1", Project: ptr("northwind")}); err != nil {
		t.Fatal(err)
	}
	if count("northwind") != 1 {
		t.Fatal("want the memory under northwind after attaching the task")
	}
	if _, err := a.SetTaskProject(ctx, app.SetTaskProjectInput{Task: "EPD-1", Project: ptr("web")}); err != nil {
		t.Fatal(err)
	}
	if count("northwind") != 0 || count("web") != 1 {
		t.Fatal("want the memory to follow the task to web")
	}
}
