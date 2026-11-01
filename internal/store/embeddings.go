package store

import (
	"context"
	"fmt"

	"github.com/OrestisStefanou/Loci/internal/domain"
)

// Embeddable names a table whose rows carry an embedding of their content
// (design.md section 10).
type Embeddable string

// The embedded tables. The value is the table name, interpolated into SQL,
// so it only ever comes from these constants.
const (
	EmbedMemories Embeddable = "memories"
	EmbedMessages Embeddable = "messages"
)

// EmbedText is a row's ID and content: what the embedding worker needs.
type EmbedText struct {
	ID      domain.ID
	Content string
}

// pendingEmbedding selects rows with no embedding from model: never
// embedded, or embedded by another model. Rows set aside as failed under
// model are left out. Blank content has nothing to embed and would otherwise
// stay at the head of the queue forever. trim() strips only spaces unless
// told otherwise, so tabs and newlines are listed. Both placeholders take
// the model.
const pendingEmbedding = `(embedding IS NULL OR embedding_model IS NULL OR embedding_model != ?)
	AND (embedding_failed IS NULL OR embedding_failed != ?)
	AND trim(content, ' ' || char(9, 10, 13)) != ''`

// PendingEmbeddings returns up to limit rows of t that need an embedding
// from model, oldest first.
func (q queries) PendingEmbeddings(ctx context.Context, t Embeddable, model string, limit int) ([]EmbedText, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, content FROM `+string(t)+` WHERE `+pendingEmbedding+` ORDER BY created_at, id LIMIT ?`, model, model, limit)
	if err != nil {
		return nil, fmt.Errorf("pending %s embeddings: %w", t, err)
	}
	defer rows.Close()
	out := []EmbedText{}
	for rows.Next() {
		var r EmbedText
		if err := rows.Scan(&r.ID, &r.Content); err != nil {
			return nil, fmt.Errorf("scan pending %s: %w", t, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountPendingEmbeddings counts rows of t that need an embedding from model.
func (q queries) CountPendingEmbeddings(ctx context.Context, t Embeddable, model string) (int, error) {
	var n int
	if err := q.db.QueryRowContext(ctx,
		`SELECT count(*) FROM `+string(t)+` WHERE `+pendingEmbedding, model, model).Scan(&n); err != nil {
		return 0, fmt.Errorf("count pending %s embeddings: %w", t, err)
	}
	return n, nil
}

// CountFailedEmbeddings counts rows of t set aside as failed under model.
func (q queries) CountFailedEmbeddings(ctx context.Context, t Embeddable, model string) (int, error) {
	var n int
	if err := q.db.QueryRowContext(ctx,
		`SELECT count(*) FROM `+string(t)+` WHERE embedding_failed = ?`, model).Scan(&n); err != nil {
		return 0, fmt.Errorf("count failed %s embeddings: %w", t, err)
	}
	return n, nil
}

// MarkEmbeddingFailed sets a row aside: the provider rejected it under model,
// so it is no longer pending for that model.
func (q queries) MarkEmbeddingFailed(ctx context.Context, t Embeddable, id domain.ID, model string) error {
	res, err := q.db.ExecContext(ctx, `UPDATE `+string(t)+` SET embedding_failed = ? WHERE id = ?`, model, id)
	if err != nil {
		return fmt.Errorf("mark %s embedding failed: %w", t, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("%s %s: %w", t, id, domain.ErrNotFound)
	}
	return nil
}

// SetEmbedding stores a row's vector and the model that produced it, and
// clears any earlier failure.
func (q queries) SetEmbedding(ctx context.Context, t Embeddable, id domain.ID, vec []float32, model string) error {
	res, err := q.db.ExecContext(ctx,
		`UPDATE `+string(t)+` SET embedding = vector32(?), embedding_model = ?, embedding_failed = NULL WHERE id = ?`,
		encodeVector(vec), model, id)
	if err != nil {
		return fmt.Errorf("set %s embedding: %w", t, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("%s %s: %w", t, id, domain.ErrNotFound)
	}
	return nil
}

// ClearEmbeddings drops the embedding and any failure of every row of t, so
// all are embedded again, and returns how many rows had either.
func (q queries) ClearEmbeddings(ctx context.Context, t Embeddable) (int64, error) {
	res, err := q.db.ExecContext(ctx,
		`UPDATE `+string(t)+` SET embedding = NULL, embedding_model = NULL, embedding_failed = NULL
		WHERE embedding IS NOT NULL OR embedding_model IS NOT NULL OR embedding_failed IS NOT NULL`)
	if err != nil {
		return 0, fmt.Errorf("clear %s embeddings: %w", t, err)
	}
	return res.RowsAffected()
}
