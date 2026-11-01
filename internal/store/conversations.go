package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/OrestisStefanou/Loci/internal/domain"
)

// ConversationRow is what EnsureConversation writes: a conversation with its
// foreign keys resolved. Empty strings are stored as NULL, except AgentID,
// which is part of the key and empty for a session's main conversation.
type ConversationRow struct {
	ID           domain.ID
	Harness      string
	SessionID    string
	AgentID      string
	AgentType    string
	ParentID     *domain.ID
	HumanActorID *domain.ID
	ProjectID    *domain.ID
	TaskID       *domain.ID
	// Repo and Branch are where the conversation started. Provenance only:
	// nothing is inferred or retrieved by them (design.md section 1).
	Repo, Branch string
	StartedAt    time.Time
}

// selectConversation is the one place a conversation's effective attachment
// is resolved, by two rules:
//
//  1. A subagent run with no project and no task of its own uses its
//     parent's. The parent (pc) is joined only then, so a run with either of
//     its own ignores the parent entirely.
//  2. The project is the task's project when the task has one, else the
//     direct one (design.md section 4).
//
// With EPD-1 on project trade-settlement:
//
//	main conversation, own fees-v2/EPD-1      -> trade-settlement/EPD-1 (rule 2)
//	subagent run, nothing of its own          -> trade-settlement/EPD-1 (rules 1, 2)
//	subagent run, own project x and no task   -> x
//
// Nothing is copied between rows, so re-attaching the main conversation moves
// its subagent runs too.
const selectConversation = `SELECT ` + conversationColumns + conversationFrom

const conversationColumns = `c.id, c.harness, c.session_id, c.agent_id, coalesce(c.agent_type, ''),
		coalesce(c.parent_conversation_id, ''), coalesce(tp.slug, cp.slug, ''), coalesce(t.key, ''),
		coalesce(h.handle, ''), coalesce(c.repo, ''), coalesce(c.branch, ''), c.started_at`

const conversationFrom = `
	FROM conversations c
	LEFT JOIN conversations pc ON pc.id = c.parent_conversation_id
		AND c.project_id IS NULL AND c.task_id IS NULL
	LEFT JOIN tasks t ON t.id = coalesce(c.task_id, pc.task_id)
	LEFT JOIN projects tp ON tp.id = t.project_id
	LEFT JOIN projects cp ON cp.id = coalesce(c.project_id, pc.project_id)
	LEFT JOIN actors h ON h.id = c.human_actor_id`

// summaryColumns are what a listing adds to a conversation: its message
// count, its subagent-run count and its first message.
const summaryColumns = `,
		(SELECT count(*) FROM messages WHERE conversation_id = c.id),
		(SELECT count(*) FROM conversations WHERE parent_conversation_id = c.id),
		coalesce((SELECT content FROM messages WHERE conversation_id = c.id ORDER BY seq LIMIT 1), '')`

// ConversationFilter bounds ListConversations. Nil or empty fields do not
// filter. Project and task match the effective attachment, so a subagent run
// with none of its own matches its parent's.
type ConversationFilter struct {
	ID        *domain.ID
	ProjectID *domain.ID
	TaskID    *domain.ID
	// Harness and Session match the agent tool and its session id.
	Harness string
	Session string
	// TagIDs must all be attached to the conversation itself (AND).
	TagIDs []domain.ID
	// ParentID lists the subagent runs of one conversation.
	ParentID *domain.ID
	// Query is a case-insensitive substring of a message. A main conversation
	// also matches through its subagent runs' messages. Each result's Match is
	// its first message containing it. Ignored when QueryVector is set.
	Query string
	// QueryVector, when set, orders the result by the cosine distance of each
	// conversation's nearest message, nearest first, and Match is that
	// message. As with Query, a main conversation's messages include its
	// subagent runs'. Only messages embedded by Model are compared, as in
	// MemoryFilter, and a conversation with none is left out.
	QueryVector []float32
	Model       string
	// MaxDistance, with QueryVector, leaves out conversations whose nearest
	// message is farther than this. Zero keeps them all.
	MaxDistance float64
	// IncludeSubagents also lists subagent runs. Without it, and without
	// ID or ParentID, only main conversations are listed.
	IncludeSubagents bool
	// Limit caps the result. Zero or negative means no cap.
	Limit int
}

// matchJoin joins each conversation to its best message: the one ranked
// first by score, then by age and id, among the messages meeting cond. A
// session's messages include its subagent runs', so a run's message is
// offered to both the run and its session. Each message is scored once,
// which keeps search linear in the number of messages; a subquery per
// conversation would be quadratic. It returns the join and its arguments.
func matchJoin(score string, scoreArgs []any, cond string, condArgs []any) (string, []any) {
	var args []any
	for range 2 {
		args = append(append(args, scoreArgs...), condArgs...)
	}
	return `
	JOIN (SELECT owner, message_id, score FROM (
		SELECT owner, message_id, score,
			row_number() OVER (PARTITION BY owner ORDER BY score, created_at, message_id) AS rn FROM (
			SELECT x.conversation_id AS owner, x.id AS message_id, ` + score + ` AS score, x.created_at
			FROM messages x WHERE ` + cond + `
			UNION ALL
			SELECT r.parent_conversation_id, x.id, ` + score + `, x.created_at
			FROM messages x JOIN conversations r ON r.id = x.conversation_id
			WHERE r.parent_conversation_id IS NOT NULL AND ` + cond + `))
		WHERE rn = 1) best ON best.owner = c.id`, args
}

// ListConversations returns the conversations matching f with their counts
// and opening message: most recently started first, or nearest first with a
// QueryVector.
func (q queries) ListConversations(ctx context.Context, f ConversationFilter) ([]domain.ConversationSummary, error) {
	// With a query, best is the matching message. Its arguments come before
	// the WHERE clause's.
	matchColumn, match := `, NULL`, ""
	var args []any
	order := ` ORDER BY c.started_at DESC, c.id DESC`
	switch {
	case len(f.QueryVector) > 0:
		// The query vector is bound as a blob: vector32(?) would be parsed
		// again for every message.
		matchColumn = `, best.message_id`
		match, args = matchJoin(`vector_distance_cos(x.embedding, ?)`, []any{vectorBlob(f.QueryVector)},
			`x.embedding IS NOT NULL AND x.embedding_model = ?`, []any{f.Model})
		order = ` ORDER BY best.score, c.started_at DESC, c.id DESC`
	case f.Query != "":
		// The first message containing the query, by age.
		matchColumn = `, best.message_id`
		match, args = matchJoin(`0`, nil, `instr(lower(x.content), lower(?)) > 0`, []any{f.Query})
	}

	where := []string{"1 = 1"}
	if f.ID != nil {
		where = append(where, "c.id = ?")
		args = append(args, *f.ID)
	}
	if f.ParentID != nil {
		where = append(where, "c.parent_conversation_id = ?")
		args = append(args, *f.ParentID)
	}
	if f.ID == nil && f.ParentID == nil && !f.IncludeSubagents {
		where = append(where, "c.agent_id = ''")
	}
	if f.Harness != "" {
		where = append(where, "c.harness = ?")
		args = append(args, f.Harness)
	}
	if f.Session != "" {
		where = append(where, "c.session_id = ?")
		args = append(args, f.Session)
	}
	if f.ProjectID != nil {
		where = append(where, "coalesce(t.project_id, c.project_id, pc.project_id) = ?")
		args = append(args, *f.ProjectID)
	}
	if f.TaskID != nil {
		where = append(where, "t.id = ?")
		args = append(args, *f.TaskID)
	}
	if len(f.TagIDs) > 0 {
		where = append(where, `c.id IN (SELECT item_id FROM item_tags WHERE item_kind = ? AND tag_id IN (`+
			placeholders(len(f.TagIDs))+`) GROUP BY item_id HAVING count(*) = ?)`)
		args = append(args, domain.ItemConversation)
		for _, id := range f.TagIDs {
			args = append(args, id)
		}
		args = append(args, len(f.TagIDs))
	}
	if len(f.QueryVector) > 0 && f.MaxDistance > 0 {
		where = append(where, "best.score <= ?")
		args = append(args, f.MaxDistance)
	}
	query := `SELECT ` + conversationColumns + summaryColumns + matchColumn + conversationFrom + match +
		` WHERE ` + strings.Join(where, " AND ") + order
	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}

	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	out := []domain.ConversationSummary{}
	var matches []domain.ID
	for rows.Next() {
		var s domain.ConversationSummary
		var started string
		var matchID sql.NullString
		c := &s.Conversation
		if err := rows.Scan(&c.ID, &c.Harness, &c.Session, &c.Agent, &c.AgentType, &c.Parent, &c.Project, &c.Task,
			&c.Human, &c.Repo, &c.Branch, &started, &s.Messages, &s.Subagents, &s.Opening, &matchID); err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		if c.StartedAt, err = parseTime(started); err != nil {
			return nil, err
		}
		if matchID.Valid {
			s.Match = &domain.MessageMatch{Message: domain.ID(matchID.String)}
			matches = append(matches, s.Match.Message)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := q.fillMatches(ctx, out, matches); err != nil {
		return nil, err
	}
	ids := make([]domain.ID, len(out))
	for i, s := range out {
		ids[i] = s.ID
	}
	tags, err := q.ItemTags(ctx, domain.ItemConversation, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Tags = tagsOrEmpty(tags[out[i].ID])
	}
	return out, nil
}

// fillMatches completes each summary's Match, which holds only the message
// id, with the message's conversation and its whole content as the snippet.
// The app shortens the snippet.
func (q queries) fillMatches(ctx context.Context, out []domain.ConversationSummary, ids []domain.ID) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, conversation_id, content FROM messages WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return fmt.Errorf("matched messages: %w", err)
	}
	defer rows.Close()
	found := map[domain.ID]domain.MessageMatch{}
	for rows.Next() {
		var m domain.MessageMatch
		if err := rows.Scan(&m.Message, &m.Conversation, &m.Snippet); err != nil {
			return fmt.Errorf("scan matched message: %w", err)
		}
		found[m.Message] = m
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range out {
		if out[i].Match != nil {
			m := found[out[i].Match.Message]
			out[i].Match = &m
		}
	}
	return nil
}

// EnsureConversation inserts r if no conversation has its harness, session
// and agent, and returns the stored one either way, with created reporting
// whether r was inserted.
func (q queries) EnsureConversation(ctx context.Context, r ConversationRow) (domain.Conversation, bool, error) {
	if _, err := q.db.ExecContext(ctx,
		`INSERT INTO conversations (id, harness, session_id, agent_id, agent_type, parent_conversation_id,
			human_actor_id, repo, branch, project_id, task_id, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (harness, session_id, agent_id) DO NOTHING`,
		r.ID, r.Harness, r.SessionID, r.AgentID, nullString(optional(r.AgentType)), nullString(r.ParentID),
		nullString(r.HumanActorID), nullString(optional(r.Repo)), nullString(optional(r.Branch)),
		nullString(r.ProjectID), nullString(r.TaskID), formatTime(r.StartedAt)); err != nil {
		return domain.Conversation{}, false, fmt.Errorf("insert conversation: %w", err)
	}
	c, err := q.FindConversation(ctx, r.Harness, r.SessionID, r.AgentID)
	if err != nil {
		return domain.Conversation{}, false, err
	}
	return c, c.ID == r.ID, nil
}

// AttachIfUnattached sets the project and task of a conversation that has
// neither of its own, and reports whether it did. A conversation with either
// is left as it is, so a correction is never overwritten.
func (q queries) AttachIfUnattached(ctx context.Context, id domain.ID, projectID, taskID *domain.ID) (bool, error) {
	res, err := q.db.ExecContext(ctx,
		`UPDATE conversations SET project_id = ?, task_id = ?
		WHERE id = ? AND project_id IS NULL AND task_id IS NULL`,
		nullString(projectID), nullString(taskID), id)
	if err != nil {
		return false, fmt.Errorf("attach conversation %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetConversationProject sets the conversation's own project, or clears it
// when nil.
func (q queries) SetConversationProject(ctx context.Context, id domain.ID, projectID *domain.ID) error {
	return q.updateConversation(ctx, id, "project_id = ?", nullString(projectID))
}

// SetConversationTask sets the conversation's own task, or clears it when nil.
func (q queries) SetConversationTask(ctx context.Context, id domain.ID, taskID *domain.ID) error {
	return q.updateConversation(ctx, id, "task_id = ?", nullString(taskID))
}

// updateConversation applies set (a fixed SET clause with placeholders for
// args) to one conversation, or returns domain.ErrNotFound.
func (q queries) updateConversation(ctx context.Context, id domain.ID, set string, args ...any) error {
	res, err := q.db.ExecContext(ctx, `UPDATE conversations SET `+set+` WHERE id = ?`, append(args, id)...)
	if err != nil {
		return fmt.Errorf("update conversation %s: %w", id, err)
	}
	return requireRow(res, "conversation", id)
}

// FindConversation returns the conversation with the given key, or
// domain.ErrNotFound. agent is empty for a session's main conversation.
func (q queries) FindConversation(ctx context.Context, harness, session, agent string) (domain.Conversation, error) {
	c, err := q.getConversation(ctx, ` WHERE c.harness = ? AND c.session_id = ? AND c.agent_id = ?`,
		harness, session, agent)
	if isNoRows(err) {
		return domain.Conversation{}, fmt.Errorf("conversation %s/%s%s: %w", harness, session, agentSuffix(agent), domain.ErrNotFound)
	}
	return c, err
}

// GetConversation returns one conversation with its tags, or domain.ErrNotFound.
func (q queries) GetConversation(ctx context.Context, id domain.ID) (domain.Conversation, error) {
	c, err := q.getConversation(ctx, ` WHERE c.id = ?`, id)
	if isNoRows(err) {
		return domain.Conversation{}, fmt.Errorf("conversation %s: %w", id, domain.ErrNotFound)
	}
	return c, err
}

func (q queries) getConversation(ctx context.Context, where string, args ...any) (domain.Conversation, error) {
	c, err := scanConversation(q.db.QueryRowContext(ctx, selectConversation+where, args...))
	if err != nil {
		if isNoRows(err) {
			return domain.Conversation{}, err
		}
		return domain.Conversation{}, fmt.Errorf("get conversation: %w", err)
	}
	tags, err := q.ItemTags(ctx, domain.ItemConversation, []domain.ID{c.ID})
	if err != nil {
		return domain.Conversation{}, err
	}
	c.Tags = tagsOrEmpty(tags[c.ID])
	return c, nil
}

func scanConversation(s scanner) (domain.Conversation, error) {
	var c domain.Conversation
	var started string
	if err := s.Scan(&c.ID, &c.Harness, &c.Session, &c.Agent, &c.AgentType, &c.Parent, &c.Project, &c.Task,
		&c.Human, &c.Repo, &c.Branch, &started); err != nil {
		return domain.Conversation{}, err
	}
	var err error
	c.StartedAt, err = parseTime(started)
	return c, err
}

func agentSuffix(agent string) string {
	if agent == "" {
		return ""
	}
	return "/" + agent
}
