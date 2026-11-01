package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/store"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

func TestOpenIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := testutil.DBPath(t)
	for range 2 {
		s, err := store.Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEnsureProjectIsLazyAndIdempotent(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)

	first, err := s.EnsureProject(ctx, domain.Project{ID: "p1", Slug: "northwind", CreatedAt: testutil.Epoch})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.EnsureProject(ctx, domain.Project{ID: "p2", Slug: "northwind", CreatedAt: testutil.Epoch})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "p1" || second.ID != "p1" {
		t.Fatalf("want both to return p1, got %q and %q", first.ID, second.ID)
	}
	if !second.CreatedAt.Equal(testutil.Epoch) {
		t.Fatalf("created_at round trip: got %v", second.CreatedAt)
	}
}

func TestGetMissingIsNotFound(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)
	if _, err := s.GetProject(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("project: want ErrNotFound, got %v", err)
	}
	if _, err := s.GetTask(ctx, "NOPE-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("task: want ErrNotFound, got %v", err)
	}
}

func TestSetTaskProject(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)
	task, err := s.EnsureTask(ctx, domain.Task{ID: "t1", Key: "EPD-1", CreatedAt: testutil.Epoch})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.EnsureProject(ctx, domain.Project{ID: "p1", Slug: "northwind", CreatedAt: testutil.Epoch})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetTaskProject(ctx, task.ID, &p.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetTask(ctx, "EPD-1"); got.ProjectSlug != "northwind" {
		t.Fatalf("after set: project %q, want northwind", got.ProjectSlug)
	}

	if err := s.SetTaskProject(ctx, task.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetTask(ctx, "EPD-1"); got.ProjectSlug != "" {
		t.Fatalf("after clear: project %q, want none", got.ProjectSlug)
	}

	if err := s.SetTaskProject(ctx, "missing", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing task: want ErrNotFound, got %v", err)
	}
}

func TestWithTxRollsBack(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)
	boom := errors.New("boom")
	err := s.WithTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.EnsureProject(ctx, domain.Project{ID: "p1", Slug: "gone", CreatedAt: testutil.Epoch}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if _, err := s.GetProject(ctx, "gone"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want rolled back, got %v", err)
	}
}

func TestListFiltersAndOrders(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)
	for i, slug := range []string{"web", "northwind", "northwind-iac"} {
		if _, err := s.EnsureProject(ctx, domain.Project{ID: fmt.Sprint(i), Slug: slug, CreatedAt: testutil.Epoch}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListProjects(ctx, "north")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Slug != "northwind" || got[1].Slug != "northwind-iac" {
		t.Fatalf("got %+v", got)
	}
	all, err := s.ListProjects(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3, got %d", len(all))
	}
}

// loci serve handles requests concurrently over one *Store.
func TestConcurrentWritesOnOneStore(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)
	const workers, writes = 8, 25

	var wg sync.WaitGroup
	errs := make(chan error, workers*writes)
	for w := range workers {
		wg.Go(func() {
			for i := range writes {
				id := fmt.Sprintf("w%d-%d", w, i)
				if _, err := s.EnsureTag(ctx, domain.Tag{ID: id, Name: id}); err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	tags, err := s.ListTags(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != workers*writes {
		t.Fatalf("want %d tags, got %d", workers*writes, len(tags))
	}
}

func TestConnectionSettings(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)
	// Force a fresh connection per query, so per-connection settings are
	// checked on connections other than the first.
	s.DB().SetMaxIdleConns(0)
	for range 3 {
		for name, want := range map[string]string{"journal_mode": "wal", "busy_timeout": "5000", "foreign_keys": "1"} {
			got, err := s.Pragma(ctx, name)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("%s = %q, want %q", name, got, want)
			}
		}
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)
	s.DB().SetMaxIdleConns(0)
	task, err := s.EnsureTask(ctx, domain.Task{ID: "t1", Key: "EPD-1", CreatedAt: testutil.Epoch})
	if err != nil {
		t.Fatal(err)
	}
	missing := "no-such-project"
	for range 3 {
		if err := s.SetTaskProject(ctx, task.ID, &missing); err == nil {
			t.Fatal("want a foreign key error for a project that does not exist")
		}
	}
}
