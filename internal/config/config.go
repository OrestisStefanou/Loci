// Package config loads ~/.loci/config.toml (design.md section 11). Add a
// field here only when code reads it.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/OrestisStefanou/Loci/internal/domain"
)

// Config is the subset of config.toml that the code currently uses.
type Config struct {
	User      User      `toml:"user"`
	Storage   Storage   `toml:"storage"`
	Embedding Embedding `toml:"embedding"`
}

// User identifies the person running Loci. The handle is recorded as the
// author of memories saved from the CLI and, later, as the human participant
// in captured conversations.
type User struct {
	Handle string `toml:"handle"`
}

// Storage configures the database.
type Storage struct {
	Path string `toml:"path"`
}

// Embedding configures semantic search (design.md section 10).
type Embedding struct {
	// Provider is "ollama" or "off".
	Provider string `toml:"provider"`
	// URL is the Ollama server.
	URL string `toml:"url"`
	// Model is the embedding model, recorded on every embedded row. Rows from
	// another model count as pending, so a change re-embeds everything once
	// loci serve restarts.
	Model string `toml:"model"`
	// DocumentPrefix and QueryPrefix are prepended to stored texts and to
	// queries. The defaults are what nomic-embed-text expects.
	DocumentPrefix string `toml:"document_prefix"`
	QueryPrefix    string `toml:"query_prefix"`
	// MaxDistance drops semantic matches farther than this cosine distance
	// from the query (0 to 2; 0 keeps all). The scale depends on the model:
	// 0.40 suits nomic-embed-text, where related texts measured 0.29 to 0.38
	// and unrelated ones 0.43 and up.
	MaxDistance float64 `toml:"max_distance"`
}

// Embedding providers.
const (
	ProviderOllama = "ollama"
	ProviderOff    = "off"
)

// DefaultPath is where config.toml lives unless LOCI_CONFIG overrides it.
func DefaultPath() string { return filepath.Join(home(), ".loci", "config.toml") }

// Load reads the config file at path. A missing file is not an error: every
// field has a default.
func Load(path string) (Config, error) {
	cfg := Config{
		User:    User{Handle: defaultHandle()},
		Storage: Storage{Path: filepath.Join(home(), ".loci", "loci.db")},
		Embedding: Embedding{
			Provider:       ProviderOllama,
			URL:            "http://localhost:11434",
			Model:          "nomic-embed-text",
			DocumentPrefix: "search_document: ",
			QueryPrefix:    "search_query: ",
			MaxDistance:    0.40,
		},
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg.Storage.Path = expandHome(cfg.Storage.Path)
	handle, err := domain.NormalizeHandle(cfg.User.Handle)
	if err != nil {
		return Config{}, fmt.Errorf("config %s: user.handle: %w", path, err)
	}
	cfg.User.Handle = handle
	switch cfg.Embedding.Provider {
	case ProviderOllama, ProviderOff:
	default:
		return Config{}, fmt.Errorf("config %s: embedding.provider must be %q or %q, got %q: %w",
			path, ProviderOllama, ProviderOff, cfg.Embedding.Provider, domain.ErrInvalid)
	}
	if d := cfg.Embedding.MaxDistance; d < 0 || d > 2 {
		return Config{}, fmt.Errorf("config %s: embedding.max_distance must be between 0 and 2, got %v: %w",
			path, d, domain.ErrInvalid)
	}
	return cfg, nil
}

// defaultHandle is the OS user name when it is a valid handle, else "me".
func defaultHandle() string {
	if u, err := user.Current(); err == nil {
		if h, err := domain.NormalizeHandle(u.Username); err == nil {
			return h
		}
	}
	return "me"
}

func expandHome(p string) string {
	if p == "~" {
		return home()
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(home(), rest)
	}
	return p
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}
