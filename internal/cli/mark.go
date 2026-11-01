package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
)

func (r *root) markCommand() *cobra.Command {
	var inactive, active bool
	cmd := &cobra.Command{
		Use:   "mark <id> (--inactive | --active)",
		Short: "Hide a memory that is wrong or outdated, or restore it",
		Long: `Mark a memory inactive when it is wrong or outdated. It stays stored but leaves
search results unless --all is given. To correct a memory, mark it inactive and
remember the right statement. --active restores it.`,
		Example: `  loci mark 01J9ZK3Q7V8X2M4N6P0R5T1W3Y --inactive
  loci mark 01J9ZK3Q7V8X2M4N6P0R5T1W3Y --active`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := app.MarkInput{ID: args[0]}
			switch {
			case inactive == active: // both or neither
				return fmt.Errorf("pass exactly one of --inactive or --active: %w", domain.ErrInvalid)
			case inactive:
				in.Status = domain.MemoryInactive
			default:
				in.Status = domain.MemoryActive
			}

			svc, err := r.service(cmd.Context())
			if err != nil {
				return err
			}
			out, err := svc.Mark(cmd.Context(), in)
			if err != nil {
				return err
			}
			return r.render(out, func(w io.Writer) {
				printMemory(w, out.Memory)
			})
		},
	}
	cmd.Flags().BoolVar(&inactive, "inactive", false, "hide the memory from search")
	cmd.Flags().BoolVar(&active, "active", false, "restore the memory")
	return cmd
}
