package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
)

func (r *root) taskCommand() *cobra.Command {
	var project string
	var noProject bool
	cmd := &cobra.Command{
		Use:   "task <key> (--project <slug> | --no-project)",
		Short: "Set, change or remove a task's project",
		Example: `  loci task EPD-100 --project settlements
  loci task EPD-100 --no-project`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := app.SetTaskProjectInput{Task: args[0]}
			setProject := cmd.Flags().Changed("project")
			if setProject == noProject { // both or neither
				return fmt.Errorf("pass exactly one of --project or --no-project: %w", domain.ErrInvalid)
			}
			if setProject {
				in.Project = &project
			}

			svc, err := r.service(cmd.Context())
			if err != nil {
				return err
			}
			out, err := svc.SetTaskProject(cmd.Context(), in)
			if err != nil {
				return err
			}
			return r.render(out, func(w io.Writer) {
				if out.Task.ProjectSlug == "" {
					fmt.Fprintf(w, "%s\t(no project)\n", out.Task.Key)
				} else {
					fmt.Fprintf(w, "%s\t%s\n", out.Task.Key, out.Task.ProjectSlug)
				}
			})
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project slug; created if new")
	cmd.Flags().BoolVar(&noProject, "no-project", false, "remove the task's project")
	return cmd
}
