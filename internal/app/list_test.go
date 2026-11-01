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

func TestList(t *testing.T) {
	ctx := context.Background()
	a := testutil.NewApp(t)
	for _, in := range []app.SetTaskProjectInput{
		{Task: "EPD-2", Project: ptr("northwind")},
		{Task: "EPD-10", Project: ptr("settlements")},
		{Task: "OPS-1"},
	} {
		if _, err := a.SetTaskProject(ctx, in); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name    string
		in      app.ListInput
		want    []app.ListItem // IDs ignored
		wantErr error
	}{
		{
			name: "tasks used at the same time are ordered by key, with projects",
			in:   app.ListInput{Kind: domain.KindTask},
			want: []app.ListItem{{Name: "EPD-10", Project: "settlements"}, {Name: "EPD-2", Project: "northwind"}, {Name: "OPS-1"}},
		},
		{
			name: "task query is case-insensitive",
			in:   app.ListInput{Kind: domain.KindTask, Query: " epd "},
			want: []app.ListItem{{Name: "EPD-10", Project: "settlements"}, {Name: "EPD-2", Project: "northwind"}},
		},
		{
			name: "projects by substring",
			in:   app.ListInput{Kind: domain.KindProject, Query: "SET"},
			want: []app.ListItem{{Name: "settlements"}},
		},
		{
			name: "no tags yet is an empty list",
			in:   app.ListInput{Kind: domain.KindTag},
			want: []app.ListItem{},
		},
		{
			name:    "unknown kind is invalid",
			in:      app.ListInput{Kind: "repos"},
			wantErr: domain.ErrInvalid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := a.List(ctx, tt.in)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("want %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.Items == nil {
				t.Fatal("Items must be non-nil so --json prints []")
			}
			if len(out.Items) != len(tt.want) {
				t.Fatalf("got %+v, want %+v", out.Items, tt.want)
			}
			for i, item := range out.Items {
				if item.Name != tt.want[i].Name || item.Project != tt.want[i].Project {
					t.Fatalf("item %d: got %+v, want %+v", i, item, tt.want[i])
				}
			}
		})
	}
}

func TestListTasksByLastUse(t *testing.T) {
	ctx := context.Background()
	a := testutil.NewAppWithClock(t, &testutil.TickClock{})
	for _, task := range []string{"EPD-1", "EPD-2", "EPD-3", "EPD-4"} {
		if _, err := a.SetTaskProject(ctx, app.SetTaskProjectInput{Task: task, Project: ptr("fees-v2")}); err != nil {
			t.Fatal(err)
		}
	}
	names := func() []string {
		t.Helper()
		out, err := a.List(ctx, app.ListInput{Kind: domain.KindTask})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, it := range out.Items {
			got = append(got, it.Name)
		}
		return got
	}

	steps := []struct {
		name string
		use  func()
		want []string
	}{
		{name: "newest task first", use: func() {}, want: []string{"EPD-4", "EPD-3", "EPD-2", "EPD-1"}},
		{name: "a conversation starting on a task moves it up", use: func() {
			captured(t, a, app.CaptureStartInput{CaptureKey: key("s1", ""), Task: "EPD-1"})
		}, want: []string{"EPD-1", "EPD-4", "EPD-3", "EPD-2"}},
		{name: "a memory saved on a task moves it up", use: func() {
			if _, err := a.Remember(ctx, app.RememberInput{Content: "The cut-off is 17:00 CET.", Task: "EPD-2"}); err != nil {
				t.Fatal(err)
			}
		}, want: []string{"EPD-2", "EPD-1", "EPD-4", "EPD-3"}},
		{name: "a message in a conversation on a task moves it up", use: func() {
			captured(t, a, app.CaptureStartInput{CaptureKey: key("s1", "")}, "Where is the cut-off?")
		}, want: []string{"EPD-1", "EPD-2", "EPD-4", "EPD-3"}},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			s.use()
			if got := names(); !slices.Equal(got, s.want) {
				t.Fatalf("got %v, want %v", got, s.want)
			}
		})
	}
}
