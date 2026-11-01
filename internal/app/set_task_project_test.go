package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

func ptr(s string) *string { return &s }

func TestSetTaskProject(t *testing.T) {
	tests := []struct {
		name        string
		steps       []app.SetTaskProjectInput // applied in order; the last one is checked
		wantKey     string
		wantProject string
		wantErr     error
	}{
		{
			name:        "creates task and project lazily, normalising both",
			steps:       []app.SetTaskProjectInput{{Task: " epd-1271 ", Project: ptr("Northwind")}},
			wantKey:     "EPD-1271",
			wantProject: "northwind",
		},
		{
			name: "moves a task to another project",
			steps: []app.SetTaskProjectInput{
				{Task: "EPD-1", Project: ptr("northwind")},
				{Task: "EPD-1", Project: ptr("settlements")},
			},
			wantKey:     "EPD-1",
			wantProject: "settlements",
		},
		{
			name: "nil project removes it",
			steps: []app.SetTaskProjectInput{
				{Task: "EPD-1", Project: ptr("northwind")},
				{Task: "EPD-1"},
			},
			wantKey: "EPD-1",
		},
		{
			name:    "empty task is invalid",
			steps:   []app.SetTaskProjectInput{{Task: "  ", Project: ptr("northwind")}},
			wantErr: domain.ErrInvalid,
		},
		{
			name:    "bad project slug is invalid",
			steps:   []app.SetTaskProjectInput{{Task: "EPD-1", Project: ptr("a b")}},
			wantErr: domain.ErrInvalid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			a := testutil.NewApp(t)
			var out app.SetTaskProjectOutput
			var err error
			for _, in := range tt.steps {
				out, err = a.SetTaskProject(ctx, in)
			}
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("want %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.Task.Key != tt.wantKey || out.Task.ProjectSlug != tt.wantProject {
				t.Fatalf("got task %q project %q, want %q project %q",
					out.Task.Key, out.Task.ProjectSlug, tt.wantKey, tt.wantProject)
			}
		})
	}
}

func TestSetTaskProjectDoesNotDuplicate(t *testing.T) {
	ctx := context.Background()
	a := testutil.NewApp(t)
	for _, key := range []string{"epd-1", "EPD-1"} {
		if _, err := a.SetTaskProject(ctx, app.SetTaskProjectInput{Task: key, Project: ptr("Northwind")}); err != nil {
			t.Fatal(err)
		}
	}
	for kind, want := range map[domain.Kind]int{domain.KindTask: 1, domain.KindProject: 1} {
		out, err := a.List(ctx, app.ListInput{Kind: kind})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Items) != want {
			t.Fatalf("%s: want %d, got %+v", kind, want, out.Items)
		}
	}
}
