package embed

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/OrestisStefanou/Loci/internal/domain"
)

// Fake is an Embedder for tests. It returns pinned vectors for known texts
// and a default vector for the rest. It applies no prefixes: it keys on the
// raw text.
type Fake struct {
	mu      sync.Mutex
	model   string
	vectors map[string][]float32
	rejects map[string]error
	def     []float32
	err     error
	hang    bool
	calls   [][]string
}

var _ Embedder = (*Fake)(nil)

// NewFake returns a Fake reporting model as its Model.
func NewFake(model string) *Fake {
	return &Fake{model: model, vectors: map[string][]float32{}, rejects: map[string]error{}, def: []float32{0, 0, 1}}
}

// Set pins the vector returned for text.
func (f *Fake) Set(text string, vec []float32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vectors[text] = vec
}

// Reject makes every later call that includes text fail with err, as a
// provider fails a whole batch over one input it cannot handle. Nil stops
// rejecting it.
func (f *Fake) Reject(text string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.rejects, text)
		return
	}
	f.rejects[text] = err
}

// Fail makes every later call return err. Nil restores normal behaviour.
func (f *Fake) Fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// Hang makes every later call block until its context is done, like a
// provider that accepts the connection and never answers. As with Ollama, an
// expired deadline is reported as domain.ErrUnavailable and a cancellation as
// the context's error. False restores normal behaviour.
func (f *Fake) Hang(hang bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hang = hang
}

// Calls returns the texts of every call so far, one slice per call.
func (f *Fake) Calls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.calls...)
}

// Model implements Embedder.
func (f *Fake) Model() string { return f.model }

// Embed implements Embedder.
func (f *Fake) Embed(ctx context.Context, _ Kind, texts []string) ([][]float32, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), texts...))
	hang := f.hang
	f.mu.Unlock()
	if hang {
		<-ctx.Done()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: %w", ctx.Err(), domain.ErrUnavailable)
		}
		return nil, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	for _, t := range texts {
		if err := f.rejects[t]; err != nil {
			return nil, err
		}
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if v, ok := f.vectors[t]; ok {
			out[i] = v
		} else {
			out[i] = f.def
		}
	}
	return out, nil
}
