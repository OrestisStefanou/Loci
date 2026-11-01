package app

import (
	"context"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/store"
)

// SetTaskProjectInput is the input of SetTaskProject (MCP: loci_set_task_project).
type SetTaskProjectInput struct {
	// Task key, e.g. "EPD-100". Created if it does not exist.
	Task string `json:"task" jsonschema:"task key, e.g. EPD-100; created if new"`
	// Project slug. Created if it does not exist. Nil removes the task's project.
	Project *string `json:"project,omitempty" jsonschema:"project slug; created if new; omit to remove the task's project"`
}

// SetTaskProjectOutput is the output of SetTaskProject.
type SetTaskProjectOutput struct {
	Task domain.Task `json:"task"`
}

// SetTaskProject sets, changes or removes a task's project. Both the task and
// the project are created lazily (product.md section 7).
func (a *App) SetTaskProject(ctx context.Context, in SetTaskProjectInput) (SetTaskProjectOutput, error) {
	key, err := domain.NormalizeTaskKey(in.Task)
	if err != nil {
		return SetTaskProjectOutput{}, err
	}
	var slug string
	if in.Project != nil {
		if slug, err = domain.NormalizeProjectSlug(*in.Project); err != nil {
			return SetTaskProjectOutput{}, err
		}
	}

	var out SetTaskProjectOutput
	err = a.store.WithTx(ctx, func(tx *store.Tx) error {
		taskID, now := a.newID()
		task, err := tx.EnsureTask(ctx, domain.Task{ID: taskID, Key: key, CreatedAt: now})
		if err != nil {
			return err
		}
		var projectID *domain.ID
		if in.Project != nil {
			id, now := a.newID()
			p, err := tx.EnsureProject(ctx, domain.Project{ID: id, Slug: slug, CreatedAt: now})
			if err != nil {
				return err
			}
			projectID = &p.ID
		}
		if err := tx.SetTaskProject(ctx, task.ID, projectID); err != nil {
			return err
		}
		out.Task, err = tx.GetTask(ctx, key)
		return err
	})
	return out, err
}
