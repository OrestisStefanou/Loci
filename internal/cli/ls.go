package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
)

func (r *root) lsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <projects|tasks|tags> [query]",
		Short: "List or search projects, tasks or tags",
		Long: `List projects, tasks or tags whose name contains query, all without one.
Projects and tags are listed by name. Tasks are listed most recently used
first (a conversation or memory on the task, or its creation), then by key.`,
		Example:   "  loci ls tasks epd",
		Args:      rangeArgs(1, 2),
		ValidArgs: []string{string(domain.KindProject), string(domain.KindTask), string(domain.KindTag)},
		RunE: func(cmd *cobra.Command, args []string) error {
			in := app.ListInput{Kind: domain.Kind(args[0])}
			if len(args) == 2 {
				in.Query = args[1]
			}

			svc, err := r.service(cmd.Context())
			if err != nil {
				return err
			}
			out, err := svc.List(cmd.Context(), in)
			if err != nil {
				return err
			}
			return r.render(out, func(w io.Writer) {
				for _, item := range out.Items {
					if item.Project != "" {
						fmt.Fprintf(w, "%s\t%s\n", item.Name, item.Project)
					} else {
						fmt.Fprintln(w, item.Name)
					}
				}
			})
		},
	}
}
