package store

import (
	"context"
	"fmt"

	"github.com/OrestisStefanou/Loci/internal/domain"
)

const selectTask = `SELECT t.id, t.key, coalesce(p.slug, ''), t.created_at
	FROM tasks t LEFT JOIN projects p ON p.id = t.project_id`

// EnsureTask inserts t (without a project) if no task with t.Key exists and
// returns the stored task either way. t.ProjectSlug is ignored; use
// SetTaskProject.
func (q queries) EnsureTask(ctx context.Context, t domain.Task) (domain.Task, error) {
	if _, err := q.db.ExecContext(ctx,
		`INSERT INTO tasks (id, key, created_at) VALUES (?, ?, ?) ON CONFLICT (key) DO NOTHING`,
		t.ID, t.Key, formatTime(t.CreatedAt)); err != nil {
		return domain.Task{}, fmt.Errorf("insert task %q: %w", t.Key, err)
	}
	return q.GetTask(ctx, t.Key)
}

// GetTask returns the task with the given key, or domain.ErrNotFound.
func (q queries) GetTask(ctx context.Context, key string) (domain.Task, error) {
	t, err := scanTask(q.db.QueryRowContext(ctx, selectTask+` WHERE t.key = ?`, key))
	if isNoRows(err) {
		return domain.Task{}, fmt.Errorf("task %q: %w", key, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Task{}, fmt.Errorf("get task %q: %w", key, err)
	}
	return t, nil
}

// SetTaskProject sets the task's project, or clears it when projectID is nil.
// Everything attached to the task follows, because the effective project is
// resolved at read time (design.md section 4).
func (q queries) SetTaskProject(ctx context.Context, taskID domain.ID, projectID *domain.ID) error {
	res, err := q.db.ExecContext(ctx,
		`UPDATE tasks SET project_id = ? WHERE id = ?`, nullString(projectID), taskID)
	if err != nil {
		return fmt.Errorf("set task project: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("task id %s: %w", taskID, domain.ErrNotFound)
	}
	return nil
}

// ListTasks returns tasks whose key contains query (all when empty), the most
// recently used first, then by key.
func (q queries) ListTasks(ctx context.Context, query string) ([]domain.Task, error) {
	rows, err := q.db.QueryContext(ctx, selectTask+` WHERE instr(t.key, ?) > 0 ORDER BY `+taskLastUsed+` DESC, t.key`, query)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	out := []domain.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// taskLastUsed is when task t was last worked on: the latest of its creation,
// a conversation on it starting, a message in one, and a memory saved on it
// (a memory saved in a conversation dates from one of its messages). It
// compares RFC 3339 text, as the other orderings by time do.
const taskLastUsed = `max(t.created_at,
	coalesce((SELECT max(c.started_at) FROM conversations c WHERE c.task_id = t.id), ''),
	coalesce((SELECT max(m.created_at) FROM messages m JOIN conversations c ON c.id = m.conversation_id
		WHERE c.task_id = t.id), ''),
	coalesce((SELECT max(created_at) FROM memories WHERE task_id = t.id), ''))`

type scanner interface{ Scan(dest ...any) error }

func scanTask(s scanner) (domain.Task, error) {
	var t domain.Task
	var created string
	if err := s.Scan(&t.ID, &t.Key, &t.ProjectSlug, &created); err != nil {
		return domain.Task{}, err
	}
	var err error
	t.CreatedAt, err = parseTime(created)
	return t, err
}
