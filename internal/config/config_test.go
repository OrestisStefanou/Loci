package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/config"
	"github.com/OrestisStefanou/Loci/internal/domain"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file uses defaults", func(t *testing.T) {
		cfg, err := config.Load(filepath.Join(dir, "absent.toml"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(cfg.Storage.Path, filepath.Join(".loci", "loci.db")) {
			t.Fatalf("default path %q", cfg.Storage.Path)
		}
		if cfg.User.Handle == "" {
			t.Fatal("want a default user handle")
		}
	})

	t.Run("reads and normalises user.handle", func(t *testing.T) {
		path := filepath.Join(dir, "user.toml")
		if err := os.WriteFile(path, []byte("[user]\nhandle = \" Ada \"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.User.Handle != "ada" {
			t.Fatalf("got %q", cfg.User.Handle)
		}
	})

	t.Run("invalid user.handle is an error", func(t *testing.T) {
		path := filepath.Join(dir, "badhandle.toml")
		if err := os.WriteFile(path, []byte("[user]\nhandle = \"a b\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load(path); err == nil {
			t.Fatal("want error")
		}
	})

	t.Run("expands ~ in storage.path", func(t *testing.T) {
		path := filepath.Join(dir, "config.toml")
		if err := os.WriteFile(path, []byte("[storage]\npath = \"~/x/loci.db\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		home, _ := os.UserHomeDir()
		if cfg.Storage.Path != filepath.Join(home, "x", "loci.db") {
			t.Fatalf("got %q", cfg.Storage.Path)
		}
	})

	t.Run("embedding defaults to ollama with nomic-embed-text", func(t *testing.T) {
		cfg, err := config.Load(filepath.Join(dir, "absent.toml"))
		if err != nil {
			t.Fatal(err)
		}
		want := config.Embedding{
			Provider: "ollama", URL: "http://localhost:11434", Model: "nomic-embed-text",
			DocumentPrefix: "search_document: ", QueryPrefix: "search_query: ", MaxDistance: 0.40,
		}
		if cfg.Embedding != want {
			t.Fatalf("got %+v", cfg.Embedding)
		}
	})

	t.Run("embedding can be turned off, keeping the other defaults", func(t *testing.T) {
		path := filepath.Join(dir, "off.toml")
		if err := os.WriteFile(path, []byte("[embedding]\nprovider = \"off\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Embedding.Provider != "off" || cfg.Embedding.Model != "nomic-embed-text" {
			t.Fatalf("got %+v", cfg.Embedding)
		}
	})

	t.Run("unknown embedding provider is invalid", func(t *testing.T) {
		path := filepath.Join(dir, "badprovider.toml")
		if err := os.WriteFile(path, []byte("[embedding]\nprovider = \"openai\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load(path); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("want ErrInvalid, got %v", err)
		}
	})

	t.Run("max_distance outside 0 to 2 is invalid", func(t *testing.T) {
		for _, v := range []string{"-0.1", "2.5"} {
			path := filepath.Join(dir, "maxdist.toml")
			if err := os.WriteFile(path, []byte("[embedding]\nmax_distance = "+v+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := config.Load(path); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("%s: want ErrInvalid, got %v", v, err)
			}
		}
	})

	t.Run("malformed file is an error", func(t *testing.T) {
		path := filepath.Join(dir, "bad.toml")
		if err := os.WriteFile(path, []byte("[storage\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load(path); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestFindWorkspace(t *testing.T) {
	root := t.TempDir()
	write := func(dir, content string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, config.WorkspaceFile), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	company := filepath.Join(root, "northwind")
	write(company, "store = \" Northwind \"\n")
	scratch := filepath.Join(company, "scratch")
	write(scratch, "store = \"scratch\"\n")
	deep := filepath.Join(company, "web", "src", "app")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, dir string
		want      string // store, "" for none
	}{
		{"in the directory itself", company, "northwind"},
		{"from deep below", deep, "northwind"},
		{"the nearest file wins", filepath.Join(scratch, "x"), "scratch"},
		{"none above", root, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.MkdirAll(tc.dir, 0o755); err != nil {
				t.Fatal(err)
			}
			ws, found, err := config.FindWorkspace(tc.dir)
			if err != nil {
				t.Fatal(err)
			}
			if found != (tc.want != "") || ws.Store != tc.want {
				t.Fatalf("got %+v found=%v, want store %q", ws, found, tc.want)
			}
		})
	}

	t.Run("an invalid file is an error", func(t *testing.T) {
		for _, content := range []string{"store = \"two words\"\n", "store = \n", ""} {
			dir := t.TempDir()
			write(dir, content)
			if _, _, err := config.FindWorkspace(dir); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("%q: got %v, want invalid", content, err)
			}
		}
	})

	t.Run("a written workspace is found", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := config.WriteWorkspace(dir, "Personal"); err != nil {
			t.Fatal(err)
		}
		ws, found, err := config.FindWorkspace(dir)
		if err != nil || !found || ws.Store != "personal" || ws.File != filepath.Join(dir, config.WorkspaceFile) {
			t.Fatalf("got %+v found=%v err=%v", ws, found, err)
		}
	})
}

func TestStoreDB(t *testing.T) {
	cfg := config.Config{Storage: config.Storage{Path: "/data/loci/loci.db"}}
	got, err := cfg.StoreDB("Northwind")
	if err != nil || got != "/data/loci/northwind.db" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := cfg.StoreDB("../etc"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a path as a store name: got %v", err)
	}
}
