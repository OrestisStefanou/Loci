package main_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/rpc"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

// TestServerLifecycle runs real loci binaries as separate processes: the CLI
// starts loci serve on demand, a second server is refused, a rebuilt binary
// replaces the running server, and stop stops it.
func TestServerLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries")
	}
	dir := testutil.ShortDir(t)
	// The binaries and the servers they start read this config, not the
	// user's, and never contact an embedding provider.
	cfg := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg, []byte("[embedding]\nprovider = \"off\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCI_CONFIG", cfg)
	v1, v2 := build(t, dir, "e2e-v1"), build(t, dir, "e2e-v2")
	db := filepath.Join(dir, "loci.db")
	t.Cleanup(func() { _ = exec.Command(v2, "--db", db, "stop").Run() })

	if out := loci(t, v1, db, 0, "task", "epd-1", "--project", "northwind"); !strings.Contains(out, "EPD-1") {
		t.Fatalf("task: %q", out)
	}
	first := status(t, v1, db)
	if first.Version != "e2e-v1" {
		t.Fatalf("status: %+v", first)
	}

	// The lock is held by another process, so a second server exits cleanly.
	if out := loci(t, v1, db, 0, "serve"); !strings.Contains(out, "already running") {
		t.Fatalf("second serve: %q", out)
	}

	// A different build replaces the server and sees the same data.
	if out := loci(t, v2, db, 0, "ls", "tasks"); !strings.Contains(out, "EPD-1") || !strings.Contains(out, "northwind") {
		t.Fatalf("ls after upgrade: %q", out)
	}
	second := status(t, v2, db)
	if second.Version != "e2e-v2" || second.PID == first.PID {
		t.Fatalf("want a new e2e-v2 server, got %+v (old pid %d)", second, first.PID)
	}

	if out := loci(t, v2, db, 0, "stop"); !strings.Contains(out, "stopped") {
		t.Fatalf("stop: %q", out)
	}
	if out := loci(t, v2, db, 1, "status"); !strings.Contains(out, "not running") {
		t.Fatalf("status after stop: %q", out)
	}
}

func build(t *testing.T, dir, version string) string {
	t.Helper()
	bin := filepath.Join(dir, "loci-"+version)
	cmd := exec.Command("go", "build", "-o", bin,
		"-ldflags", "-X github.com/OrestisStefanou/Loci/internal/version.Version="+version, ".")
	cmd.Env = append(cmd.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// loci runs bin and returns combined output, failing unless it exits with want.
func loci(t *testing.T, bin, db string, want int, args ...string) string {
	t.Helper()
	out, err := exec.Command(bin, append([]string{"--db", db}, args...)...).CombinedOutput()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if code != want {
		t.Fatalf("loci %v: exit %d, want %d\n%s", args, code, want, out)
	}
	return string(out)
}

func status(t *testing.T, bin, db string) rpc.Status {
	t.Helper()
	var st rpc.Status
	if err := json.Unmarshal([]byte(loci(t, bin, db, 0, "--json", "status")), &st); err != nil {
		t.Fatal(err)
	}
	return st
}
