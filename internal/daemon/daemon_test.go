package daemon_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/daemon"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/embed"
	"github.com/OrestisStefanou/Loci/internal/rpc"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

func paths(t *testing.T) daemon.Paths {
	t.Helper()
	p, err := daemon.PathsFor(filepath.Join(testutil.ShortDir(t), "loci.db"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// server runs daemon.Run in a goroutine. The returned channel yields Run's
// result; the server is stopped when the test ends.
func server(t *testing.T, p daemon.Paths, version string) <-chan error {
	t.Helper()
	return serverWith(t, daemon.Options{Paths: p, Version: version})
}

// serverWith is server with more options. Logger and LockWait are filled in.
func serverWith(t *testing.T, opts daemon.Options) <-chan error {
	t.Helper()
	opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	opts.LockWait = 2 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- daemon.Run(ctx, opts)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	return done
}

// starter starts an in-process server the way daemon.Starter starts a process.
func starter(t *testing.T, p daemon.Paths, version string) (func(context.Context) error, *int) {
	var mu sync.Mutex
	starts := 0
	return func(context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		starts++
		server(t, p, version)
		return nil
	}, &starts
}

func TestClientStartsServerOnDemand(t *testing.T) {
	ctx := context.Background()
	p := paths(t)
	start, starts := starter(t, p, "v1")
	c := rpc.NewClient(p.Socket, "v1", start)

	out, err := c.SetTaskProject(ctx, app.SetTaskProjectInput{Task: "epd-1", Project: ptr("Northwind")})
	if err != nil {
		t.Fatal(err)
	}
	if out.Task.Key != "EPD-1" || out.Task.ProjectSlug != "northwind" {
		t.Fatalf("got %+v", out.Task)
	}
	list, err := c.List(ctx, app.ListInput{Kind: domain.KindTask})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].Name != "EPD-1" {
		t.Fatalf("got %+v", list.Items)
	}
	if *starts != 1 {
		t.Fatalf("want 1 start, got %d", *starts)
	}
}

func TestErrorsKeepTheirKind(t *testing.T) {
	ctx := context.Background()
	p := paths(t)
	server(t, p, "v1")
	c := rpc.NewClient(p.Socket, "v1", nil)
	waitUp(t, c)

	_, err := c.SetTaskProject(ctx, app.SetTaskProjectInput{Task: " "})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	if !strings.Contains(err.Error(), "task must not be empty") {
		t.Fatalf("message lost: %v", err)
	}
}

func TestNewVersionReplacesRunningServer(t *testing.T) {
	ctx := context.Background()
	p := paths(t)
	old := server(t, p, "v1")
	waitUp(t, rpc.NewClient(p.Socket, "v1", nil))

	start, starts := starter(t, p, "v2")
	c := rpc.NewClient(p.Socket, "v2", start)
	if _, err := c.List(ctx, app.ListInput{Kind: domain.KindProject}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-old:
		if err != nil {
			t.Fatalf("old server: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old server did not stop")
	}
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != "v2" || *starts != 1 {
		t.Fatalf("want v2 started once, got %s started %d times", st.Version, *starts)
	}
}

func TestSecondServerIsRejected(t *testing.T) {
	p := paths(t)
	server(t, p, "v1")
	waitUp(t, rpc.NewClient(p.Socket, "v1", nil))

	err := daemon.Run(context.Background(), daemon.Options{
		Paths: p, Version: "v1", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if !errors.Is(err, daemon.ErrAlreadyRunning) {
		t.Fatalf("want ErrAlreadyRunning, got %v", err)
	}
}

func TestStaleSocketIsReplaced(t *testing.T) {
	p := paths(t)
	if err := os.WriteFile(p.Socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	server(t, p, "v1")
	waitUp(t, rpc.NewClient(p.Socket, "v1", nil))
}

func TestStop(t *testing.T) {
	ctx := context.Background()
	p := paths(t)
	done := server(t, p, "v1")
	c := rpc.NewClient(p.Socket, "v1", nil)
	waitUp(t, c)

	if err := c.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(p.Socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket not removed: %v", err)
	}
	if err := c.Stop(ctx); !errors.Is(err, rpc.ErrServerNotRunning) {
		t.Fatalf("second stop: want ErrServerNotRunning, got %v", err)
	}
	if _, err := c.List(ctx, app.ListInput{Kind: domain.KindTag}); !errors.Is(err, rpc.ErrServerNotRunning) {
		t.Fatalf("client without starter: want ErrServerNotRunning, got %v", err)
	}
}

// Hooks from several sessions call the server at once.
func TestConcurrentClients(t *testing.T) {
	ctx := context.Background()
	p := paths(t)
	server(t, p, "v1")
	waitUp(t, rpc.NewClient(p.Socket, "v1", nil))

	const clients, calls = 8, 10
	var wg sync.WaitGroup
	errs := make(chan error, clients*calls)
	for i := range clients {
		wg.Go(func() {
			c := rpc.NewClient(p.Socket, "v1", nil)
			for j := range calls {
				task := fmt.Sprintf("EPD-%d", i*calls+j)
				if _, err := c.SetTaskProject(ctx, app.SetTaskProjectInput{Task: task, Project: ptr("northwind")}); err != nil {
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
	list, err := rpc.NewClient(p.Socket, "v1", nil).List(ctx, app.ListInput{Kind: domain.KindTask})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != clients*calls {
		t.Fatalf("want %d tasks, got %d", clients*calls, len(list.Items))
	}
}

func TestPathsForRejectsLongSocketPath(t *testing.T) {
	_, err := daemon.PathsFor("/" + strings.Repeat("x", 120) + "/loci.db")
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func waitUp(t *testing.T, c *rpc.Client) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := c.Status(context.Background()); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not come up")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func ptr(s string) *string { return &s }

func TestSocketIsPrivateWhateverTheUmask(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)

	p := paths(t)
	server(t, p, "v1")
	waitUp(t, rpc.NewClient(p.Socket, "v1", nil))
	info, err := os.Stat(p.Socket)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("socket mode %o, want 600", mode)
	}
}

// A long-lived client must be able to start a server more than once, e.g.
// after the first one was stopped or replaced.
func TestStarterLaunchesOnEveryCall(t *testing.T) {
	dir := testutil.ShortDir(t)
	calls := filepath.Join(dir, "calls")
	exe := filepath.Join(dir, "fake-loci")
	script := "#!/bin/sh\necho \"$@\" >> " + calls + "\n"
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	p := paths(t)
	start := daemon.Starter(exe, p)
	for range 2 {
		if err := start(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(calls)
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		if len(lines) == 2 && lines[0] == "--db "+p.DB+" serve" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("want 2 launches of `--db %s serve`, got %q", p.DB, b)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitForEmbedding polls the server's status until ok returns true.
func waitForEmbedding(t *testing.T, c *rpc.Client, ok func(rpc.EmbeddingStatus) bool) rpc.EmbeddingStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, err := c.Status(context.Background())
		if err == nil && ok(st.Embedding) {
			return st.Embedding
		}
		if time.Now().After(deadline) {
			t.Fatalf("embedding status never matched: last %+v, %v", st.Embedding, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestEmbeddingWorker(t *testing.T) {
	ctx := context.Background()

	t.Run("embeds new memories in the background", func(t *testing.T) {
		daemon.FastEmbedWorker(t)
		p := paths(t)
		fake := embed.NewFake("fake-model")
		serverWith(t, daemon.Options{Paths: p, Version: "v1", Embedder: fake})
		c := rpc.NewClient(p.Socket, "v1", nil)
		if st := waitForEmbedding(t, c, func(rpc.EmbeddingStatus) bool { return true }); st.State != "idle" {
			t.Fatalf("before any memory: %+v", st)
		}
		if _, err := c.Remember(ctx, app.RememberInput{Content: "embed me"}); err != nil {
			t.Fatal(err)
		}
		st := waitForEmbedding(t, c, func(s rpc.EmbeddingStatus) bool { return s.State == "ok" && s.Pending == 0 && len(fake.Calls()) > 0 })
		if st.Model != "fake-model" {
			t.Fatalf("status %+v", st)
		}
		if calls := fake.Calls(); calls[len(calls)-1][0] != "embed me" {
			t.Fatalf("calls %v", calls)
		}
	})

	t.Run("an unreachable provider never blocks remember or search", func(t *testing.T) {
		daemon.FastEmbedWorker(t)
		p := paths(t)
		fake := embed.NewFake("fake-model")
		fake.Fail(fmt.Errorf("refused: %w", domain.ErrUnavailable))
		serverWith(t, daemon.Options{Paths: p, Version: "v1", Embedder: fake})
		c := rpc.NewClient(p.Socket, "v1", nil)
		waitForEmbedding(t, c, func(rpc.EmbeddingStatus) bool { return true })
		if _, err := c.Remember(ctx, app.RememberInput{Content: "saved while down"}); err != nil {
			t.Fatal(err)
		}
		st := waitForEmbedding(t, c, func(s rpc.EmbeddingStatus) bool { return s.State == "unavailable" })
		if st.Pending != 1 {
			t.Fatalf("status %+v", st)
		}
		out, err := c.SearchMemories(ctx, app.SearchMemoriesInput{Query: "down"})
		if err != nil {
			t.Fatal(err)
		}
		if out.Ranking != app.RankingText || !out.SemanticUnavailable || len(out.Memories) != 1 {
			t.Fatalf("search %+v", out)
		}

		fake.Fail(nil)
		waitForEmbedding(t, c, func(s rpc.EmbeddingStatus) bool { return s.State == "ok" && s.Pending == 0 })
	})

	t.Run("a row the provider rejects is set aside and the queue moves on", func(t *testing.T) {
		daemon.FastEmbedWorker(t)
		p := paths(t)
		fake := embed.NewFake("fake-model")
		fake.Reject("poison", errors.New("ollama: 500 Internal Server Error"))
		serverWith(t, daemon.Options{Paths: p, Version: "v1", Embedder: fake})
		c := rpc.NewClient(p.Socket, "v1", nil)
		waitForEmbedding(t, c, func(rpc.EmbeddingStatus) bool { return true })
		for _, content := range []string{"poison", "fine"} {
			if _, err := c.Remember(ctx, app.RememberInput{Content: content}); err != nil {
				t.Fatal(err)
			}
		}
		waitForEmbedding(t, c, func(s rpc.EmbeddingStatus) bool { return s.State == "ok" && s.Pending == 0 && s.Failed == 1 })
		if _, err := c.Remember(ctx, app.RememberInput{Content: "after"}); err != nil {
			t.Fatal(err)
		}
		waitForEmbedding(t, c, func(s rpc.EmbeddingStatus) bool { return s.State == "ok" && s.Pending == 0 && s.Failed == 1 })
	})

	t.Run("no embedder reports off", func(t *testing.T) {
		p := paths(t)
		server(t, p, "v1")
		st := waitForEmbedding(t, rpc.NewClient(p.Socket, "v1", nil), func(rpc.EmbeddingStatus) bool { return true })
		if st.State != "off" {
			t.Fatalf("status %+v", st)
		}
	})
}
