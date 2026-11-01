package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
)

func (r *root) embedCommand() *cobra.Command {
	var backfill bool
	cmd := &cobra.Command{
		Use:   "embed [--backfill]",
		Short: "Embed memories and messages that have no embedding yet",
		Long: `Embed every memory and message that has no embedding from the configured
model, in batches, memories first.
loci serve does this in the background; run it to catch up now, or with
--backfill to embed everything again when the model's output changed but its
name did not (new prefixes, or a newer pull of the same model). A change of
embedding.model needs no backfill: restart the server with loci stop.`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := r.service(cmd.Context())
			if err != nil {
				return err
			}
			in := app.EmbedInput{Backfill: backfill}
			var out app.EmbedOutput
			total := 0
			var failed []domain.ID
			for {
				out, err = svc.Embed(cmd.Context(), in)
				if errors.Is(err, domain.ErrUnavailable) {
					return fmt.Errorf("%w\n%s", err, r.ollamaHint())
				}
				if err != nil {
					return err
				}
				in.Backfill = false
				total += out.Embedded
				failed = append(failed, out.Failed...)
				if !out.Enabled || out.Embedded == 0 || out.Pending == 0 {
					break
				}
				if !r.json {
					fmt.Fprintf(cmd.OutOrStdout(), "embedded %d, %d pending\n", total, out.Pending)
				}
			}
			out.Embedded, out.Failed = total, failed // --json reports the whole run, not the last batch
			return r.render(out, func(w io.Writer) {
				if len(out.Failed) > 0 {
					fmt.Fprintf(w, "set aside %d the provider rejected; they stay searchable by text\n", len(out.Failed))
				}
				switch {
				case !out.Enabled:
					fmt.Fprintln(w, "embedding is off (config embedding.provider)")
				case out.Pending > 0:
					fmt.Fprintf(w, "embedded %d, %d pending (they could not be embedded)\n", out.Embedded, out.Pending)
				default:
					fmt.Fprintf(w, "embedded %d, everything embedded with %s\n", out.Embedded, out.Model)
				}
			})
		},
	}
	cmd.Flags().BoolVar(&backfill, "backfill", false, "clear all embeddings first and embed everything again")
	return cmd
}

// ollamaHint says how to make the embedding provider reachable.
func (r *root) ollamaHint() string {
	model := "nomic-embed-text"
	if cfg, err := r.loadConfig(); err == nil {
		model = cfg.Embedding.Model
	}
	return fmt.Sprintf("start Ollama and run `ollama pull %s`, or set embedding.provider = \"off\" in the config", model)
}
