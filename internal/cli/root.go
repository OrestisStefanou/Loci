// Package cli is the command-line adapter. Each command parses its arguments
// into an app operation's Input, calls the operation through app.Service, and
// renders the Output as text or, with --json, as the Output struct itself.
// Commands contain no business logic and never import the store.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/config"
	"github.com/OrestisStefanou/Loci/internal/daemon"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/rpc"
	"github.com/OrestisStefanou/Loci/internal/version"
)

// Exit codes.
const (
	exitOK      = 0
	exitError   = 1
	exitInvalid = 2 // bad arguments or domain.ErrInvalid
)

// Env is what the CLI needs from its process.
type Env struct {
	Stdout, Stderr io.Writer
	// Stdin is read by commands that accept "-" as content. Nil means none.
	Stdin io.Reader
	// Service overrides how commands reach the operations; tests pass an
	// in-process *app.App. Nil means connect to loci serve for the database,
	// starting it if needed.
	Service func(ctx context.Context, dbPath string) (app.Service, error)
}

// root holds state shared by all commands.
type root struct {
	env    Env
	dbPath string
	store  string
	json   bool
}

// Execute runs the loci CLI with args (without the program name) and returns
// the process exit code.
func Execute(ctx context.Context, env Env, args []string) int {
	r := &root{env: env}
	cmd := r.command()
	cmd.SetArgs(args)
	cmd.SetOut(env.Stdout)
	cmd.SetErr(env.Stderr)
	if env.Stdin != nil {
		cmd.SetIn(env.Stdin)
	}
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(env.Stderr, "loci:", err)
		if errors.Is(err, domain.ErrInvalid) {
			return exitInvalid
		}
		return exitError
	}
	return exitOK
}

func (r *root) command() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "loci",
		Short:         "Loci stores agent conversations and memories by the work they belong to",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.PersistentFlags().StringVar(&r.dbPath, "db", "", "database file (default: --store, $LOCI_DB, the workspace's store, then storage.path in config)")
	cmd.PersistentFlags().StringVar(&r.store, "store", "", "use the named store, <store>.db next to storage.path")
	cmd.PersistentFlags().BoolVar(&r.json, "json", false, "print machine-readable JSON")
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return fmt.Errorf("%w: %w", err, domain.ErrInvalid)
	})

	cmd.AddCommand(
		r.taskCommand(),
		r.lsCommand(),
		r.rememberCommand(),
		r.memoriesCommand(),
		r.markCommand(),
		r.attachCommand(),
		r.conversationsCommand(),
		r.showCommand(),
		r.captureCommand(),
		r.initCommand(),
		r.embedCommand(),
		r.serveCommand(),
		r.statusCommand(),
		r.stopCommand(),
	)
	return cmd
}

// service returns the operations for the selected database. Commands call it
// in RunE so that --help and argument errors never start a server.
func (r *root) service(ctx context.Context) (app.Service, error) {
	db, err := r.resolveDBPath()
	if err != nil {
		return nil, err
	}
	return r.serviceFor(ctx, db)
}

// serviceFor returns the operations for database db.
func (r *root) serviceFor(ctx context.Context, db string) (app.Service, error) {
	if r.env.Service != nil {
		return r.env.Service(ctx, db)
	}
	p, err := daemon.PathsFor(db)
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find loci binary to start the server: %w", err)
	}
	return rpc.NewClient(p.Socket, version.Get(), daemon.Starter(exe, p)), nil
}

// paths returns the server files for the selected database.
func (r *root) paths() (daemon.Paths, error) {
	db, err := r.resolveDBPath()
	if err != nil {
		return daemon.Paths{}, err
	}
	return daemon.PathsFor(db)
}

// resolveDBPath selects the database for the working directory.
func (r *root) resolveDBPath() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("find working directory: %w", err)
	}
	return r.dbPathFor(wd)
}

// dbPathFor selects the database for work in dir: --db, --store, $LOCI_DB,
// the store of the workspace file above dir, then config storage.path.
func (r *root) dbPathFor(dir string) (string, error) {
	if r.dbPath != "" && r.store != "" {
		return "", fmt.Errorf("--db and --store both name a database; give one: %w", domain.ErrInvalid)
	}
	if r.dbPath != "" {
		return r.dbPath, nil
	}
	cfg, err := r.loadConfig()
	if err != nil {
		return "", err
	}
	if r.store != "" {
		return cfg.StoreDB(r.store)
	}
	if p := os.Getenv("LOCI_DB"); p != "" {
		return p, nil
	}
	ws, found, err := config.FindWorkspace(dir)
	if err != nil {
		return "", err
	}
	if found {
		return cfg.StoreDB(ws.Store)
	}
	return cfg.Storage.Path, nil
}

// loadConfig reads $LOCI_CONFIG, or the default config file.
func (r *root) loadConfig() (config.Config, error) {
	cfgPath := os.Getenv("LOCI_CONFIG")
	if cfgPath == "" {
		cfgPath = config.DefaultPath()
	}
	return config.Load(cfgPath)
}

// exactArgs is cobra.ExactArgs with the error marked invalid, so it exits 2.
func exactArgs(n int) cobra.PositionalArgs {
	return wrapInvalid(cobra.ExactArgs(n))
}

func rangeArgs(minArgs, maxArgs int) cobra.PositionalArgs {
	return wrapInvalid(cobra.RangeArgs(minArgs, maxArgs))
}

func wrapInvalid(check cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := check(cmd, args); err != nil {
			return fmt.Errorf("%w: %w", err, domain.ErrInvalid)
		}
		return nil
	}
}
