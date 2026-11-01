package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/OrestisStefanou/Loci/internal/capture"
	"github.com/OrestisStefanou/Loci/internal/config"
	"github.com/OrestisStefanou/Loci/internal/domain"
)

// initOutput is what loci init did.
type initOutput struct {
	// Repo is the repository's root, where both files live.
	Repo string `json:"repo"`
	// Workspace is the .loci.toml naming the store; Settings is Claude
	// Code's local settings file holding the hooks.
	Workspace string `json:"workspace"`
	Settings  string `json:"settings"`
	Store     string `json:"store,omitempty"`
	DB        string `json:"db,omitempty"`
	// Command is what the hooks run.
	Command string `json:"command,omitempty"`
	// Skill is the /loci skill's file, with which the agent changes the
	// session's attachment. On --remove, empty when there was none of loci's.
	Skill string `json:"skill,omitempty"`
	// Mod is the folder of the loci-pick mod, the card above the prompt with
	// which the person attaches the session. On --remove, empty when there
	// was none of loci's.
	Mod string `json:"mod,omitempty"`
	// Replaced is the store the workspace named before, if it existed.
	Replaced string `json:"replaced,omitempty"`
	Removed  bool   `json:"removed,omitempty"`
}

// localSettings is Claude Code's per-person settings file for a repository,
// read from the repository's root.
var localSettings = filepath.Join(".claude", "settings.local.json")

// skillFile is the /loci skill, which Claude Code loads from the repository's
// root like the settings.
var skillFile = filepath.Join(".claude", "skills", "loci", "SKILL.md")

// modDir is the loci-pick mod, which Claude Code loads from the repository's
// skills folder in a trusted repository.
var modDir = filepath.Join(".claude", "skills", "loci-pick")

func (r *root) initCommand() *cobra.Command {
	var remove bool
	cmd := &cobra.Command{
		Use:   "init <store> | init --remove",
		Short: "Capture Claude Code sessions in this repository into a store",
		Long: `Set up the current git repository so its Claude Code sessions are captured
into the named store (one database, such as one per company). At the
repository's root it writes .loci.toml naming the store, and into
.claude/settings.local.json, Claude Code's per-person settings for the
repository, hooks that run loci capture claude-code and rules allowing the
agent to run loci attach and loci ls. It also writes the /loci skill to
.claude/skills/loci, with which the agent attaches the session to a project
and task, and the loci-pick mod to .claude/skills/loci-pick: a card above the
prompt, shown while the session is unattached, with which you search, create
and pick the task or project yourself (/loci-pick opens it again). All are
added to .git/info/exclude, so they are never committed.
Nothing outside the repository is changed, and repositories without it are
not captured. Loci commands run inside the repository use the same store.

Running it again replaces the store, the hooks, the rules, the skill and the mod, for
example after moving the loci binary. --remove takes them and .loci.toml out
again and leaves the store alone.`,
		Example: `  cd ~/code/northwind/web && loci init northwind
  cd ~/code/side-project && loci init personal`,
		Args: rangeArgs(0, 1),
		RunE: func(_ *cobra.Command, args []string) error {
			if remove != (len(args) == 0) {
				return fmt.Errorf("give a store, or --remove without one: %w", domain.ErrInvalid)
			}
			store := ""
			if len(args) == 1 {
				store = args[0]
			}
			out, err := r.initRepo(store, remove)
			if err != nil {
				return err
			}
			return r.render(out, func(w io.Writer) {
				if out.Removed {
					fmt.Fprintf(w, "removed\t%s\n", out.Workspace)
					fmt.Fprintf(w, "removed hooks\t%s\n", out.Settings)
					if out.Skill != "" {
						fmt.Fprintf(w, "removed skill\t%s\n", out.Skill)
					}
					if out.Mod != "" {
						fmt.Fprintf(w, "removed mod\t%s\n", out.Mod)
					}
					fmt.Fprintln(w, "Sessions in this repository are no longer captured. The store is kept.")
					return
				}
				fmt.Fprintf(w, "store\t%s (%s)\n", out.Store, out.DB)
				if out.Replaced != "" && out.Replaced != out.Store {
					fmt.Fprintf(w, "replaced\tstore %s\n", out.Replaced)
				}
				fmt.Fprintf(w, "workspace\t%s\n", out.Workspace)
				fmt.Fprintf(w, "hooks\t%s\n", out.Settings)
				fmt.Fprintf(w, "hooks run\t%s\n", out.Command)
				fmt.Fprintf(w, "skill\t%s\n", out.Skill)
				fmt.Fprintf(w, "mod\t%s\n", out.Mod)
				fmt.Fprintln(w, "New Claude Code sessions in this repository are captured. Undo with: loci init --remove")
			})
		},
	}
	cmd.Flags().BoolVar(&remove, "remove", false, "stop capturing this repository")
	return cmd
}

func (r *root) initRepo(store string, remove bool) (initOutput, error) {
	wd, err := os.Getwd()
	if err != nil {
		return initOutput{}, fmt.Errorf("find working directory: %w", err)
	}
	repo, commonDir := capture.RepoRoot(wd)
	if repo == "" {
		return initOutput{}, fmt.Errorf("%s is not in a git repository; Claude Code reads a repository's hooks from its root, so loci init runs inside one: %w", wd, domain.ErrInvalid)
	}
	out := initOutput{Repo: repo, Workspace: filepath.Join(repo, config.WorkspaceFile),
		Settings: filepath.Join(repo, localSettings), Skill: filepath.Join(repo, skillFile),
		Mod: filepath.Join(repo, modDir), Removed: remove}
	if old, found, err := config.FindWorkspace(repo); err == nil && found && old.File == out.Workspace {
		out.Replaced = old.Store
	}

	if remove {
		if err := editSettings(out.Settings, removeSettings); err != nil {
			return initOutput{}, err
		}
		removedMod, err := removeMod(out.Mod)
		if err != nil {
			return initOutput{}, err
		}
		if !removedMod {
			out.Mod = ""
		}
		removed, err := removeSkill(repo, out.Skill)
		if err != nil {
			return initOutput{}, err
		}
		if !removed {
			out.Skill = ""
		}
		if err := os.Remove(out.Workspace); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return initOutput{}, err
		}
		return out, nil
	}

	cfg, err := r.loadConfig()
	if err != nil {
		return initOutput{}, err
	}
	if out.DB, err = cfg.StoreDB(store); err != nil {
		return initOutput{}, err
	}
	argv, err := r.lociArgv()
	if err != nil {
		return initOutput{}, err
	}
	loci := shellCommand(argv)
	out.Command = loci + " capture claude-code"
	if err := checkSkill(out.Skill); err != nil {
		return initOutput{}, err
	}
	if _, err := checkMod(out.Mod); err != nil {
		return initOutput{}, err
	}
	modFiles, err := capture.Mod(argv)
	if err != nil {
		return initOutput{}, err
	}
	ws, err := config.WriteWorkspace(repo, store)
	if err != nil {
		return initOutput{}, err
	}
	out.Store = ws.Store
	install := func(b []byte) ([]byte, error) {
		b, err := capture.InstallPermissions(b, loci) // finds the old rules through the old hooks
		if err != nil {
			return nil, err
		}
		return capture.InstallHooks(b, out.Command)
	}
	if err := editSettings(out.Settings, install); err != nil {
		return initOutput{}, err
	}
	if err := writeFileAtomic(out.Skill, capture.Skill(loci), 0o644); err != nil {
		return initOutput{}, fmt.Errorf("write the /loci skill: %w", err)
	}
	if err := writeMod(out.Mod, modFiles); err != nil {
		return initOutput{}, fmt.Errorf("write the loci-pick mod: %w", err)
	}
	if err := capture.Exclude(commonDir, "/"+config.WorkspaceFile, "/.claude/settings.local.json", "/.claude/skills/loci/",
		"/.claude/skills/loci-pick/"); err != nil {
		return initOutput{}, fmt.Errorf("add loci's files to .git/info/exclude: %w", err)
	}
	return out, nil
}

// editSettings applies edit to a Claude Code settings file, creating it if
// needed. A file left with nothing in it is deleted, so --remove after init
// leaves no trace when loci created the file.
func editSettings(path string, edit func([]byte) ([]byte, error)) error {
	old, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	updated, err := edit(old)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	// A settings file kept elsewhere is often a symlink: write through it
	// rather than replacing the link with a file.
	target, mode := path, fs.FileMode(0o600)
	if exists {
		if target, err = filepath.EvalSymlinks(path); err != nil {
			return err
		}
		if fi, err := os.Stat(target); err == nil {
			mode = fi.Mode().Perm()
		}
	}
	if bytes.Equal(bytes.TrimSpace(updated), []byte("{}")) {
		if !exists {
			return nil
		}
		return os.Remove(target)
	}
	return writeFileAtomic(target, updated, mode)
}

func removeSettings(b []byte) ([]byte, error) {
	b, err := capture.RemovePermissions(b) // finds the rules through the hooks
	if err != nil {
		return nil, err
	}
	return capture.RemoveHooks(b)
}

// checkSkill refuses to replace a skill file at path that loci did not write.
func checkSkill(path string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !capture.IsSkill(b) {
		return fmt.Errorf("%s is not loci's: move it away, then run loci init again: %w", path, domain.ErrConflict)
	}
	return nil
}

// checkMod reports whether the mod folder exists, and refuses one loci did
// not write.
func checkMod(dir string) (exists bool, err error) {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	b, err := os.ReadFile(filepath.Join(dir, capture.ModLociFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err != nil || !capture.IsMod(b) {
		return true, fmt.Errorf("%s is not loci's: move it away, then run loci init again: %w", dir, domain.ErrConflict)
	}
	return true, nil
}

// writeMod replaces the mod folder with files, so a file a newer loci no
// longer ships does not linger.
func writeMod(dir string, files map[string][]byte) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for rel, b := range files {
		if err := writeFileAtomic(filepath.Join(dir, filepath.FromSlash(rel)), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// removeMod deletes the mod folder if loci wrote it.
func removeMod(dir string) (removed bool, err error) {
	exists, err := checkMod(dir)
	if !exists || errors.Is(err, domain.ErrConflict) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, os.RemoveAll(dir)
}

// removeSkill deletes the skill file if loci wrote it, then the folders init
// created for it and left empty, up to .claude.
func removeSkill(repo, path string) (removed bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err == nil && capture.IsSkill(b) {
		if err := os.Remove(path); err != nil {
			return false, err
		}
		removed = true
	}
	for dir := filepath.Dir(path); dir != repo; dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil { // only succeeds while empty
			break
		}
	}
	return removed, nil
}

// lociArgv is how the hooks, the agent and the mod run loci: this binary by
// absolute path, because Claude Code's PATH may not include where it lives,
// with --db or --store only when given, so normally the workspace file picks
// the store.
func (r *root) lociArgv() ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find the loci binary: %w", err)
	}
	argv := []string{exe}
	if r.dbPath != "" {
		db, err := filepath.Abs(r.dbPath)
		if err != nil {
			return nil, err
		}
		argv = append(argv, "--db", db)
	}
	if r.store != "" {
		argv = append(argv, "--store", r.store)
	}
	return argv, nil
}

// lociCommand is lociArgv as one shell command, for the hooks and the agent.
func (r *root) lociCommand() (string, error) {
	argv, err := r.lociArgv()
	if err != nil {
		return "", err
	}
	return shellCommand(argv), nil
}

func shellCommand(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9/._+-]+$`)

func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// writeFileAtomic replaces path with data, so Claude Code never reads a
// half-written settings file.
func writeFileAtomic(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
