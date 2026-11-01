package cli

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/render"
)

func (r *root) showCommand() *cobra.Command {
	var in app.GetConversationInput
	cmd := &cobra.Command{
		Use:   "show <conversation-id> [--around <message-id> [--window <n>]]",
		Short: "Print a captured conversation as Markdown",
		Long: `Print a conversation's messages in order as Markdown, then the subagent runs
it started; show a run by its id. --around prints only the messages within
--window (default 5) of one message.`,
		Example: `  loci show 01J9ZK3Q7V8X2M4N6P0R5T1W3Y
  loci show 01J9ZK3Q7V8X2M4N6P0R5T1W3Y --around 01J9ZK4B2C3D4E5F6G7H8J9K0M --window 2`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in.ID = args[0]
			svc, err := r.service(cmd.Context())
			if err != nil {
				return err
			}
			out, err := svc.GetConversation(cmd.Context(), in)
			if err != nil {
				return err
			}
			return r.renderDocument(out, func(w io.Writer) error {
				return render.Conversation(w, out.Conversation, out.Messages, out.Subagents)
			})
		},
	}
	cmd.Flags().StringVar(&in.AroundMessage, "around", "", "message id; print only the messages around it")
	cmd.Flags().IntVar(&in.Window, "window", 0, "messages either side of --around (default 5, at most 500)")
	return cmd
}
