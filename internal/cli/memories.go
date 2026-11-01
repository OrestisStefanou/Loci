package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/render"
)

func (r *root) memoriesCommand() *cobra.Command {
	var in app.SearchMemoriesInput
	cmd := &cobra.Command{
		Use:   "memories [--project <slug>] [--task <key>] [--tag <name>]... [--query <text>]",
		Short: "List or search memories",
		Long: `List memories bounded by project, task, tags and text. Filters combine.
--project includes memories on the project's tasks. --task matches that task only.
Without --query the result is most recent first. With it, substring matches are
ranked together with memories close in meaning when embeddings are on.`,
		Example: `  loci memories --task EPD-100
  loci memories --project northwind --tag decided --query cut-off`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := r.service(cmd.Context())
			if err != nil {
				return err
			}
			out, err := svc.SearchMemories(cmd.Context(), in)
			if err != nil {
				return err
			}
			if out.SemanticUnavailable {
				fmt.Fprintln(cmd.ErrOrStderr(), "note: semantic ranking unavailable, showing text matches")
			}
			return r.render(out, func(w io.Writer) {
				for i, m := range out.Memories {
					if i > 0 {
						fmt.Fprintln(w)
					}
					printMemory(w, m)
				}
			})
		},
	}
	cmd.Flags().StringVar(&in.Project, "project", "", "project slug (includes its tasks' memories)")
	cmd.Flags().StringVar(&in.Task, "task", "", "task key")
	cmd.Flags().StringArrayVar(&in.Tags, "tag", nil, "tag that must be present; repeatable")
	cmd.Flags().StringVar(&in.Query, "query", "", "text to search for, by substring and by meaning")
	cmd.Flags().BoolVar(&in.IncludeInactive, "all", false, "also show memories marked inactive")
	cmd.Flags().IntVar(&in.Limit, "limit", 0, "maximum number of memories (default 50, at most 500)")
	return cmd
}

// printMemory writes one memory: a header line, then the content indented.
func printMemory(w io.Writer, m domain.Memory) {
	status := ""
	if m.Status != domain.MemoryActive {
		status = "  (" + string(m.Status) + ")"
	}
	from := ""
	if m.Conversation != "" {
		from = "  from " + m.Conversation
	}
	fmt.Fprintf(w, "%s\t%s\t%s\t%s%s%s\n", m.ID, m.CreatedAt.Format("2006-01-02 15:04"), m.Author,
		render.Attachment(m.Project, m.Task, m.Tags), from, status)
	for _, line := range strings.Split(strings.TrimRight(m.Content, "\n"), "\n") {
		fmt.Fprintf(w, "  %s\n", line)
	}
}
