package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/OrestisStefanou/Loci/internal/app"
)

func (r *root) rememberCommand() *cobra.Command {
	var in app.RememberInput
	cmd := &cobra.Command{
		Use:   "remember <content> [--project <slug>] [--task <key>] [--tag <name>]... [--conversation <id>]",
		Short: "Save a memory",
		Long: `Save a memory, optionally attached to a project, a task and tags. Tags are
topics that cut across projects and tasks, such as customer-support.
--conversation records the conversation the memory comes from; a memory with
no project and no task of its own shares that conversation's, including when
the conversation is attached later. Pass - as the content to read it from
stdin.`,
		Example: `  loci remember "Settlement cut-off is 16:00 CET" --task EPD-100 --tag settlement
  loci remember "Refunds over 500 need a second approver" --conversation 01J9ZK3Q7V8X2M4N6P0R5T1W3Y
  git log -1 --format=%B | loci remember - --project northwind`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			out, err := svc.Remember(cmd.Context(), in)
			if err != nil {
				return err
			}
			return r.render(out, func(w io.Writer) {
				printMemory(w, out.Memory)
			})
		},
	}
	cmd.Flags().StringVar(&in.Project, "project", "", "project slug; created if new")
	cmd.Flags().StringVar(&in.Task, "task", "", "task key; created if new")
	cmd.Flags().StringArrayVar(&in.Tags, "tag", nil, "tag; repeatable, created if new")
	cmd.Flags().StringVar(&in.Author, "author", "", "agent handle saving the memory (default: you)")
	cmd.Flags().StringVar(&in.Conversation, "conversation", "", "id of the conversation the memory comes from")
	return cmd
}
