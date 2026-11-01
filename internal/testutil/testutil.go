// Package testutil builds real, isolated dependencies for tests: a fresh
// database file per test, a fixed clock and deterministic IDs. Tests use the
// real store, never a mock (CLAUDE.md, Testing).
package testutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/embed"
	"github.com/OrestisStefanou/Loci/internal/store"
)

// UserHandle is the configured person in NewApp.
const UserHandle = "tester"

// Epoch is the fixed time returned by Clock.
var Epoch = time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

// Clock always returns Epoch.
type Clock struct{}

func (Clock) Now() time.Time { return Epoch }

// TickClock returns Epoch, then a minute later on each call, so tests can
// order things by time.
type TickClock struct {
	mu sync.Mutex
	n  int
}

func (c *TickClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return Epoch.Add(time.Duration(c.n-1) * time.Minute)
}

// SeqIDs returns "id-0001", "id-0002", ... in order.
type SeqIDs struct {
	mu sync.Mutex
	n  int
}

func (s *SeqIDs) New(time.Time) domain.ID {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return fmt.Sprintf("id-%04d", s.n)
}

// DBPath returns a database path inside a per-test temporary directory.
func DBPath(t testing.TB) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "loci.db")
}

// ShortDir returns a temporary directory under /tmp, removed when the test
// ends. Use it for Unix sockets: t.TempDir() paths on macOS can exceed the
// 103-byte socket path limit.
func ShortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "loci-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// NewStore opens a fresh store and closes it when the test or benchmark ends.
func NewStore(t testing.TB) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), DBPath(t))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// NewApp returns an App over a fresh store with deterministic clock and IDs,
// with embedding off.
func NewApp(t *testing.T) *app.App {
	t.Helper()
	return NewAppWithEmbedder(t, nil)
}

// NewAppWithEmbedder is NewApp with embedding on, through e (usually an
// embed.Fake).
func NewAppWithEmbedder(t *testing.T, e embed.Embedder) *app.App {
	t.Helper()
	return app.New(app.Deps{Store: NewStore(t), Clock: Clock{}, IDs: &SeqIDs{}, UserHandle: UserHandle, Embedder: e})
}

// NewAppWithClock is NewApp with its time from c (usually a TickClock).
func NewAppWithClock(t *testing.T, c domain.Clock) *app.App {
	t.Helper()
	return app.New(app.Deps{Store: NewStore(t), Clock: c, IDs: &SeqIDs{}, UserHandle: UserHandle})
}
