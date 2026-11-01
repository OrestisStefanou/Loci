package store

import (
	"context"
	"fmt"

	"github.com/OrestisStefanou/Loci/internal/domain"
)

// EnsureActor inserts a if no actor with a.Handle exists and returns the
// stored actor either way. Handles are the identity: the kind and agent type
// of an existing actor are not changed.
func (q queries) EnsureActor(ctx context.Context, a domain.Actor) (domain.Actor, error) {
	if _, err := q.db.ExecContext(ctx,
		`INSERT INTO actors (id, kind, handle, agent_type) VALUES (?, ?, ?, ?) ON CONFLICT (handle) DO NOTHING`,
		a.ID, a.Kind, a.Handle, nullString(optional(a.AgentType))); err != nil {
		return domain.Actor{}, fmt.Errorf("insert actor %q: %w", a.Handle, err)
	}
	var out domain.Actor
	var agentType *string
	if err := q.db.QueryRowContext(ctx,
		`SELECT id, kind, handle, agent_type FROM actors WHERE handle = ?`, a.Handle).
		Scan(&out.ID, &out.Kind, &out.Handle, &agentType); err != nil {
		return domain.Actor{}, fmt.Errorf("get actor %q: %w", a.Handle, err)
	}
	if agentType != nil {
		out.AgentType = *agentType
	}
	return out, nil
}

// optional turns "" into nil so empty strings are stored as NULL.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
