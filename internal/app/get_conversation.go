package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/store"
)

// Window bounds for GetConversation.
const (
	defaultWindow = 5
	maxWindow     = 500
)

// GetConversationInput is the input of GetConversation (MCP: loci_get_conversation).
type GetConversationInput struct {
	// ID is the conversation's id.
	ID string `json:"id" jsonschema:"id of the conversation"`
	// AroundMessage, when set, returns only the messages within Window of
	// this one, e.g. to read a search hit in context.
	AroundMessage string `json:"around_message,omitempty" jsonschema:"id of a message in the conversation; only the messages around it are returned"`
	// Window is how many messages either side of AroundMessage to return;
	// 0 means 5, at most 500. Only with AroundMessage.
	Window int `json:"window,omitempty" jsonschema:"messages either side of around_message, default 5, at most 500"`
}

// GetConversationOutput is the output of GetConversation.
type GetConversationOutput struct {
	// Conversation carries the total message count, which is more than
	// len(Messages) when a window was asked for.
	Conversation domain.ConversationSummary `json:"conversation"`
	Messages     []domain.Message           `json:"messages"`
	// Subagents are the runs the session started, most recent first. Empty
	// for a subagent run, whose Conversation.Parent names its session.
	Subagents []domain.ConversationSummary `json:"subagents"`
}

// GetConversation returns one conversation with its messages in order, all
// of them or a window around one, and the subagent runs it started.
func (a *App) GetConversation(ctx context.Context, in GetConversationInput) (GetConversationOutput, error) {
	id := strings.TrimSpace(in.ID)
	around := strings.TrimSpace(in.AroundMessage)
	switch {
	case id == "":
		return GetConversationOutput{}, fmt.Errorf("id must not be empty: %w", domain.ErrInvalid)
	case in.Window < 0:
		return GetConversationOutput{}, fmt.Errorf("window must not be negative: %w", domain.ErrInvalid)
	case in.Window > 0 && around == "":
		return GetConversationOutput{}, fmt.Errorf("window needs around_message: %w", domain.ErrInvalid)
	}

	out := GetConversationOutput{}
	err := a.store.WithTx(ctx, func(tx *store.Tx) error {
		found, err := tx.ListConversations(ctx, store.ConversationFilter{ID: &id})
		if err != nil {
			return err
		}
		if len(found) == 0 {
			return fmt.Errorf("conversation %s: %w", id, domain.ErrNotFound)
		}
		shorten(found, "")
		out.Conversation = found[0]

		first, last := 0, 0
		if around != "" {
			m, err := tx.GetMessage(ctx, around)
			if err != nil {
				return err
			}
			if m.ConversationID != id {
				return fmt.Errorf("message %s is not in conversation %s: %w", around, id, domain.ErrInvalid)
			}
			window := in.Window
			if window == 0 {
				window = defaultWindow
			}
			window = min(window, maxWindow)
			first, last = max(m.Seq-window, 1), m.Seq+window
		}
		if out.Messages, err = tx.ListMessages(ctx, id, first, last); err != nil {
			return err
		}
		out.Subagents, err = tx.ListConversations(ctx, store.ConversationFilter{ParentID: &id})
		shorten(out.Subagents, "")
		return err
	})
	if err != nil {
		return GetConversationOutput{}, err
	}
	return out, nil
}
