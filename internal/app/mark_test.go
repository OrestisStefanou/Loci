package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

func TestMark(t *testing.T) {
	// Each case starts with one active memory; an empty ID in a step means
	// that memory. The last step is checked.
	tests := []struct {
		name    string
		steps   []app.MarkInput
		want    domain.MemoryStatus
		wantErr error
	}{
		{name: "marks inactive", steps: []app.MarkInput{{Status: "inactive"}}, want: domain.MemoryInactive},
		{name: "normalises the status", steps: []app.MarkInput{{Status: " Inactive "}}, want: domain.MemoryInactive},
		{
			name:  "restores to active",
			steps: []app.MarkInput{{Status: "inactive"}, {Status: "active"}},
			want:  domain.MemoryActive,
		},
		{
			name:  "marking twice is harmless",
			steps: []app.MarkInput{{Status: "inactive"}, {Status: "inactive"}},
			want:  domain.MemoryInactive,
		},
		{name: "missing status is invalid", steps: []app.MarkInput{{}}, wantErr: domain.ErrInvalid},
		{name: "unknown status is invalid", steps: []app.MarkInput{{Status: "wrong"}}, wantErr: domain.ErrInvalid},
		{name: "empty id is invalid", steps: []app.MarkInput{{ID: " ", Status: "inactive"}}, wantErr: domain.ErrInvalid},
		{name: "unknown id is not found", steps: []app.MarkInput{{ID: "nope", Status: "inactive"}}, wantErr: domain.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			a := testutil.NewApp(t)
			mem, err := a.Remember(ctx, app.RememberInput{Content: "x"})
			if err != nil {
				t.Fatal(err)
			}
			var out app.MarkOutput
			for _, in := range tt.steps {
				if in.ID == "" {
					in.ID = " " + mem.Memory.ID + " "
				}
				if out, err = a.Mark(ctx, in); err != nil {
					break
				}
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
			if out.Memory.ID != mem.Memory.ID || out.Memory.Status != tt.want {
				t.Fatalf("got %s %s, want %s %s", out.Memory.ID, out.Memory.Status, mem.Memory.ID, tt.want)
			}
		})
	}
}

func TestMarkedMemoriesLeaveSearch(t *testing.T) {
	ctx := context.Background()
	a := testutil.NewApp(t)
	out, err := a.Remember(ctx, app.RememberInput{Content: "x", Task: "EPD-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Mark(ctx, app.MarkInput{ID: out.Memory.ID, Status: domain.MemoryInactive}); err != nil {
		t.Fatal(err)
	}
	count := func(includeInactive bool) int {
		res, err := a.SearchMemories(ctx, app.SearchMemoriesInput{Task: "EPD-1", IncludeInactive: includeInactive})
		if err != nil {
			t.Fatal(err)
		}
		return len(res.Memories)
	}
	if n := count(false); n != 0 {
		t.Fatalf("active search: got %d, want 0", n)
	}
	if n := count(true); n != 1 {
		t.Fatalf("include_inactive: got %d, want 1", n)
	}
	if _, err := a.Mark(ctx, app.MarkInput{ID: out.Memory.ID, Status: domain.MemoryActive}); err != nil {
		t.Fatal(err)
	}
	if n := count(false); n != 1 {
		t.Fatalf("after restore: got %d, want 1", n)
	}
}
