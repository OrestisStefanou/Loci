package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/OrestisStefanou/Loci/internal/domain"
)

// DefaultTimeout bounds one call, including the model load on the first.
// Measured with nomic-embed-text on darwin/arm64: 0.6s to load, 0.6s for a
// batch of 64. Two calls must fit in the rpc client's 30s timeout: `loci
// embed` can wait for the worker's batch to finish, then make its own.
const DefaultTimeout = 10 * time.Second

// OllamaConfig configures NewOllama.
type OllamaConfig struct {
	URL            string // e.g. http://localhost:11434
	Model          string // e.g. nomic-embed-text
	DocumentPrefix string // prepended to Document texts
	QueryPrefix    string // prepended to Query texts
	Timeout        time.Duration
}

// Ollama embeds through a local Ollama server (POST /api/embed).
type Ollama struct {
	cfg  OllamaConfig
	http *http.Client
}

var _ Embedder = (*Ollama)(nil)

// NewOllama returns an Ollama embedder. A zero Timeout means DefaultTimeout.
func NewOllama(cfg OllamaConfig) *Ollama {
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	return &Ollama{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
}

// Model returns the configured model name.
func (o *Ollama) Model() string { return o.cfg.Model }

type ollamaRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
	// Truncate cuts a text past the model's context length instead of
	// failing the batch. Ollama's default, set explicitly: one message per
	// vector, no chunking (design.md section 10).
	Truncate bool `json:"truncate"`
}

type ollamaResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed implements Embedder.
func (o *Ollama) Embed(ctx context.Context, kind Kind, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	prefix := o.cfg.DocumentPrefix
	if kind == Query {
		prefix = o.cfg.QueryPrefix
	}
	input := make([]string, len(texts))
	for i, t := range texts {
		input[i] = prefix + t
	}
	body, err := json.Marshal(ollamaRequest{Model: o.cfg.Model, Input: input, Truncate: true})
	if err != nil {
		return nil, fmt.Errorf("encode embed request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.cfg.URL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.http.Do(req)
	if err != nil {
		if unreachable(err) {
			return nil, fmt.Errorf("ollama at %s: %w: %w", o.cfg.URL, err, domain.ErrUnavailable)
		}
		return nil, fmt.Errorf("ollama at %s: %w", o.cfg.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := strings.TrimSpace(string(excerpt))
		switch resp.StatusCode {
		case http.StatusNotFound:
			return nil, fmt.Errorf("ollama: model %q not found, run `ollama pull %s` (%s)", o.cfg.Model, o.cfg.Model, msg)
		case http.StatusServiceUnavailable: // busy: its request queue is full
			return nil, fmt.Errorf("ollama: %s: %s: %w", resp.Status, msg, domain.ErrUnavailable)
		}
		return nil, fmt.Errorf("ollama: %s: %s", resp.Status, msg)
	}
	var out ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode ollama response: %w", err)
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama returned %d embeddings for %d texts", len(out.Embeddings), len(texts))
	}
	return out.Embeddings, nil
}

// unreachable reports whether err means the server could not be reached or
// did not answer in time, as opposed to answering with an error.
func unreachable(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr) || errors.Is(err, context.DeadlineExceeded)
}
