package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/OrestisStefanou/Loci/internal/domain"
)

// MessageRow is what InsertMessage writes, with actors resolved to IDs. The
// sequence number is assigned by InsertMessage.
type MessageRow struct {
	ID               domain.ID
	ConversationID   domain.ID
	PromptID         string
	Role             domain.Role
	SpeakerActorID   *domain.ID
	AddresseeActorID *domain.ID
	Content          string
	CreatedAt        time.Time
}

const selectMessage = `SELECT m.id, m.conversation_id, m.seq, coalesce(m.prompt_id, ''), m.role,
		coalesce(s.handle, ''), coalesce(a.handle, ''), m.content, m.created_at
	FROM messages m
	LEFT JOIN actors s ON s.id = m.speaker_actor_id
	LEFT JOIN actors a ON a.id = m.addressee_actor_id`

// InsertMessage appends r to its conversation with the next sequence number
// (1 for the first). Callers serialise inserts into one conversation; two at
// once would compute the same seq and the second would fail the unique key.
func (q queries) InsertMessage(ctx context.Context, r MessageRow) error {
	if _, err := q.db.ExecContext(ctx,
		`INSERT INTO messages (id, conversation_id, seq, prompt_id, role, speaker_actor_id,
			addressee_actor_id, content, created_at)
		SELECT ?, ?, coalesce(max(seq), 0) + 1, ?, ?, ?, ?, ?, ? FROM messages WHERE conversation_id = ?`,
		r.ID, r.ConversationID, nullString(optional(r.PromptID)), r.Role, nullString(r.SpeakerActorID),
		nullString(r.AddresseeActorID), r.Content, formatTime(r.CreatedAt), r.ConversationID); err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	return nil
}

// GetMessage returns one message, or domain.ErrNotFound.
func (q queries) GetMessage(ctx context.Context, id domain.ID) (domain.Message, error) {
	m, err := scanMessage(q.db.QueryRowContext(ctx, selectMessage+` WHERE m.id = ?`, id))
	if isNoRows(err) {
		return domain.Message{}, fmt.Errorf("message %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Message{}, fmt.Errorf("get message %s: %w", id, err)
	}
	return m, nil
}

// ListMessages returns a conversation's messages in order, those with seq
// from first to last inclusive. Zero leaves that end open.
func (q queries) ListMessages(ctx context.Context, conversationID domain.ID, first, last int) ([]domain.Message, error) {
	query := selectMessage + ` WHERE m.conversation_id = ?`
	args := []any{conversationID}
	if first > 0 {
		query += ` AND m.seq >= ?`
		args = append(args, first)
	}
	if last > 0 {
		query += ` AND m.seq <= ?`
		args = append(args, last)
	}
	rows, err := q.db.QueryContext(ctx, query+` ORDER BY m.seq`, args...)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()
	out := []domain.Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// FindPromptMessage returns the conversation's message with the given role
// for promptID, or domain.ErrNotFound.
func (q queries) FindPromptMessage(ctx context.Context, conversationID domain.ID, promptID string, role domain.Role) (domain.Message, error) {
	m, err := scanMessage(q.db.QueryRowContext(ctx,
		selectMessage+` WHERE m.conversation_id = ? AND m.prompt_id = ? AND m.role = ? ORDER BY m.seq LIMIT 1`,
		conversationID, promptID, role))
	if isNoRows(err) {
		return domain.Message{}, fmt.Errorf("message for prompt %s: %w", promptID, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Message{}, fmt.Errorf("find message for prompt %s: %w", promptID, err)
	}
	return m, nil
}

// SetMessageContent replaces a message's content and clears its embedding,
// which described the old content, and any failure, so the worker embeds it
// again.
func (q queries) SetMessageContent(ctx context.Context, id domain.ID, content string) error {
	res, err := q.db.ExecContext(ctx,
		`UPDATE messages SET content = ?, embedding = NULL, embedding_model = NULL, embedding_failed = NULL WHERE id = ?`, content, id)
	if err != nil {
		return fmt.Errorf("set message content %s: %w", id, err)
	}
	return requireRow(res, "message", id)
}

func scanMessage(s scanner) (domain.Message, error) {
	var m domain.Message
	var created string
	if err := s.Scan(&m.ID, &m.ConversationID, &m.Seq, &m.PromptID, &m.Role, &m.Speaker, &m.Addressee,
		&m.Content, &created); err != nil {
		return domain.Message{}, err
	}
	var err error
	m.CreatedAt, err = parseTime(created)
	return m, err
}

// requireRow returns domain.ErrNotFound when an update by id matched nothing.
func requireRow(res sql.Result, kind string, id domain.ID) error {
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("%s %s: %w", kind, id, domain.ErrNotFound)
	}
	return nil
}
