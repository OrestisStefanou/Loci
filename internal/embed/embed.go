// Package embed turns texts into vectors for semantic search (design.md
// section 10). Embedder is the seam: Ollama is the real implementation, Fake
// the one tests use. Only this package and cli/serve.go know which is in use.
package embed

import "context"

// Kind says what a text is embedded as. Models like nomic-embed-text apply
// a different prefix to documents and to queries.
type Kind string

const (
	Document Kind = "document"
	Query    Kind = "query"
)

// Embedder turns texts into vectors. Implementations are safe for concurrent use.
type Embedder interface {
	// Embed returns one vector per text, in order. It returns an error
	// wrapping domain.ErrUnavailable when the provider cannot be reached, so
	// callers can degrade.
	Embed(ctx context.Context, kind Kind, texts []string) ([][]float32, error)
	// Model is the identifier recorded on every row this embedder fills.
	Model() string
}
