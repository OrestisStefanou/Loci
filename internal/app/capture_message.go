package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/store"
)

// CaptureMessageInput is the input of CaptureMessage (CLI only: loci capture message).
type CaptureMessageInput struct {
	CaptureKey
	// Role is "user" for a prompt, "assistant" for a final answer.
	Role domain.Role `json:"role" jsonschema:"user for a prompt, assistant for a final answer"`
	// Content is stored exactly as given.
	Content string `json:"content" jsonschema:"the prompt or the final answer"`
	// PromptID is the tool's id for the prompt this message belongs to. A
	// second message with the same prompt id and role replaces the first.
	PromptID string `json:"prompt_id,omitempty" jsonschema:"the tool's id for the prompt; a repeat with the same role replaces the earlier message"`
	// AgentType is the subagent's type, used only if this message has to
	// start a subagent conversation.
	AgentType string `json:"agent_type,omitempty" jsonschema:"the subagent's type, e.g. tech-pm-agent"`
}

// CaptureMessageOutput is the output of CaptureMessage.
type CaptureMessageOutput struct {
	Message      domain.Message      `json:"message"`
	Conversation domain.Conversation `json:"conversation"`
}

// CaptureMessage stores a prompt or a final answer, starting the conversation
// if needed. Who spoke to whom is derived (design.md section 4): in a main
// conversation the person and the main agent, in a subagent run the main
// agent and the subagent. The main agent's handle is the harness name, a
// subagent's is its type.
func (a *App) CaptureMessage(ctx context.Context, in CaptureMessageInput) (CaptureMessageOutput, error) {
	key, err := in.normalize()
	if err != nil {
		return CaptureMessageOutput{}, err
	}
	if in.Role != domain.RoleUser && in.Role != domain.RoleAssistant {
		return CaptureMessageOutput{}, fmt.Errorf("role must be %q or %q, got %q: %w",
			domain.RoleUser, domain.RoleAssistant, in.Role, domain.ErrInvalid)
	}
	if strings.TrimSpace(in.Content) == "" {
		return CaptureMessageOutput{}, fmt.Errorf("content must not be empty: %w", domain.ErrInvalid)
	}
	promptID := strings.TrimSpace(in.PromptID)
	agentType := strings.TrimSpace(in.AgentType)
	if err := checkLen("prompt id", promptID); err != nil {
		return CaptureMessageOutput{}, err
	}
	if err := checkLen("agent type", agentType); err != nil {
		return CaptureMessageOutput{}, err
	}

	a.captureMu.Lock()
	defer a.captureMu.Unlock()
	var out CaptureMessageOutput
	err = a.store.WithTx(ctx, func(tx *store.Tx) error {
		conv, _, err := a.ensureConversation(ctx, tx, key, agentType, attachment{}, provenance{})
		if err != nil {
			return err
		}
		speaker, addressee, err := a.participants(ctx, tx, conv, in.Role)
		if err != nil {
			return err
		}
		id := ""
		if promptID != "" {
			m, err := tx.FindPromptMessage(ctx, conv.ID, promptID, in.Role)
			switch {
			case err == nil:
				id = m.ID
				if err := tx.SetMessageContent(ctx, id, in.Content); err != nil {
					return err
				}
			case !errors.Is(err, domain.ErrNotFound):
				return err
			}
		}
		if id == "" {
			row := store.MessageRow{ConversationID: conv.ID, PromptID: promptID, Role: in.Role,
				SpeakerActorID: &speaker.ID, AddresseeActorID: &addressee.ID, Content: in.Content}
			row.ID, row.CreatedAt = a.newID()
			if err := tx.InsertMessage(ctx, row); err != nil {
				return err
			}
			id = row.ID
		}
		out.Conversation = conv
		out.Message, err = tx.GetMessage(ctx, id)
		return err
	})
	return out, err
}

// participants returns who says a message with role in conv, and to whom.
func (a *App) participants(ctx context.Context, tx *store.Tx, conv domain.Conversation, role domain.Role) (speaker, addressee domain.Actor, err error) {
	main, err := a.ensureActor(ctx, tx, domain.Actor{Kind: domain.ActorAgent, Handle: conv.Harness})
	if err != nil {
		return speaker, addressee, err
	}
	// The person is the one recorded on the conversation, not whoever is in
	// config now, so a changed user.handle never splits one conversation.
	human := conv.Human
	if human == "" {
		human = a.userHandle
	}
	asker := domain.Actor{Kind: domain.ActorHuman, Handle: human}
	answerer := main
	if conv.Agent != "" {
		asker = main
		answerer = domain.Actor{Kind: domain.ActorAgent, Handle: agentHandle(conv.AgentType), AgentType: conv.AgentType}
	}
	if asker.ID == "" {
		if asker, err = a.ensureActor(ctx, tx, asker); err != nil {
			return speaker, addressee, err
		}
	}
	if answerer.ID == "" {
		if answerer, err = a.ensureActor(ctx, tx, answerer); err != nil {
			return speaker, addressee, err
		}
	}
	if role == domain.RoleUser {
		return asker, answerer, nil
	}
	return answerer, asker, nil
}

var notHandle = regexp.MustCompile(`[^a-z0-9._-]+`)

// agentHandle turns a subagent type into an actor handle. Types are not
// always valid handles (a plugin's agent is "plugin:agent"), so other
// characters become '-'. An unknown type is "subagent".
func agentHandle(agentType string) string {
	h := notHandle.ReplaceAllString(strings.ToLower(strings.TrimSpace(agentType)), "-")
	h = strings.TrimLeft(h, "._-")
	if len(h) > 128 {
		h = h[:128]
	}
	if h == "" {
		return "subagent"
	}
	return h
}
