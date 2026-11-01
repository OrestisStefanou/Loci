// Package daemon runs loci serve, the one process that opens the database
// (design.md section 3), and starts it in the background on demand.
//
// Exclusivity comes from a lock file next to the database. Shutdown order is
// stop listening, remove the socket, close the store, release the lock;
// startup is the reverse. That order lets an old server hand over to a new
// one without both ever holding the database.
//
// macOS and Linux only (Unix sockets, flock, setsid).
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/embed"
	"github.com/OrestisStefanou/Loci/internal/rpc"
	"github.com/OrestisStefanou/Loci/internal/store"
)

// ErrAlreadyRunning means another server holds the lock for this database.
var ErrAlreadyRunning = errors.New("loci serve is already running for this database")

// maxSocketPath is the usable length of sun_path on macOS (104 with the NUL).
const maxSocketPath = 103

// Paths are the files belonging to one database.
type Paths struct {
	DB     string
	Socket string
	Lock   string
	Log    string
}

// PathsFor derives the socket, lock and log paths from the database path, so
// each database has its own server.
func PathsFor(db string) (Paths, error) {
	abs, err := filepath.Abs(db)
	if err != nil {
		return Paths{}, err
	}
	p := Paths{
		DB:     abs,
		Socket: abs + ".sock",
		Lock:   abs + ".lock",
		Log:    filepath.Join(logDir(abs), "serve.log"),
	}
	if len(p.Socket) > maxSocketPath {
		return Paths{}, fmt.Errorf("socket path %s is longer than %d bytes; use a shorter database path: %w",
			p.Socket, maxSocketPath, domain.ErrInvalid)
	}
	return p, nil
}

// LogDir is the directory for the logs of the database db: the server's, and
// those of clients that must not report errors to their caller, such as
// loci capture claude-code. It does not depend on the socket path being
// usable, so a client can log that it is not.
func LogDir(db string) (string, error) {
	abs, err := filepath.Abs(db)
	if err != nil {
		return "", err
	}
	return logDir(abs), nil
}

func logDir(absDB string) string { return filepath.Join(filepath.Dir(absDB), "log") }

// Options configure Run.
type Options struct {
	Paths   Paths
	Version string
	Logger  *slog.Logger
	// LockWait is how long to wait for the lock, covering an old server that
	// is still shutting down. Zero fails immediately.
	LockWait time.Duration
	// UserHandle is config user.handle: the person this server records as
	// author. Empty means "me".
	UserHandle string
	// Embedder computes vectors for semantic search. Nil means embedding is
	// off and no worker runs.
	Embedder embed.Embedder
	// MaxVectorDistance is config embedding.max_distance; zero keeps every
	// vector match.
	MaxVectorDistance float64
}

// Run serves until ctx is cancelled or a client asks it to stop.
func Run(ctx context.Context, opts Options) error {
	log := opts.Logger
	p := opts.Paths

	if err := os.MkdirAll(filepath.Dir(p.DB), 0o700); err != nil {
		return err
	}
	unlock, err := lock(ctx, p.Lock, opts.LockWait)
	if err != nil {
		return err
	}
	defer unlock()

	if err := os.Remove(p.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	st, err := store.Open(ctx, p.DB)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ln, err := listen(p.Socket)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(p.Socket) }()

	// Background work stops, and is waited for, before the store closes.
	var workers sync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer func() { cancel(); workers.Wait() }()
	started := time.Now().UTC()
	userHandle := opts.UserHandle
	if userHandle == "" {
		userHandle = "me"
	}
	a := app.New(app.Deps{
		Store: st, Clock: domain.SystemClock{}, IDs: domain.ULIDGen{}, UserHandle: userHandle,
		Embedder: opts.Embedder, MaxVectorDistance: opts.MaxVectorDistance,
	})
	var worker *embedWorker
	if opts.Embedder != nil {
		worker = newEmbedWorker(a, log)
	}
	srv := &http.Server{
		Handler: rpc.NewHandler(a, rpc.Control{
			Version: opts.Version,
			Logger:  log,
			Status: func() rpc.Status {
				return rpc.Status{
					Version: opts.Version, PID: os.Getpid(), DB: p.DB, StartedAt: started,
					Embedding: embeddingStatus(ctx, a, worker),
				}
			},
			Shutdown: cancel,
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	if worker != nil {
		workers.Go(func() { worker.run(ctx) })
	}
	log.Info("loci serve started", "db", p.DB, "version", opts.Version, "pid", os.Getpid())

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		return err
	}
	shutdownCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	err = srv.Shutdown(shutdownCtx)
	log.Info("loci serve stopped")
	return err
}

// embeddingStatus reports the worker's last pass and the live backlog.
func embeddingStatus(ctx context.Context, a *app.App, w *embedWorker) rpc.EmbeddingStatus {
	if w == nil {
		return rpc.EmbeddingStatus{State: embedOff}
	}
	st := rpc.EmbeddingStatus{}
	st.State, st.Error = w.status()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	model, pending, failed, err := a.EmbeddingBacklog(ctx)
	st.Model, st.Pending, st.Failed = model, pending, failed
	if err != nil && st.Error == "" {
		st.Error = err.Error()
	}
	return st
}

// listen creates the socket with mode 0600 from the start. Setting the
// umask, rather than chmod after listening, leaves no window in which another
// user could connect. umask is process-wide, which is safe here because
// nothing else in loci serve creates files while it starts.
func listen(socket string) (net.Listener, error) {
	old := syscall.Umask(0o177)
	ln, err := net.Listen("unix", socket)
	syscall.Umask(old)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socket, err)
	}
	return ln, nil
}

// lock takes an exclusive flock on path, retrying until wait has passed.
func lock(ctx context.Context, path string, wait time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, ErrAlreadyRunning
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// Starter returns a function that launches `exe serve --db <db>` detached
// from the caller, logging to p.Log. Every call launches a server: a
// long-lived caller (the MCP server) must be able to start one again after
// the first stops. Concurrent or redundant starts are harmless, because the
// lock leaves exactly one server running and the others exit.
func Starter(exe string, p Paths) func(context.Context) error {
	return func(context.Context) error { return start(exe, p) }
}

func start(exe string, p Paths) error {
	if err := os.MkdirAll(filepath.Dir(p.Log), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(p.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = logf.Close() }() // the child has its own descriptor

	// Not CommandContext: the server must outlive the command that started it.
	cmd := exec.Command(exe, "--db", p.DB, "serve")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap it if this process lives longer
	return nil
}
