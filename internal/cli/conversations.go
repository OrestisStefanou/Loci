package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/render"
)

func (r *root) conversationsCommand() *cobra.Command {
	var in app.SearchConversationsInput
	cmd := &cobra.Command{
		Use:   "conversations [--project <slug>] [--task <key>] [--tag <name>]... [--query <text>] [--harness <tool> [--session <id>]]",
		Short: "List or search captured conversations",
		Long: `List captured conversations bounded by project, task, tags and text, most
recently started first. Filters combine. --project includes conversations on
the project's tasks. --query matches the messages as a substring and, when
embeddings are on, by meaning, including those of a session's subagent runs;
each result then shows the message that matched. Only sessions are listed,
each with its count of subagent runs, unless --subagents is given. --harness
with --session finds one session's conversation. Read one with loci show,
and a match in context with loci show <conversation> --around <message>.`,
		Example: `  loci conversations --task EPD-100
  loci conversations --project fees-v2 --query cut-off
  loci conversations --harness claude-code --session 5f1c...`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := r.service(cmd.Context())
			if err != nil {
				return err
			}
			out, err := svc.SearchConversations(cmd.Context(), in)
			if err != nil {
				return err
			}
			if out.SemanticUnavailable {
				fmt.Fprintln(cmd.ErrOrStderr(), "note: semantic ranking unavailable, showing text matches")
			}
			return r.render(out, func(w io.Writer) {
				for i, c := range out.Conversations {
					if i > 0 {
						fmt.Fprintln(w)
					}
					printConversation(w, c.Conversation)
					counts := plural(c.Messages, "message")
					if c.Parent == "" {
						counts += ", " + plural(c.Subagents, "subagent run")
					}
					fmt.Fprintf(w, "  %s\n", counts)
					if c.Opening != "" {
						fmt.Fprintf(w, "  %s\n", c.Opening)
					}
					if m := c.Match; m != nil {
						in := ""
						if m.Conversation != c.ID {
							in = " in " + string(m.Conversation)
						}
						fmt.Fprintf(w, "  match: %s%s \"%s\"\n", m.Message, in, m.Snippet)
					}
				}
			})
		},
	}
	cmd.Flags().StringVar(&in.Project, "project", "", "project slug (includes its tasks' conversations)")
	cmd.Flags().StringVar(&in.Task, "task", "", "task key")
	cmd.Flags().StringArrayVar(&in.Tags, "tag", nil, "tag that must be present; repeatable")
	cmd.Flags().StringVar(&in.Query, "query", "", "text to search for in the messages")
	cmd.Flags().StringVar(&in.Harness, "harness", "", "agent tool, e.g. claude-code")
	cmd.Flags().StringVar(&in.Session, "session", "", "the tool's session id; needs --harness")
	cmd.Flags().BoolVar(&in.IncludeSubagents, "subagents", false, "also list subagent runs")
	cmd.Flags().IntVar(&in.Limit, "limit", 0, "maximum number of conversations (default 50, at most 500)")
	return cmd
}

// printConversation writes one header line: id, start, attachment, and what
// ran it: the repository for a session, the type and session for a subagent
// run.
func printConversation(w io.Writer, c domain.Conversation) {
	ran := c.Repo
	if c.Repo != "" && c.Branch != "" {
		ran += "@" + c.Branch
	}
	if c.Parent != "" {
		agentType := c.AgentType
		if agentType == "" {
			agentType = "subagent"
		}
		ran = agentType + " run in " + c.Parent
	}
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.ID, c.StartedAt.Format("2006-01-02 15:04"),
		render.Attachment(c.Project, c.Task, c.Tags), ran)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
