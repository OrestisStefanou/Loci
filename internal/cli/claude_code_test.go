package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/cli"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

// hook runs loci capture claude-code with event on stdin, as Claude Code does.
func hook(t *testing.T, svc func(context.Context, string) (app.Service, error), db, event string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := cli.Env{Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader(event), Service: svc}
	code := cli.Execute(context.Background(), env, []string{"--db", db, "capture", "claude-code"})
	return stdout.String(), stderr.String(), code
}

// workspace returns a directory opted into capture with a .loci.toml.
func workspace(t *testing.T, store string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".loci.toml"), []byte("store = \""+store+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCaptureClaudeCode(t *testing.T) {
	a := testutil.NewApp(t)
	svc := func(context.Context, string) (app.Service, error) { return a, nil }
	ws := workspace(t, "northwind")
	sub := filepath.Join(ws, "web", "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("session start prints the conversation for the agent's context", func(t *testing.T) {
		out, errOut, code := hook(t, svc, testutil.DBPath(t), `{"hook_event_name":"SessionStart","session_id":"s1","source":"startup","cwd":"`+sub+`"}`)
		if code != 0 || errOut != "" || !strings.HasPrefix(out, "Loci: this session is conversation ") {
			t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
		}
	})

	t.Run("the agent is told to run this binary with the hook's flags", func(t *testing.T) {
		db := testutil.DBPath(t)
		out, _, _ := hook(t, svc, db, `{"hook_event_name":"SessionStart","session_id":"s9","cwd":"`+sub+`"}`)
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		if want := "`" + exe + " --db " + db + " remember "; !strings.Contains(out, want) {
			t.Fatalf("block %q lacks %q", out, want)
		}
	})

	t.Run("a prompt is stored, and the first one of an unattached session says how to attach it", func(t *testing.T) {
		out, errOut, code := hook(t, svc, testutil.DBPath(t), `{"hook_event_name":"UserPromptSubmit","session_id":"s1","prompt_id":"p1","prompt":"hi","cwd":"`+ws+`"}`)
		if code != 0 || !strings.Contains(out, " attach ") || errOut != "" {
			t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
		}
		m, err := a.CaptureMessage(context.Background(), app.CaptureMessageInput{
			CaptureKey: app.CaptureKey{Harness: "claude-code", Session: "s1"}, Role: "assistant", Content: "hello", PromptID: "p1"})
		if err != nil || m.Message.Seq != 2 {
			t.Fatalf("the prompt was not stored first: %+v, %v", m.Message, err)
		}
	})

	t.Run("a directory without a workspace file is not captured", func(t *testing.T) {
		called := false
		spy := func(context.Context, string) (app.Service, error) { called = true; return a, nil }
		db := filepath.Join(t.TempDir(), "loci.db")
		out, errOut, code := hook(t, spy, db, `{"hook_event_name":"SessionStart","session_id":"s2","cwd":"`+t.TempDir()+`"}`)
		if code != 0 || out != "" || errOut != "" || called {
			t.Fatalf("exit %d, stdout %q, stderr %q, service called %v", code, out, errOut, called)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(db), "log")); !os.IsNotExist(err) {
			t.Fatalf("logged a session that is not captured: %v", err)
		}
	})

	t.Run("the workspace names the store", func(t *testing.T) {
		home := isolatedConfig(t)
		var got string
		spy := func(_ context.Context, db string) (app.Service, error) { got = db; return a, nil }
		var stdout, stderr bytes.Buffer
		env := cli.Env{Stdout: &stdout, Stderr: &stderr, Service: spy,
			Stdin: strings.NewReader(`{"hook_event_name":"SessionStart","session_id":"s3","cwd":"` + sub + `"}`)}
		if code := cli.Execute(context.Background(), env, []string{"capture", "claude-code"}); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if want := filepath.Join(home, "northwind.db"); got != want {
			t.Fatalf("database %q, want %q", got, want)
		}
	})

	failing := func(context.Context, string) (app.Service, error) { return nil, errors.New("server unreachable") }
	tests := []struct {
		name, event string
		svc         func(context.Context, string) (app.Service, error)
		logged      string
	}{
		{"invalid JSON", `{"hook_event_name":`, svc, "event=\"\""},
		{"the server cannot be reached", `{"hook_event_name":"Stop","session_id":"s9","last_assistant_message":"secret answer","cwd":"` + ws + `"}`, failing, "session=s9"},
		{"the operation rejects the event", `{"hook_event_name":"UserPromptSubmit","prompt":"secret prompt","cwd":"` + ws + `"}`, svc, "event=UserPromptSubmit"},
		{"the workspace file is invalid", `{"hook_event_name":"UserPromptSubmit","session_id":"s8","prompt":"secret prompt","cwd":"` + workspace(t, "Not A Store") + `"}`, svc, "session=s8"},
	}
	for _, tc := range tests {
		t.Run("never fails the agent when "+tc.name, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "loci.db")
			out, errOut, code := hook(t, tc.svc, db, tc.event)
			if code != 0 || out != "" || errOut != "" {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
			}
			log, err := os.ReadFile(filepath.Join(filepath.Dir(db), "log", "capture.log"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(log), "capture failed") || !strings.Contains(string(log), tc.logged) {
				t.Fatalf("log %q", log)
			}
			if strings.Contains(string(log), "secret") {
				t.Fatalf("content reached the log: %q", log)
			}
		})
	}
}

// gitRepo returns a directory that looks like a git repository's root.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// isolatedConfig points the config at a temporary file whose storage.path is
// <home>/loci.db, and returns home.
func isolatedConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("LOCI_CONFIG", filepath.Join(home, "config.toml"))
	t.Setenv("LOCI_DB", "")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[storage]\npath = \""+filepath.Join(home, "loci.db")+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestInit(t *testing.T) {
	home := isolatedConfig(t)
	repo := gitRepo(t)
	sub := filepath.Join(repo, "internal", "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(repo, ".claude", "settings.local.json")
	skill := filepath.Join(repo, ".claude", "skills", "loci", "SKILL.md")
	mod := filepath.Join(repo, ".claude", "skills", "loci-pick")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var got string
	a := testutil.NewApp(t)
	spy := func(_ context.Context, db string) (app.Service, error) { got = db; return a, nil }
	exec := func(args ...string) (string, int) {
		var stdout, stderr bytes.Buffer
		code := cli.Execute(context.Background(), cli.Env{Stdout: &stdout, Stderr: &stderr, Service: spy}, args)
		return stdout.String() + stderr.String(), code
	}

	t.Chdir(sub)
	out, code := exec("init", "Personal")
	if code != 0 || !strings.Contains(out, filepath.Join(home, "personal.db")) {
		t.Fatalf("init: exit %d, output %q", code, out)
	}
	if ws := read(t, filepath.Join(repo, ".loci.toml")); !strings.Contains(ws, `store = "personal"`) {
		t.Fatalf("workspace file %q: want it at the repository root", ws)
	}
	hooks := read(t, settings)
	if !strings.Contains(hooks, "capture claude-code") || !strings.Contains(hooks, "SessionStart") {
		t.Fatalf("settings %s", hooks)
	}
	for _, rule := range []string{"Bash(" + exe + " attach:*)", "Bash(" + exe + " ls:*)", "Bash(" + exe + " remember:*)"} {
		if !strings.Contains(hooks, rule) {
			t.Fatalf("settings lack the rule %s: %s", rule, hooks)
		}
	}
	if s := read(t, skill); !strings.Contains(s, "name: loci") || !strings.Contains(s, "`"+exe+" attach <id> --task <KEY>`") {
		t.Fatalf("skill %s", s)
	}
	if m := read(t, filepath.Join(mod, "hooks", "loci.ts")); !strings.HasPrefix(m, "// Written by loci init.") ||
		!strings.Contains(m, `export const loci: readonly string[] = ["`+exe+`"]`) {
		t.Fatalf("mod's loci.ts %s", m)
	}
	for _, f := range []string{".claude-plugin/plugin.json", "hooks/hooks.json", "hooks/register.tsx", "types/index.d.ts"} {
		if _, err := os.Stat(filepath.Join(mod, f)); err != nil {
			t.Fatalf("mod file %s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(mod, "tests")); !os.IsNotExist(err) {
		t.Fatalf("the mod's tests were written into the repository: %v", err)
	}
	if ex := read(t, filepath.Join(repo, ".git", "info", "exclude")); ex != "/.loci.toml\n/.claude/settings.local.json\n/.claude/skills/loci/\n/.claude/skills/loci-pick/\n" {
		t.Fatalf("exclude %q", ex)
	}
	if !strings.Contains(out, skill) || !strings.Contains(out, mod) {
		t.Fatalf("init output %q lacks the skill or the mod", out)
	}

	t.Run("commands in the repository use its store", func(t *testing.T) {
		if out, code := exec("ls", "tasks"); code != 0 || got != filepath.Join(home, "personal.db") {
			t.Fatalf("exit %d, output %q, database %q", code, out, got)
		}
	})
	t.Run("--store picks another store", func(t *testing.T) {
		if out, code := exec("--store", "northwind", "ls", "tasks"); code != 0 || got != filepath.Join(home, "northwind.db") {
			t.Fatalf("exit %d, output %q, database %q", code, out, got)
		}
	})
	t.Run("outside a workspace the default database is used", func(t *testing.T) {
		t.Chdir(t.TempDir())
		if out, code := exec("ls", "tasks"); code != 0 || got != filepath.Join(home, "loci.db") {
			t.Fatalf("exit %d, output %q, database %q", code, out, got)
		}
	})
	t.Run("running it again replaces the store and keeps one set of hooks", func(t *testing.T) {
		out, code := exec("init", "other")
		if code != 0 || !strings.Contains(out, "replaced") {
			t.Fatalf("exit %d, output %q", code, out)
		}
		if again := read(t, settings); again != hooks {
			t.Fatalf("hooks changed:\n%s\n%s", hooks, again)
		}
		if ex := read(t, filepath.Join(repo, ".git", "info", "exclude")); strings.Count(ex, ".loci.toml") != 1 {
			t.Fatalf("exclude %q", ex)
		}
	})
	t.Run("--remove leaves no trace of the files loci created", func(t *testing.T) {
		if out, code := exec("init", "--remove"); code != 0 || !strings.Contains(out, "removed") {
			t.Fatalf("exit %d, output %q", code, out)
		}
		for _, f := range []string{settings, skill, mod, filepath.Dir(settings), filepath.Join(repo, ".loci.toml")} {
			if _, err := os.Stat(f); !os.IsNotExist(err) {
				t.Fatalf("%s still exists: %v", f, err)
			}
		}
	})

	tests := []struct {
		name string
		dir  string
		args []string
	}{
		{"outside a git repository", t.TempDir(), []string{"init", "personal"}},
		{"an invalid store name", repo, []string{"init", "not a store"}},
		{"no store", repo, []string{"init"}},
		{"a store with --remove", repo, []string{"init", "x", "--remove"}},
		{"--db and --store together", repo, []string{"--db", "x.db", "--store", "a", "ls", "tasks"}},
	}
	for _, tc := range tests {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			t.Chdir(tc.dir)
			if out, code := exec(tc.args...); code != 2 {
				t.Fatalf("exit %d, output %q", code, out)
			}
		})
	}
}

func TestInitKeepsExistingSettings(t *testing.T) {
	isolatedConfig(t)
	repo := gitRepo(t)
	real := filepath.Join(t.TempDir(), "settings.local.json")
	link := filepath.Join(repo, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{"permissions": {"allow": ["Bash(make check)"]}}`
	if err := os.WriteFile(real, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte("*.log"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	svc := testutil.NewApp(t)

	if _, errOut, code := run(t, svc, "init", "personal"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v", err)
	}
	if got := read(t, real); !strings.Contains(got, "Bash(make check)") || !strings.Contains(got, "capture claude-code") {
		t.Fatalf("settings %s", got)
	}
	if fi, err := os.Stat(real); err != nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode changed: %v", err)
	}
	if ex := read(t, filepath.Join(repo, ".git", "info", "exclude")); ex != "*.log\n/.loci.toml\n/.claude/settings.local.json\n/.claude/skills/loci/\n/.claude/skills/loci-pick/\n" {
		t.Fatalf("exclude %q", ex)
	}

	if _, errOut, code := run(t, svc, "init", "--remove"); code != 0 {
		t.Fatalf("remove: exit %d: %s", code, errOut)
	}
	if got := read(t, real); !strings.Contains(got, "Bash(make check)") || strings.Contains(got, "hooks") || strings.Contains(got, " ls:*") {
		t.Fatalf("after remove %s", got)
	}
}

func TestInitKeepsAnotherLociSkill(t *testing.T) {
	isolatedConfig(t)
	repo := gitRepo(t)
	skill := filepath.Join(repo, ".claude", "skills", "loci", "SKILL.md")
	other := filepath.Join(repo, ".claude", "skills", "other", "SKILL.md")
	mine := "---\nname: loci\n---\nmy own skill\n"
	for _, f := range []string{skill, other} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(mine), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(repo)
	svc := testutil.NewApp(t)

	if _, errOut, code := run(t, svc, "init", "personal"); code != 1 || !strings.Contains(errOut, "not loci's") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := read(t, skill); got != mine {
		t.Fatalf("the skill was replaced: %s", got)
	}
	if _, err := os.Stat(filepath.Join(repo, ".loci.toml")); !os.IsNotExist(err) {
		t.Fatalf("init went ahead: %v", err)
	}

	if out, errOut, code := run(t, svc, "init", "--remove"); code != 0 || strings.Contains(out, "skill") {
		t.Fatalf("remove: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if read(t, skill) != mine || read(t, other) != mine {
		t.Fatal("--remove deleted a skill loci did not write")
	}
}

func TestInitKeepsAnotherLociPickFolder(t *testing.T) {
	isolatedConfig(t)
	repo := gitRepo(t)
	mine := filepath.Join(repo, ".claude", "skills", "loci-pick", "hooks", "loci.ts")
	if err := os.MkdirAll(filepath.Dir(mine), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mine, []byte("export const loci = ['mine']\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	svc := testutil.NewApp(t)

	if _, errOut, code := run(t, svc, "init", "personal"); code != 1 || !strings.Contains(errOut, "not loci's") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(repo, ".loci.toml")); !os.IsNotExist(err) {
		t.Fatalf("init went ahead: %v", err)
	}
	if out, errOut, code := run(t, svc, "init", "--remove"); code != 0 || strings.Contains(out, "mod") {
		t.Fatalf("remove: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if read(t, mine) != "export const loci = ['mine']\n" {
		t.Fatal("--remove deleted a folder loci did not write")
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
