package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/store"
)

// MarkInput is the input of Mark (MCP: loci_mark).
type MarkInput struct {
	// ID of the memory to mark.
	ID string `json:"id" jsonschema:"id of the memory to mark"`
	// Status is inactive to hide the memory from search, or active to
	// restore it.
	Status domain.MemoryStatus `json:"status" jsonschema:"one of inactive (hide a memory that is wrong or outdated) or active (restore it)"`
}

// MarkOutput is the output of Mark.
type MarkOutput struct {
	Memory domain.Memory `json:"memory"`
}

// Mark sets a memory's status. An inactive memory stays stored but leaves
// search results; correction never deletes (design.md section 4). To correct
// a memory, mark it inactive and remember the right statement.
func (a *App) Mark(ctx context.Context, in MarkInput) (MarkOutput, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return MarkOutput{}, fmt.Errorf("id must not be empty: %w", domain.ErrInvalid)
	}
	status := domain.MemoryStatus(strings.ToLower(strings.TrimSpace(string(in.Status))))
	if status != domain.MemoryActive && status != domain.MemoryInactive {
		return MarkOutput{}, fmt.Errorf("status must be %s or %s, got %q: %w",
			domain.MemoryActive, domain.MemoryInactive, in.Status, domain.ErrInvalid)
	}

	var out MarkOutput
	err := a.store.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.SetMemoryStatus(ctx, id, status, a.clock.Now()); err != nil {
			return err
		}
		var err error
		out.Memory, err = tx.GetMemory(ctx, id)
		return err
	})
	return out, err
}
