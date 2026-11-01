package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
)

func (r *root) attachCommand() *cobra.Command {
	var project, task string
	var tags []string
	var noProject, noTask, noTags bool
	cmd := &cobra.Command{
		Use:   "attach <id> [--project <slug> | --no-project] [--task <key> | --no-task] [--tag <name>... | --no-tags]",
		Short: "Change the project, task or tags of a memory or conversation",
		Long: `Change where a memory or conversation is attached. Only what you pass changes.
--tag replaces the item's tags with the ones given. While the item's task has a
project, that project is the one shown and searched, whatever --project says.
A subagent run with neither project nor task of its own shares its session's.`,
		Example: `  loci attach 01J9ZK3Q7V8X2M4N6P0R5T1W3Y --task EPD-100
  loci attach 01J9ZK3Q7V8X2M4N6P0R5T1W3Y --no-project --tag decided --tag auth`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := app.AttachInput{Item: args[0]}
			flags := cmd.Flags()
			var err error
			if in.Project, err = optional(flags.Changed("project"), noProject, project, "--project", "--no-project"); err != nil {
				return err
			}
			if in.Task, err = optional(flags.Changed("task"), noTask, task, "--task", "--no-task"); err != nil {
				return err
			}
			switch setTags := flags.Changed("tag"); {
			case setTags && noTags:
				return fmt.Errorf("pass at most one of --tag or --no-tags: %w", domain.ErrInvalid)
			case setTags:
				in.Tags = &tags
			case noTags:
				in.Tags = &[]string{}
			}
			if in.Project == nil && in.Task == nil && in.Tags == nil {
				return fmt.Errorf("pass at least one of --project, --no-project, --task, --no-task, --tag or --no-tags: %w", domain.ErrInvalid)
			}

			svc, err := r.service(cmd.Context())
			if err != nil {
				return err
			}
			out, err := svc.Attach(cmd.Context(), in)
			if err != nil {
				return err
			}
			return r.render(out, func(w io.Writer) {
				if out.Conversation != nil {
					printConversation(w, *out.Conversation)
					return
				}
				printMemory(w, *out.Memory)
			})
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project slug; created if new")
	cmd.Flags().BoolVar(&noProject, "no-project", false, "remove the item's project")
	cmd.Flags().StringVar(&task, "task", "", "task key; created if new")
	cmd.Flags().BoolVar(&noTask, "no-task", false, "remove the item's task")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "tag; repeatable, replaces the item's tags, created if new")
	cmd.Flags().BoolVar(&noTags, "no-tags", false, "remove all the item's tags")
	return cmd
}

// optional turns a --x / --no-x flag pair into an attach field: nil when
// neither is given, empty to remove, the value to set.
func optional(set, remove bool, value, setFlag, removeFlag string) (*string, error) {
	switch {
	case set && remove:
		return nil, fmt.Errorf("pass at most one of %s or %s: %w", setFlag, removeFlag, domain.ErrInvalid)
	case set:
		return &value, nil
	case remove:
		empty := ""
		return &empty, nil
	}
	return nil, nil
}
