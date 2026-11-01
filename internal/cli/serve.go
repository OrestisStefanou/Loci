package cli

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/spf13/cobra"

	"github.com/OrestisStefanou/Loci/internal/config"
	"github.com/OrestisStefanou/Loci/internal/daemon"
	"github.com/OrestisStefanou/Loci/internal/embed"
	"github.com/OrestisStefanou/Loci/internal/rpc"
	"github.com/OrestisStefanou/Loci/internal/version"
)

func (r *root) serveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the server that owns the database (started automatically when needed)",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := r.paths()
			if err != nil {
				return err
			}
			cfg, err := r.loadConfig()
			if err != nil {
				return err
			}
			err = daemon.Run(cmd.Context(), daemon.Options{
				Paths:             p,
				Version:           version.Get(),
				Logger:            slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil)),
				LockWait:          3 * time.Second,
				UserHandle:        cfg.User.Handle,
				Embedder:          newEmbedder(cfg.Embedding),
				MaxVectorDistance: cfg.Embedding.MaxDistance,
			})
			if errors.Is(err, daemon.ErrAlreadyRunning) {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return nil
			}
			return err
		},
	}
}

// newEmbedder builds the configured embedding provider, or nil when it is off.
// This is the only place outside internal/embed that knows the provider.
func newEmbedder(c config.Embedding) embed.Embedder {
	if c.Provider == config.ProviderOff {
		return nil
	}
	return embed.NewOllama(embed.OllamaConfig{
		URL: c.URL, Model: c.Model, DocumentPrefix: c.DocumentPrefix, QueryPrefix: c.QueryPrefix,
	})
}

func (r *root) statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the server is running",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := r.paths()
			if err != nil {
				return err
			}
			st, err := rpc.NewClient(p.Socket, version.Get(), nil).Status(cmd.Context())
			if err != nil {
				return err
			}
			return r.render(st, func(w io.Writer) {
				fmt.Fprintf(w, "running\tpid %d\tversion %s\tsince %s\n", st.PID, st.Version, st.StartedAt.Format(time.RFC3339))
				fmt.Fprintf(w, "db\t%s\n", st.DB)
				printEmbeddingStatus(w, st.Embedding)
				fmt.Fprintf(w, "log\t%s\n", p.Log)
			})
		},
	}
}

func printEmbeddingStatus(w io.Writer, e rpc.EmbeddingStatus) {
	if e.State == "off" || e.State == "" {
		fmt.Fprintln(w, "embedding\toff")
		return
	}
	failed := ""
	if e.Failed > 0 {
		failed = fmt.Sprintf(", %d failed", e.Failed)
	}
	fmt.Fprintf(w, "embedding\t%s\t%s\t%d pending%s\n", e.State, e.Model, e.Pending, failed)
	if e.Error != "" {
		fmt.Fprintf(w, "\t%s\n", e.Error)
	}
}

func (r *root) stopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the server",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := r.paths()
			if err != nil {
				return err
			}
			err = rpc.NewClient(p.Socket, version.Get(), nil).Stop(cmd.Context())
			if errors.Is(err, rpc.ErrServerNotRunning) {
				fmt.Fprintln(cmd.OutOrStdout(), "not running")
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "stopped")
			return nil
		},
	}
}
