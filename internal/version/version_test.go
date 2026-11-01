package version_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OrestisStefanou/Loci/internal/version"
)

func TestFromExecutableChangesWhenTheBinaryChanges(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "loci")
	if err := os.WriteFile(exe, []byte("build 1"), 0o700); err != nil {
		t.Fatal(err)
	}
	find := func() (string, error) { return exe, nil }

	first := version.FromExecutable(find)
	if !strings.HasPrefix(first, "dev+") || first != version.FromExecutable(find) {
		t.Fatalf("want a stable dev+ identity, got %q", first)
	}

	// A rebuild writes a new file: new modification time.
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(exe, later, later); err != nil {
		t.Fatal(err)
	}
	if version.FromExecutable(find) == first {
		t.Fatal("identity did not change after the binary changed")
	}
}

func TestFromExecutableFallsBackToDev(t *testing.T) {
	got := version.FromExecutable(func() (string, error) { return "", errors.New("unknown") })
	if got != "dev" {
		t.Fatalf("got %q", got)
	}
}
