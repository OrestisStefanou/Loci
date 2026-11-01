package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/store"
)

// RememberInput is the input of Remember (MCP: loci_remember).
type RememberInput struct {
	// Content is the statement worth keeping.
	Content string `json:"content" jsonschema:"the statement to keep, standalone and specific"`
	// Project slug. Created if it does not exist. Empty means none.
	Project string `json:"project,omitempty" jsonschema:"project slug; created if new"`
	// Task key. Created if it does not exist. Empty means none. The task's
	// own project is not changed; use SetTaskProject for that.
	Task string `json:"task,omitempty" jsonschema:"task key, e.g. EPD-100; created if new"`
	// Tags, any number. Created if they do not exist. Tags are topics that cut
	// across projects and tasks (customer-support, auth), not the kind of
	// statement, which the content carries (design.md section 13, point 6).
	Tags []string `json:"tags,omitempty" jsonschema:"topics that cut across projects and tasks, e.g. customer-support; reuse existing ones; created if new"`
	// Conversation is the id of the conversation the memory is saved in. A
	// memory with no project and no task of its own shares the conversation's
	// attachment, so it follows when the conversation is attached later.
	Conversation string `json:"conversation,omitempty" jsonschema:"id of the conversation this memory comes from (the Loci conversation id of this session)"`
	// Author is the agent handle saving the memory. Empty means the
	// configured person.
	Author string `json:"author,omitempty" jsonschema:"agent handle saving this, e.g. claude; omit when a person saves it"`
}

// RememberOutput is the output of Remember.
type RememberOutput struct {
	Memory domain.Memory `json:"memory"`
}

// Remember saves a memory, creating its project, task, tags and author lazily
// (product.md section 7). Nothing is required beyond the content. A
// conversation given must exist.
func (a *App) Remember(ctx context.Context, in RememberInput) (RememberOutput, error) {
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return RememberOutput{}, fmt.Errorf("content must not be empty: %w", domain.ErrInvalid)
	}
	var slug, key string
	var err error
	if in.Project != "" {
		if slug, err = domain.NormalizeProjectSlug(in.Project); err != nil {
			return RememberOutput{}, err
		}
	}
	if in.Task != "" {
		if key, err = domain.NormalizeTaskKey(in.Task); err != nil {
			return RememberOutput{}, err
		}
	}
	tags, err := normalizeTags(in.Tags)
	if err != nil {
		return RememberOutput{}, err
	}
	author := domain.Actor{Kind: domain.ActorHuman, Handle: a.userHandle}
	if in.Author != "" {
		author.Kind = domain.ActorAgent
		if author.Handle, err = domain.NormalizeHandle(in.Author); err != nil {
			return RememberOutput{}, err
		}
	}

	var out RememberOutput
	err = a.store.WithTx(ctx, func(tx *store.Tx) error {
		row := store.MemoryRow{Content: content}
		row.ID, row.CreatedAt = a.newID()
		if conv := strings.TrimSpace(in.Conversation); conv != "" {
			c, err := tx.GetConversation(ctx, conv)
			if err != nil {
				return err
			}
			row.ConversationID = &c.ID
		}
		if slug != "" {
			id, now := a.newID()
			p, err := tx.EnsureProject(ctx, domain.Project{ID: id, Slug: slug, CreatedAt: now})
			if err != nil {
				return err
			}
			row.ProjectID = &p.ID
		}
		if key != "" {
			id, now := a.newID()
			t, err := tx.EnsureTask(ctx, domain.Task{ID: id, Key: key, CreatedAt: now})
			if err != nil {
				return err
			}
			row.TaskID = &t.ID
		}
		author.ID, _ = a.newID()
		actor, err := tx.EnsureActor(ctx, author)
		if err != nil {
			return err
		}
		row.AuthorActorID = &actor.ID
		tagIDs := make([]domain.ID, 0, len(tags))
		for _, name := range tags {
			id, _ := a.newID()
			t, err := tx.EnsureTag(ctx, domain.Tag{ID: id, Name: name})
			if err != nil {
				return err
			}
			tagIDs = append(tagIDs, t.ID)
		}
		if err := tx.InsertMemory(ctx, row); err != nil {
			return err
		}
		if err := tx.AddItemTags(ctx, domain.ItemMemory, row.ID, tagIDs); err != nil {
			return err
		}
		out.Memory, err = tx.GetMemory(ctx, row.ID)
		return err
	})
	return out, err
}

// normalizeTags normalises each tag and drops duplicates, keeping order.
func normalizeTags(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		name, err := domain.NormalizeTag(raw)
		if err != nil {
			return nil, err
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out, nil
}
