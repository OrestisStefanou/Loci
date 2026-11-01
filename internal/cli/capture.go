package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/capture"
	"github.com/OrestisStefanou/Loci/internal/config"
	"github.com/OrestisStefanou/Loci/internal/daemon"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/render"
)

func (r *root) captureCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Record a conversation from an agent tool",
		Long: `Record conversations as they happen. A conversation is identified by the agent
tool (--harness), its session id (--session) and, for a subagent run inside the
session, the subagent's id (--agent). start and message are for tools without
a built-in translator; they exit non-zero on errors. claude-code is the
translator Claude Code runs (set up by loci init).`,
	}
	cmd.AddCommand(r.captureStartCommand(), r.captureMessageCommand(), r.captureClaudeCodeCommand())
	return cmd
}

// captureKeyFlags adds the flags that identify a conversation.
func captureKeyFlags(cmd *cobra.Command, k *app.CaptureKey) {
	cmd.Flags().StringVar(&k.Harness, "harness", "", "agent tool, e.g. claude-code (required)")
	cmd.Flags().StringVar(&k.Session, "session", "", "the tool's session id (required)")
	cmd.Flags().StringVar(&k.Agent, "agent", "", "the tool's id for a subagent run; omit for the main conversation")
}

func (r *root) captureStartCommand() *cobra.Command {
	var in app.CaptureStartInput
	cmd := &cobra.Command{
		Use:   "start --harness <tool> --session <id> [--project <slug>] [--task <key>] [--agent <id> --agent-type <type>] [--repo <url>] [--branch <name>]",
		Short: "Start or resume a conversation",
		Long: `Start a conversation, or resume it if it exists. It is attached to --project
and --task when given, as long as it has neither yet; nothing is inferred. One
that already has a project or task keeps it. A subagent run (--agent) belongs to
the session's main conversation and shares its attachment. --repo and --branch
record where it started; a local path is not stored.`,
		Example: `  loci capture start --harness my-tool --session 42 --task EPD-100 \
    --repo "$(git remote get-url origin)" --branch "$(git branch --show-current)"`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := r.service(cmd.Context())
			if err != nil {
				return err
			}
			out, err := svc.CaptureStart(cmd.Context(), in)
			if err != nil {
				return err
			}
			state := "resumed"
			if out.Created {
				state = "started"
			}
			return r.render(out, func(w io.Writer) {
				c := out.Conversation
				fmt.Fprintf(w, "%s\t%s\t%s\n", c.ID, render.Attachment(c.Project, c.Task, c.Tags), state)
			})
		},
	}
	captureKeyFlags(cmd, &in.CaptureKey)
	cmd.Flags().StringVar(&in.Project, "project", "", "project slug for a new conversation; created if new")
	cmd.Flags().StringVar(&in.Task, "task", "", "task key for a new conversation; created if new")
	cmd.Flags().StringVar(&in.AgentType, "agent-type", "", "the subagent's type, e.g. tech-pm-agent")
	cmd.Flags().StringVar(&in.Repo, "repo", "", "repository origin URL")
	cmd.Flags().StringVar(&in.Branch, "branch", "", "git branch")
	return cmd
}

func (r *root) captureMessageCommand() *cobra.Command {
	var in app.CaptureMessageInput
	var role string
	cmd := &cobra.Command{
		Use:   "message <content> --harness <tool> --session <id> --role (user|assistant) [--prompt-id <id>] [--agent <id>]",
		Short: "Add a prompt or a final answer to a conversation",
		Long: `Add a message, starting the conversation if needed. Store prompts (--role user)
and final answers (--role assistant), not tool calls or results. A repeat with
the same --prompt-id and role replaces the earlier message. Pass - as the
content to read it from stdin.`,
		Example: `  loci capture message "What is the settlement cut-off?" --harness my-tool --session 42 --role user --prompt-id p1
  echo "16:00 CET" | loci capture message - --harness my-tool --session 42 --role assistant --prompt-id p1`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in.Role = domain.Role(role)
			in.Content = args[0]
			if in.Content == "-" {
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("read content from stdin: %w", err)
				}
				in.Content = string(b)
			}
			svc, err := r.service(cmd.Context())
			if err != nil {
				return err
			}
			out, err := svc.CaptureMessage(cmd.Context(), in)
			if err != nil {
				return err
			}
			return r.render(out, func(w io.Writer) {
				m := out.Message
				fmt.Fprintf(w, "%s\t#%d\t%s\t%s -> %s\n", m.ConversationID, m.Seq, m.Role, m.Speaker, m.Addressee)
			})
		},
	}
	captureKeyFlags(cmd, &in.CaptureKey)
	cmd.Flags().StringVar(&role, "role", "", "user for a prompt, assistant for a final answer (required)")
	cmd.Flags().StringVar(&in.PromptID, "prompt-id", "", "the tool's id for the prompt this message belongs to")
	cmd.Flags().StringVar(&in.AgentType, "agent-type", "", "the subagent's type, if this message starts its run")
	return cmd
}

// captureTimeout bounds one translator run, including starting loci serve,
// so capture never holds up the agent for long.
const captureTimeout = 3 * time.Second

// maxEventSize bounds what the translator reads from stdin.
const maxEventSize = 16 << 20

func (r *root) captureClaudeCodeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "claude-code",
		Short: "Store one Claude Code hook event read from stdin",
		Long: `Read one Claude Code hook event as JSON on stdin and store what it carries: the
person's prompt, the agent's final answer, a session or subagent starting. On
SessionStart it prints the Loci conversation id, which Claude Code adds to the
agent's context. Claude Code runs it from the hooks loci init writes.

Only sessions working in a directory with a .loci.toml at or above it (see
loci init) are captured, into the store that file names; others are ignored.

It never fails the agent: it gives up after 3s, always exits 0, and logs what
it could not store to capture.log in the log directory next to the database.`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			r.captureClaudeCode(cmd.Context(), cmd.InOrStdin())
			return nil
		},
	}
}

func (r *root) captureClaudeCode(ctx context.Context, stdin io.Reader) {
	ctx, cancel := context.WithTimeout(ctx, captureTimeout)
	defer cancel()
	var ev capture.Event
	db := ""
	err := json.NewDecoder(io.LimitReader(stdin, maxEventSize)).Decode(&ev)
	if err == nil {
		var captured bool
		if db, captured, err = r.captureDB(ev.CWD); err == nil && !captured {
			return
		}
	}
	if err == nil {
		var svc app.Service
		if svc, err = r.serviceFor(ctx, db); err == nil {
			var block string
			if block, err = capture.ClaudeCode(ctx, svc, ev, r.agentCommand()); err == nil {
				_, err = fmt.Fprint(r.env.Stdout, block)
			}
		}
	}
	if err != nil {
		r.logCaptureError(db, ev, err)
	}
}

// agentCommand is the command the context block tells the agent to run:
// the same binary and flags as the hook, which init wrote from lociCommand.
func (r *root) agentCommand() string {
	loci, err := r.lociCommand()
	if err != nil {
		return "loci"
	}
	return loci
}

// captureDB returns the database for a session working in dir, and false
// when no workspace file opts dir into capture.
func (r *root) captureDB(dir string) (db string, captured bool, err error) {
	if dir == "" {
		if dir, err = os.Getwd(); err != nil {
			return "", false, err
		}
	}
	if _, found, err := config.FindWorkspace(dir); err != nil || !found {
		return "", false, err
	}
	db, err = r.dbPathFor(dir)
	return db, err == nil, err
}

// logCaptureError records a turn that was not stored, next to db or, when
// it is not known, the database of the working directory. It logs which
// session and event, never the content.
func (r *root) logCaptureError(db string, ev capture.Event, err error) {
	if db == "" {
		var perr error
		if db, perr = r.resolveDBPath(); perr != nil {
			return // no database path, so nowhere to log
		}
	}
	dir, perr := daemon.LogDir(db)
	if perr != nil || os.MkdirAll(dir, 0o700) != nil {
		return
	}
	f, ferr := os.OpenFile(filepath.Join(dir, "capture.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if ferr != nil {
		return
	}
	defer func() { _ = f.Close() }()
	slog.New(slog.NewTextHandler(f, nil)).Error("capture failed", "harness", capture.Harness,
		"event", ev.HookEventName, "session", ev.SessionID, "agent", ev.AgentID, "err", err)
}
