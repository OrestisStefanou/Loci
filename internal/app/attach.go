package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/store"
)

// AttachInput is the input of Attach (MCP: loci_attach). Omitted fields are
// left as they are; at least one must be given.
type AttachInput struct {
	// Item is the ID of the memory or conversation to re-attach. IDs are
	// unique across both.
	Item string `json:"item" jsonschema:"id of the memory or conversation to re-attach"`
	// Project slug. Nil leaves it unchanged, empty removes it, otherwise it is
	// set and created if new.
	Project *string `json:"project,omitempty" jsonschema:"project slug, created if new; empty string removes it; omit to leave unchanged"`
	// Task key. Nil leaves it unchanged, empty removes it, otherwise it is set
	// and created if new. The task's own project is not changed.
	Task *string `json:"task,omitempty" jsonschema:"task key, e.g. EPD-100, created if new; empty string removes it; omit to leave unchanged"`
	// Tags replace the item's tags. Nil leaves them unchanged, empty removes
	// them all.
	Tags *[]string `json:"tags,omitempty" jsonschema:"the complete new set of tags, created if new; empty list removes all; omit to leave unchanged"`
}

// AttachOutput is the output of Attach. Exactly one field is set, for the
// kind of item attached.
type AttachOutput struct {
	Memory       *domain.Memory       `json:"memory,omitempty"`
	Conversation *domain.Conversation `json:"conversation,omitempty"`
}

// Attach changes the project, task or tags of a memory or a conversation
// (product.md section 7). The item's effective project still follows its task
// when the task has one, and a subagent run left with neither project nor task
// of its own shares its parent's again (design.md section 4).
func (a *App) Attach(ctx context.Context, in AttachInput) (AttachOutput, error) {
	id := strings.TrimSpace(in.Item)
	if id == "" {
		return AttachOutput{}, fmt.Errorf("item must not be empty: %w", domain.ErrInvalid)
	}
	if in.Project == nil && in.Task == nil && in.Tags == nil {
		return AttachOutput{}, fmt.Errorf("give at least one of project, task or tags: %w", domain.ErrInvalid)
	}
	var slug, key string
	var tags []string
	var err error
	if in.Project != nil && strings.TrimSpace(*in.Project) != "" {
		if slug, err = domain.NormalizeProjectSlug(*in.Project); err != nil {
			return AttachOutput{}, err
		}
	}
	if in.Task != nil && strings.TrimSpace(*in.Task) != "" {
		if key, err = domain.NormalizeTaskKey(*in.Task); err != nil {
			return AttachOutput{}, err
		}
	}
	if in.Tags != nil {
		if tags, err = normalizeTags(*in.Tags); err != nil {
			return AttachOutput{}, err
		}
	}

	var out AttachOutput
	err = a.store.WithTx(ctx, func(tx *store.Tx) error {
		now := a.clock.Now()
		kind, err := itemKind(ctx, tx, id)
		if err != nil {
			return err
		}
		if kind == domain.ItemMemory {
			if err := tx.TouchMemory(ctx, id, now); err != nil {
				return err
			}
		}
		if in.Project != nil {
			var projectID *domain.ID
			if slug != "" {
				pid, created := a.newID()
				p, err := tx.EnsureProject(ctx, domain.Project{ID: pid, Slug: slug, CreatedAt: created})
				if err != nil {
					return err
				}
				projectID = &p.ID
			}
			if err := setProject(ctx, tx, kind, id, projectID, now); err != nil {
				return err
			}
		}
		if in.Task != nil {
			var taskID *domain.ID
			if key != "" {
				tid, created := a.newID()
				t, err := tx.EnsureTask(ctx, domain.Task{ID: tid, Key: key, CreatedAt: created})
				if err != nil {
					return err
				}
				taskID = &t.ID
			}
			if err := setTask(ctx, tx, kind, id, taskID, now); err != nil {
				return err
			}
		}
		if in.Tags != nil {
			tagIDs := make([]domain.ID, 0, len(tags))
			for _, name := range tags {
				tid, _ := a.newID()
				t, err := tx.EnsureTag(ctx, domain.Tag{ID: tid, Name: name})
				if err != nil {
					return err
				}
				tagIDs = append(tagIDs, t.ID)
			}
			if err := tx.SetItemTags(ctx, kind, id, tagIDs); err != nil {
				return err
			}
		}
		if kind == domain.ItemConversation {
			c, err := tx.GetConversation(ctx, id)
			out.Conversation = &c
			return err
		}
		m, err := tx.GetMemory(ctx, id)
		out.Memory = &m
		return err
	})
	if err != nil {
		return AttachOutput{}, err
	}
	return out, nil
}

// itemKind says whether id is a memory or a conversation, or returns
// domain.ErrNotFound.
func itemKind(ctx context.Context, tx *store.Tx, id domain.ID) (domain.ItemKind, error) {
	_, err := tx.GetMemory(ctx, id)
	if err == nil {
		return domain.ItemMemory, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return "", err
	}
	_, err = tx.GetConversation(ctx, id)
	if err == nil {
		return domain.ItemConversation, nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		return "", fmt.Errorf("no memory or conversation %s: %w", id, domain.ErrNotFound)
	}
	return "", err
}

func setProject(ctx context.Context, tx *store.Tx, kind domain.ItemKind, id domain.ID, projectID *domain.ID, at time.Time) error {
	if kind == domain.ItemConversation {
		return tx.SetConversationProject(ctx, id, projectID)
	}
	return tx.SetMemoryProject(ctx, id, projectID, at)
}

func setTask(ctx context.Context, tx *store.Tx, kind domain.ItemKind, id domain.ID, taskID *domain.ID, at time.Time) error {
	if kind == domain.ItemConversation {
		return tx.SetConversationTask(ctx, id, taskID)
	}
	return tx.SetMemoryTask(ctx, id, taskID, at)
}
