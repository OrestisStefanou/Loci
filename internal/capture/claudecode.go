// Package capture translates agent tools' events into the capture operations
// (design.md section 5). The operations know nothing about any tool; each
// translator maps one tool's events onto them. Claude Code is the first.
package capture

import (
	"context"
	"fmt"
	"strings"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
)

// Harness is the name Claude Code conversations are stored under.
const Harness = "claude-code"

// Event is the part of a Claude Code hook event that capture reads. Claude
// Code passes it as JSON on stdin; unknown fields are ignored.
type Event struct {
	HookEventName        string `json:"hook_event_name"`
	SessionID            string `json:"session_id"`
	PromptID             string `json:"prompt_id"`
	CWD                  string `json:"cwd"`
	Prompt               string `json:"prompt"`
	LastAssistantMessage string `json:"last_assistant_message"`
	AgentID              string `json:"agent_id"`
	AgentType            string `json:"agent_type"`
}

// ClaudeCode stores what ev carries by calling one capture operation, and
// returns the text to print for Claude Code to add to the agent's context:
// the context block at SessionStart, and the attach instruction with the
// first prompt while the session is unattached. Events capture does not use, and answers with no
// text, are ignored. No project or task is passed: Loci infers nothing, and
// the agent attaches the session (design.md section 7). loci is the command
// the agent runs for that, the binary by absolute path with any store flags.
func ClaudeCode(ctx context.Context, svc app.Service, ev Event, loci string) (string, error) {
	key := app.CaptureKey{Harness: Harness, Session: ev.SessionID}
	switch ev.HookEventName {
	case "SessionStart":
		repo, branch := ReadGit(ev.CWD)
		out, err := svc.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: key, Repo: repo, Branch: branch})
		if err != nil {
			return "", err
		}
		return contextBlock(out.Conversation, loci), nil
	case "UserPromptSubmit":
		if strings.TrimSpace(ev.Prompt) == "" {
			return "", nil
		}
		out, err := svc.CaptureMessage(ctx, app.CaptureMessageInput{CaptureKey: key, Role: domain.RoleUser,
			Content: ev.Prompt, PromptID: ev.PromptID})
		if err != nil {
			return "", err
		}
		return attachBlock(out.Conversation, out.Message, loci), nil
	case "Stop":
		return "", message(ctx, svc, app.CaptureMessageInput{CaptureKey: key, Role: domain.RoleAssistant,
			Content: ev.LastAssistantMessage, PromptID: ev.PromptID})
	case "SubagentStart", "SubagentStop":
		// Claude Code's own background helpers, such as the one that
		// suggests the person's next prompt, fire these too, with no agent
		// type. What they produce is not part of the conversation.
		if strings.TrimSpace(ev.AgentType) == "" {
			return "", nil
		}
		return "", subagent(ctx, svc, key, ev)
	default:
		return "", nil
	}
}

// subagent stores a subagent run starting, or its final answer.
func subagent(ctx context.Context, svc app.Service, key app.CaptureKey, ev Event) error {
	key.Agent = ev.AgentID
	if ev.HookEventName == "SubagentStart" {
		_, err := svc.CaptureStart(ctx, app.CaptureStartInput{CaptureKey: key, AgentType: ev.AgentType})
		return err
	}
	return message(ctx, svc, app.CaptureMessageInput{CaptureKey: key, Role: domain.RoleAssistant,
		Content: ev.LastAssistantMessage, PromptID: ev.PromptID, AgentType: ev.AgentType})
}

// message stores in unless its content is blank: a turn can end with no text
// (an interrupted answer), and there is nothing to keep.
func message(ctx context.Context, svc app.Service, in app.CaptureMessageInput) error {
	if strings.TrimSpace(in.Content) == "" {
		return nil
	}
	_, err := svc.CaptureMessage(ctx, in)
	return err
}

// contextBlock tells the agent which Loci conversation it is in and how to
// save memories (design.md section 7).
func contextBlock(c domain.Conversation, loci string) string {
	fill := strings.NewReplacer("{id}", c.ID, "{loci}", loci).Replace
	var at []string
	if c.Project != "" {
		at = append(at, "project "+c.Project)
	}
	if c.Task != "" {
		at = append(at, "task "+c.Task)
	}
	if len(at) > 0 {
		return fmt.Sprintf("Loci: this session is conversation %s, %s.\n", c.ID, strings.Join(at, ", ")) +
			fill(rememberInstruction)
	}
	return fmt.Sprintf("Loci: this session is conversation %s, not attached to a project or task.\n", c.ID) +
		fill(rememberInstruction)
}

// attachBlock tells the agent to attach the session, with its first prompt
// and only if it is still unattached then (design.md section 7). Until then
// the person can attach it from the picker above the prompt, and the agent
// never overwrites that choice.
func attachBlock(c domain.Conversation, first domain.Message, loci string) string {
	if first.Seq != 1 || c.Project != "" || c.Task != "" {
		return ""
	}
	return fmt.Sprintf("Loci: this session, conversation %s, is still not attached to a project or task.\n", c.ID) +
		strings.NewReplacer("{id}", c.ID, "{loci}", loci).Replace(attachInstruction)
}

// attachInstruction tells the agent to attach an unattached session. {loci}
// is the command to run and {id} the conversation.
const attachInstruction = "Before your first reply, work out which task this session is for from the branch, " +
	"the worktree name or the person's first prompt. Find its key with `{loci} ls tasks <query>` and run " +
	"`{loci} attach {id} --task <KEY>`, adding `--project <slug>` only if the task has no project yet " +
	"(`{loci} ls projects` lists them). Begin that reply with one line: " +
	"`Loci: project <slug>, task <KEY>. Say /loci to change.`\n" +
	"If you cannot tell, do not guess: leave it unattached and say so in one line, adding that /loci attaches it.\n"

// rememberInstruction tells the agent what to save as memories and how
// (design.md section 7). Memories saved with --conversation share the
// session's attachment, so the agent passes no project or task.
const rememberInstruction = "Save to Loci what a later session on this work would need and could not get from " +
	"the code, the git history or the ticket: decisions and why, approaches rejected and why, constraints found " +
	"(limits, behaviour of external systems), open questions. One standalone, specific statement per memory " +
	"(names, numbers), readable without this conversation. Save it once it is settled, not as progress notes. " +
	"Never save credentials, secrets, or personal or client data.\n" +
	"Tags are optional topics that cut across projects and tasks, such as customer-support or auth. " +
	"Do not tag the project, the task or the kind of statement. Check `{loci} ls tags` first and reuse an " +
	"existing tag rather than creating a near-duplicate. A tag never names a client or a person.\n" +
	"Run `{loci} remember \"<statement>\" --conversation {id} --author claude-code [--tag <topic>]...` " +
	"and tell the person in one line what you saved.\n"
