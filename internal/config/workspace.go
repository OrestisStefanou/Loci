package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/OrestisStefanou/Loci/internal/domain"
)

// WorkspaceFile opts a directory, and everything below it, into Loci and
// names the store its sessions go to (design.md section 11). Without one,
// Claude Code sessions are not captured.
const WorkspaceFile = ".loci.toml"

// Workspace is the nearest WorkspaceFile above a directory.
type Workspace struct {
	// File is the workspace file's path.
	File string `toml:"-"`
	// Store names the database, kept as <store>.db next to storage.path.
	Store string `toml:"store"`
}

// FindWorkspace returns the workspace file closest to dir, looking in dir
// and then each parent. found is false when there is none.
func FindWorkspace(dir string) (ws Workspace, found bool, err error) {
	dir, err = filepath.Abs(dir)
	if err != nil {
		return Workspace{}, false, err
	}
	for {
		path := filepath.Join(dir, WorkspaceFile)
		ws, err := readWorkspace(path)
		if err == nil {
			return ws, true, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return Workspace{}, false, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Workspace{}, false, nil
		}
		dir = parent
	}
}

func readWorkspace(path string) (Workspace, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Workspace{}, err
	}
	var ws Workspace
	if err := toml.Unmarshal(b, &ws); err != nil {
		return Workspace{}, fmt.Errorf("read %s: %w: %w", path, err, domain.ErrInvalid)
	}
	if ws.Store, err = domain.NormalizeStore(ws.Store); err != nil {
		return Workspace{}, fmt.Errorf("%s: store: %w", path, err)
	}
	ws.File = path
	return ws, nil
}

// WriteWorkspace writes a workspace file for store in dir, replacing any
// that is there, and returns it.
func WriteWorkspace(dir, store string) (Workspace, error) {
	store, err := domain.NormalizeStore(store)
	if err != nil {
		return Workspace{}, err
	}
	ws := Workspace{File: filepath.Join(dir, WorkspaceFile), Store: store}
	content := fmt.Sprintf("# Written by loci init: Claude Code sessions in this repository are captured\n# into this store. loci init --remove stops capturing.\nstore = %q\n", store)
	if err := os.WriteFile(ws.File, []byte(content), 0o644); err != nil {
		return Workspace{}, err
	}
	return ws, nil
}

// StoreDB is the database file of store: <store>.db in the directory of
// storage.path, so every store sits next to the default one.
func (c Config) StoreDB(store string) (string, error) {
	store, err := domain.NormalizeStore(store)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(c.Storage.Path), store+".db"), nil
}
