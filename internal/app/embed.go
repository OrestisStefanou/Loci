package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/embed"
	"github.com/OrestisStefanou/Loci/internal/store"
)

// Embed batch bounds.
const (
	defaultEmbedLimit = 64
	maxEmbedLimit     = 256
)

// EmbedInput is the input of Embed (CLI: loci embed; also run by the
// embedding worker in loci serve). Not exposed over MCP.
type EmbedInput struct {
	// Backfill clears every stored embedding first, so everything is embedded again.
	Backfill bool `json:"backfill,omitempty" jsonschema:"clear every stored embedding first so everything is embedded again"`
	// Limit is the batch size; 0 means 64, at most 256.
	Limit int `json:"limit,omitempty" jsonschema:"batch size, default 64, at most 256"`
}

// EmbedOutput is the output of Embed.
type EmbedOutput struct {
	Model    string `json:"model"`
	Embedded int    `json:"embedded"` // rows embedded by this call
	Pending  int    `json:"pending"`  // rows still waiting after this call
	Enabled  bool   `json:"enabled"`  // false when embedding is off
	// Failed are the rows this call set aside because the provider rejected
	// them while embedding others (design.md section 10).
	Failed []domain.ID `json:"failed,omitempty"`
}

// embedOrder is the order Embed fills a batch from: memories first, so a
// backlog of captured messages never delays a memory just saved.
var embedOrder = []store.Embeddable{store.EmbedMemories, store.EmbedMessages}

// Embed embeds one batch of memories and messages that have no embedding
// from the current model: pending memories first, then messages up to the
// batch size. When the provider cannot be reached it returns an error
// wrapping domain.ErrUnavailable and the rows stay pending.
//
// When the provider rejects the batch otherwise, its rows are tried one at a
// time. Those it still rejects are set aside as failed under the model, so
// they no longer block the rows behind them, but only when at least one
// other row was embedded: if every row fails, the cause is the provider or
// its configuration, not the rows, and the error is returned.
func (a *App) Embed(ctx context.Context, in EmbedInput) (EmbedOutput, error) {
	limit := in.Limit
	switch {
	case limit < 0:
		return EmbedOutput{}, fmt.Errorf("limit must not be negative: %w", domain.ErrInvalid)
	case limit == 0:
		limit = defaultEmbedLimit
	case limit > maxEmbedLimit:
		limit = maxEmbedLimit
	}
	if a.embedder == nil {
		return EmbedOutput{}, nil
	}
	a.embedMu.Lock()
	defer a.embedMu.Unlock()

	model := a.embedder.Model()
	out := EmbedOutput{Model: model, Enabled: true}
	if in.Backfill {
		for _, t := range embedOrder {
			if _, err := a.store.ClearEmbeddings(ctx, t); err != nil {
				return EmbedOutput{}, err
			}
		}
	}
	var rows []pendingRow
	for _, t := range embedOrder {
		if len(rows) == limit {
			break
		}
		pending, err := a.store.PendingEmbeddings(ctx, t, model, limit-len(rows))
		if err != nil {
			return EmbedOutput{}, err
		}
		for _, r := range pending {
			rows = append(rows, pendingRow{table: t, id: r.ID, text: strings.TrimSpace(r.Content)})
		}
	}
	if len(rows) > 0 {
		vecs, err := a.embedRows(ctx, rows)
		if err != nil {
			return EmbedOutput{}, err
		}
		if err := a.store.WithTx(ctx, func(tx *store.Tx) error {
			for i, r := range rows {
				if vecs[i] == nil {
					if err := tx.MarkEmbeddingFailed(ctx, r.table, r.id, model); err != nil {
						return err
					}
					out.Failed = append(out.Failed, r.id)
					continue
				}
				if err := tx.SetEmbedding(ctx, r.table, r.id, vecs[i], model); err != nil {
					return err
				}
				out.Embedded++
			}
			return nil
		}); err != nil {
			return EmbedOutput{}, err
		}
	}
	var err error
	if out.Pending, err = a.countPending(ctx, model); err != nil {
		return EmbedOutput{}, err
	}
	return out, nil
}

// pendingRow is one row of a batch: where it lives and the text to embed.
type pendingRow struct {
	table store.Embeddable
	id    domain.ID
	text  string
}

// embedRows embeds rows in one call. When the provider rejects the call for
// a reason other than being unavailable, it tries each row alone and returns
// a nil vector for each row still rejected. Alone, each vector passes the
// size check by itself, so a vector whose size differs from the one most
// rows got is rejected too: stored, it would make every vector search for
// the model fail. If every row is rejected, it returns the batch's error.
func (a *App) embedRows(ctx context.Context, rows []pendingRow) ([][]float32, error) {
	texts := make([]string, len(rows))
	for i, r := range rows {
		texts[i] = r.text
	}
	vecs, batchErr := a.embedDocuments(ctx, texts)
	if batchErr == nil {
		return vecs, nil
	}
	if len(rows) == 1 || errors.Is(batchErr, domain.ErrUnavailable) || ctx.Err() != nil {
		return nil, batchErr
	}
	vecs = make([][]float32, len(rows))
	sizes := map[int]int{}
	for i, text := range texts {
		v, err := a.embedDocuments(ctx, []string{text})
		switch {
		case errors.Is(err, domain.ErrUnavailable) || ctx.Err() != nil:
			return nil, err
		case err == nil:
			vecs[i] = v[0]
			sizes[len(v[0])]++
		}
	}
	if len(sizes) == 0 {
		return nil, batchErr
	}
	size := 0
	for n, count := range sizes {
		if count > sizes[size] || count == sizes[size] && n < size {
			size = n
		}
	}
	for i, v := range vecs {
		if v != nil && len(v) != size {
			vecs[i] = nil
		}
	}
	return vecs, nil
}

// embedDocuments embeds texts as documents and checks the vectors.
func (a *App) embedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	vecs, err := a.embedder.Embed(ctx, embed.Document, texts)
	if err == nil {
		err = checkVectors(vecs)
	}
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	return vecs, nil
}

// countPending counts the rows of every embedded table that need an
// embedding from model.
func (a *App) countPending(ctx context.Context, model string) (int, error) {
	total := 0
	for _, t := range embedOrder {
		n, err := a.store.CountPendingEmbeddings(ctx, t, model)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// checkVectors rejects provider output the engine cannot compare: an empty
// vector, vectors of different sizes, or a value that is not a finite number.
// Stored, one such vector would make every vector search for the model fail.
func checkVectors(vecs [][]float32) error {
	for i, v := range vecs {
		if len(v) == 0 {
			return fmt.Errorf("provider returned an empty vector")
		}
		if len(v) != len(vecs[0]) {
			return fmt.Errorf("provider returned vectors of %d and %d dimensions", len(vecs[0]), len(vecs[i]))
		}
		for _, x := range v {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				return fmt.Errorf("provider returned a vector containing %v", x)
			}
		}
	}
	return nil
}

// EmbeddingBacklog reports the embedding model, how many memories and
// messages still need an embedding from it, and how many were set aside as
// failed under it. loci serve uses it for its status; it is not an
// operation. An empty model means embedding is off.
func (a *App) EmbeddingBacklog(ctx context.Context) (model string, pending, failed int, err error) {
	if a.embedder == nil {
		return "", 0, 0, nil
	}
	model = a.embedder.Model()
	if pending, err = a.countPending(ctx, model); err != nil {
		return "", 0, 0, err
	}
	for _, t := range embedOrder {
		n, err := a.store.CountFailedEmbeddings(ctx, t, model)
		if err != nil {
			return "", 0, 0, err
		}
		failed += n
	}
	return model, pending, failed, nil
}
