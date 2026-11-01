package store

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/OrestisStefanou/Loci/internal/domain"
)

// MemoryRow is what InsertMemory writes: a memory with its foreign keys
// resolved. The app resolves slugs, keys and handles to IDs first.
type MemoryRow struct {
	ID        domain.ID
	Content   string
	ProjectID *domain.ID
	TaskID    *domain.ID
	// ConversationID is the conversation the memory was saved in, if any.
	ConversationID *domain.ID
	AuthorActorID  *domain.ID
	CreatedAt      time.Time
}

// MemoryFilter bounds ListMemories. Nil or empty fields do not filter.
type MemoryFilter struct {
	// ProjectID and TaskID match the effective attachment (selectMemory).
	ProjectID *domain.ID
	TaskID    *domain.ID
	// TagIDs must all be attached (AND).
	TagIDs []domain.ID
	// Query is a case-insensitive substring of the content. Ignored when
	// QueryVector is set.
	Query string
	// QueryVector, when set, orders the result by cosine distance to the
	// vector, nearest first. Only memories embedded by Model, the model that
	// produced QueryVector, are compared: vectors from different models live in
	// different spaces, so their distances mean nothing, and when the sizes
	// differ the engine rejects the comparison and fails the whole query.
	// Memories embedded by another model wait for the worker to re-embed them.
	QueryVector []float32
	Model       string
	// MaxDistance, with QueryVector, drops memories farther than this cosine
	// distance from it. Zero keeps them all.
	MaxDistance float64
	// IncludeInactive also returns inactive memories.
	IncludeInactive bool
	// Limit caps the result. Zero or negative means no cap.
	Limit int
}

// selectMemory is the one place a memory's effective attachment is
// resolved, by three rules:
//
//  1. A memory with no project and no task of its own uses its source
//     conversation's (c). The conversation is joined only then, so a memory
//     with either of its own ignores it entirely.
//  2. That conversation, when it is a subagent run with nothing of its own,
//     uses its parent's (pc), as selectConversation does.
//  3. The project is the task's project when the task has one, else the
//     direct one (design.md section 4).
//
// Nothing is copied between rows, so re-attaching a conversation moves the
// memories saved in it that have no attachment of their own.
const selectMemory = `SELECT m.id, m.content, coalesce(tp.slug, mp.slug, ''), coalesce(t.key, ''),
		coalesce(m.conversation_id, ''), coalesce(a.handle, ''), m.status, m.created_at, m.updated_at
	FROM memories m
	LEFT JOIN conversations c ON c.id = m.conversation_id
		AND m.project_id IS NULL AND m.task_id IS NULL
	LEFT JOIN conversations pc ON pc.id = c.parent_conversation_id
		AND c.project_id IS NULL AND c.task_id IS NULL
	LEFT JOIN tasks t ON t.id = coalesce(m.task_id, c.task_id, pc.task_id)
	LEFT JOIN projects tp ON tp.id = t.project_id
	LEFT JOIN projects mp ON mp.id = coalesce(m.project_id, c.project_id, pc.project_id)
	LEFT JOIN actors a ON a.id = m.author_actor_id`

// InsertMemory stores a new, active memory.
func (q queries) InsertMemory(ctx context.Context, r MemoryRow) error {
	now := formatTime(r.CreatedAt)
	if _, err := q.db.ExecContext(ctx,
		`INSERT INTO memories (id, content, project_id, task_id, conversation_id, author_actor_id, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Content, nullString(r.ProjectID), nullString(r.TaskID), nullString(r.ConversationID), nullString(r.AuthorActorID),
		domain.MemoryActive, now, now); err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}
	return nil
}

// GetMemory returns one memory with its tags, or domain.ErrNotFound.
func (q queries) GetMemory(ctx context.Context, id domain.ID) (domain.Memory, error) {
	m, err := scanMemory(q.db.QueryRowContext(ctx, selectMemory+` WHERE m.id = ?`, id))
	if isNoRows(err) {
		return domain.Memory{}, fmt.Errorf("memory %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Memory{}, fmt.Errorf("get memory %s: %w", id, err)
	}
	tags, err := q.ItemTags(ctx, domain.ItemMemory, []domain.ID{id})
	if err != nil {
		return domain.Memory{}, err
	}
	m.Tags = tagsOrEmpty(tags[id])
	return m, nil
}

// ListMemories returns the memories matching f, most recent first.
func (q queries) ListMemories(ctx context.Context, f MemoryFilter) ([]domain.Memory, error) {
	where := []string{"1 = 1"}
	var args []any
	if f.ProjectID != nil {
		where = append(where, "coalesce(t.project_id, m.project_id, c.project_id, pc.project_id) = ?")
		args = append(args, *f.ProjectID)
	}
	if f.TaskID != nil {
		where = append(where, "t.id = ?")
		args = append(args, *f.TaskID)
	}
	if len(f.TagIDs) > 0 {
		where = append(where, `m.id IN (SELECT item_id FROM item_tags WHERE item_kind = ? AND tag_id IN (`+
			placeholders(len(f.TagIDs))+`) GROUP BY item_id HAVING count(*) = ?)`)
		args = append(args, domain.ItemMemory)
		for _, id := range f.TagIDs {
			args = append(args, id)
		}
		args = append(args, len(f.TagIDs))
	}
	switch {
	case len(f.QueryVector) > 0:
		where = append(where, "m.embedding IS NOT NULL AND m.embedding_model = ?")
		args = append(args, f.Model)
		if f.MaxDistance > 0 {
			where = append(where, "vector_distance_cos(m.embedding, vector32(?)) <= ?")
			args = append(args, encodeVector(f.QueryVector), f.MaxDistance)
		}
	case f.Query != "":
		where = append(where, "instr(lower(m.content), lower(?)) > 0")
		args = append(args, f.Query)
	}
	if !f.IncludeInactive {
		where = append(where, "m.status = ?")
		args = append(args, domain.MemoryActive)
	}
	order := " ORDER BY m.created_at DESC, m.id DESC"
	if len(f.QueryVector) > 0 {
		order = " ORDER BY vector_distance_cos(m.embedding, vector32(?)), m.created_at DESC, m.id DESC"
		args = append(args, encodeVector(f.QueryVector))
	}
	sql := selectMemory + " WHERE " + strings.Join(where, " AND ") + order
	if f.Limit > 0 {
		sql += " LIMIT ?"
		args = append(args, f.Limit)
	}

	rows, err := q.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()
	out := []domain.Memory{}
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	ids := make([]domain.ID, len(out))
	for i, m := range out {
		ids[i] = m.ID
	}
	tags, err := q.ItemTags(ctx, domain.ItemMemory, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Tags = tagsOrEmpty(tags[out[i].ID])
	}
	return out, nil
}

// SetMemoryStatus updates a memory's status. Used by mark.
func (q queries) SetMemoryStatus(ctx context.Context, id domain.ID, status domain.MemoryStatus, at time.Time) error {
	return q.updateMemory(ctx, id, "status = ?", at, status)
}

// SetMemoryProject sets the memory's own project, or clears it when nil.
func (q queries) SetMemoryProject(ctx context.Context, id domain.ID, projectID *domain.ID, at time.Time) error {
	return q.updateMemory(ctx, id, "project_id = ?", at, nullString(projectID))
}

// SetMemoryTask sets the memory's task, or clears it when nil.
func (q queries) SetMemoryTask(ctx context.Context, id domain.ID, taskID *domain.ID, at time.Time) error {
	return q.updateMemory(ctx, id, "task_id = ?", at, nullString(taskID))
}

// TouchMemory bumps a memory's updated_at, for changes stored outside its
// row such as tags, or returns domain.ErrNotFound.
func (q queries) TouchMemory(ctx context.Context, id domain.ID, at time.Time) error {
	return q.updateMemory(ctx, id, "id = id", at)
}

// updateMemory applies set (a fixed SET clause with placeholders for args)
// to one memory and bumps updated_at, or returns domain.ErrNotFound.
func (q queries) updateMemory(ctx context.Context, id domain.ID, set string, at time.Time, args ...any) error {
	args = append(args, formatTime(at), id)
	res, err := q.db.ExecContext(ctx, `UPDATE memories SET `+set+`, updated_at = ? WHERE id = ?`, args...)
	if err != nil {
		return fmt.Errorf("update memory %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("memory %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

// vectorBlob encodes vec as the engine stores a vector32: its float32
// values, little-endian, with nothing after them.
func vectorBlob(vec []float32) []byte {
	b := make([]byte, 4*len(vec))
	for i, f := range vec {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return b
}

// encodeVector formats vec as the JSON array text vector32() parses.
func encodeVector(vec []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

func scanMemory(s scanner) (domain.Memory, error) {
	var m domain.Memory
	var created, updated string
	if err := s.Scan(&m.ID, &m.Content, &m.Project, &m.Task, &m.Conversation, &m.Author, &m.Status, &created, &updated); err != nil {
		return domain.Memory{}, err
	}
	var err error
	if m.CreatedAt, err = parseTime(created); err != nil {
		return domain.Memory{}, err
	}
	m.UpdatedAt, err = parseTime(updated)
	return m, err
}

func tagsOrEmpty(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}
