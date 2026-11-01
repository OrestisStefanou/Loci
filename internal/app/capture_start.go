package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/store"
)

// CaptureKey identifies a captured conversation by what the agent tool knows,
// so callers never need a Loci ID (design.md section 5).
type CaptureKey struct {
	// Harness is the agent tool, e.g. "claude-code".
	Harness string `json:"harness" jsonschema:"the agent tool the conversation runs in, e.g. claude-code"`
	// Session is the tool's own session id.
	Session string `json:"session" jsonschema:"the tool's session id"`
	// Agent is the tool's id for a subagent run inside the session. Empty
	// means the session's main conversation.
	Agent string `json:"agent,omitempty" jsonschema:"the tool's id for a subagent run; omit for the main conversation"`
}

// CaptureStartInput is the input of CaptureStart (CLI only: loci capture start).
type CaptureStartInput struct {
	CaptureKey
	// AgentType is the subagent's type, e.g. "tech-pm-agent".
	AgentType string `json:"agent_type,omitempty" jsonschema:"the subagent's type, e.g. tech-pm-agent"`
	// Project and Task attach the conversation, created if new. Empty leaves
	// it unattached; nothing is inferred (design.md section 7). They apply
	// only while the conversation has neither of its own (it is new, or was
	// started by a message first), so a correction is never overwritten.
	Project string `json:"project,omitempty" jsonschema:"project slug; created if new; ignored once the conversation has a project or task"`
	Task    string `json:"task,omitempty" jsonschema:"task key, e.g. EPD-100; created if new; ignored once the conversation has a project or task"`
	// Repo and Branch are where the conversation started, kept as provenance
	// only. Repo is the origin URL in any form git accepts; it is stored
	// normalised, without credentials.
	Repo   string `json:"repo,omitempty" jsonschema:"repository origin URL"`
	Branch string `json:"branch,omitempty" jsonschema:"git branch"`
}

// CaptureStartOutput is the output of CaptureStart.
type CaptureStartOutput struct {
	Conversation domain.Conversation `json:"conversation"`
	// Created is false when the conversation already existed (a resumed session).
	Created bool `json:"created"`
}

// CaptureStart starts a conversation, or returns it if its key exists (a
// resumed session). The project and task given, if any, are applied while the
// conversation has neither of its own: when it is new, or was created
// unattached by an earlier message. Otherwise it is left as it is, so a
// corrected attachment survives. A subagent run is linked to the session's
// main conversation (created if needed) and shares its attachment unless
// given its own.
func (a *App) CaptureStart(ctx context.Context, in CaptureStartInput) (CaptureStartOutput, error) {
	key, err := in.normalize()
	if err != nil {
		return CaptureStartOutput{}, err
	}
	var at attachment
	if strings.TrimSpace(in.Project) != "" {
		if at.project, err = domain.NormalizeProjectSlug(in.Project); err != nil {
			return CaptureStartOutput{}, err
		}
	}
	if strings.TrimSpace(in.Task) != "" {
		if at.task, err = domain.NormalizeTaskKey(in.Task); err != nil {
			return CaptureStartOutput{}, err
		}
	}
	agentType := strings.TrimSpace(in.AgentType)
	where := provenance{repo: domain.NormalizeRepo(in.Repo), branch: strings.TrimSpace(in.Branch)}
	if err := checkLen("agent type", agentType); err != nil {
		return CaptureStartOutput{}, err
	}
	if err := checkLen("repo", strings.TrimSpace(in.Repo)); err != nil {
		return CaptureStartOutput{}, err
	}
	if err := checkLen("branch", where.branch); err != nil {
		return CaptureStartOutput{}, err
	}

	a.captureMu.Lock()
	defer a.captureMu.Unlock()
	var out CaptureStartOutput
	err = a.store.WithTx(ctx, func(tx *store.Tx) error {
		c, created, err := a.ensureConversation(ctx, tx, key, agentType, at, where)
		if err != nil {
			return err
		}
		if !created && at != (attachment{}) {
			projectID, taskID, err := a.ensureAttachment(ctx, tx, at)
			if err != nil {
				return err
			}
			applied, err := tx.AttachIfUnattached(ctx, c.ID, projectID, taskID)
			if err != nil {
				return err
			}
			if applied {
				if c, err = tx.GetConversation(ctx, c.ID); err != nil {
					return err
				}
			}
		}
		out = CaptureStartOutput{Conversation: c, Created: created}
		return nil
	})
	return out, err
}

// attachment is a normalised project slug and task key, either empty.
type attachment struct{ project, task string }

// provenance is where a conversation started, normalised.
type provenance struct{ repo, branch string }

// captureKey is a CaptureKey after normalisation.
type captureKey struct{ harness, session, agent string }

// maxToolValueLen bounds every value an agent tool supplies: ids, agent
// types, repository and branch names.
const maxToolValueLen = 256

func checkLen(field, v string) error {
	if len(v) > maxToolValueLen {
		return fmt.Errorf("%s must be at most %d characters: %w", field, maxToolValueLen, domain.ErrInvalid)
	}
	return nil
}

func (k CaptureKey) normalize() (captureKey, error) {
	harness, err := domain.NormalizeHarness(k.Harness)
	if err != nil {
		return captureKey{}, err
	}
	session := strings.TrimSpace(k.Session)
	if session == "" {
		return captureKey{}, fmt.Errorf("session must not be empty: %w", domain.ErrInvalid)
	}
	agent := strings.TrimSpace(k.Agent)
	if err := checkLen("session", session); err != nil {
		return captureKey{}, err
	}
	if err := checkLen("agent", agent); err != nil {
		return captureKey{}, err
	}
	return captureKey{harness: harness, session: session, agent: agent}, nil
}

// ensureConversation returns the conversation for key, creating it if it does
// not exist (and, for a subagent run, its parent) attached to at. Callers
// hold captureMu.
func (a *App) ensureConversation(ctx context.Context, tx *store.Tx, key captureKey, agentType string, at attachment, where provenance) (domain.Conversation, bool, error) {
	c, err := tx.FindConversation(ctx, key.harness, key.session, key.agent)
	if err == nil {
		return c, false, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.Conversation{}, false, err
	}

	human, err := a.ensureActor(ctx, tx, domain.Actor{Kind: domain.ActorHuman, Handle: a.userHandle})
	if err != nil {
		return domain.Conversation{}, false, err
	}
	row := store.ConversationRow{Harness: key.harness, SessionID: key.session, AgentID: key.agent,
		AgentType: agentType, HumanActorID: &human.ID, Repo: where.repo, Branch: where.branch}
	row.ID, row.StartedAt = a.newID()
	if key.agent != "" {
		parent, _, err := a.ensureConversation(ctx, tx, captureKey{harness: key.harness, session: key.session}, "", attachment{}, where)
		if err != nil {
			return domain.Conversation{}, false, err
		}
		row.ParentID = &parent.ID
	}
	if row.ProjectID, row.TaskID, err = a.ensureAttachment(ctx, tx, at); err != nil {
		return domain.Conversation{}, false, err
	}
	return tx.EnsureConversation(ctx, row)
}

// ensureAttachment creates and returns the project and task in at, either
// nil when not given.
func (a *App) ensureAttachment(ctx context.Context, tx *store.Tx, at attachment) (projectID, taskID *domain.ID, err error) {
	if at.project != "" {
		id, now := a.newID()
		p, err := tx.EnsureProject(ctx, domain.Project{ID: id, Slug: at.project, CreatedAt: now})
		if err != nil {
			return nil, nil, err
		}
		projectID = &p.ID
	}
	if at.task != "" {
		id, now := a.newID()
		t, err := tx.EnsureTask(ctx, domain.Task{ID: id, Key: at.task, CreatedAt: now})
		if err != nil {
			return nil, nil, err
		}
		taskID = &t.ID
	}
	return projectID, taskID, nil
}

// ensureActor creates actor lazily by handle and returns the stored one.
func (a *App) ensureActor(ctx context.Context, tx *store.Tx, actor domain.Actor) (domain.Actor, error) {
	actor.ID, _ = a.newID()
	return tx.EnsureActor(ctx, actor)
}
