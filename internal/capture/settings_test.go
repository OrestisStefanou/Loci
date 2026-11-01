package capture_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/capture"
)

const cmd = "/usr/local/bin/loci capture claude-code"

// handlers returns the commands of every handler on event, in order.
func handlers(t *testing.T, settings []byte, event string) []string {
	t.Helper()
	var s struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(settings, &s); err != nil {
		t.Fatalf("invalid JSON %s: %v", settings, err)
	}
	var out []string
	for _, g := range s.Hooks[event] {
		for _, h := range g.Hooks {
			out = append(out, h.Command)
		}
	}
	return out
}

func TestInstallHooks(t *testing.T) {
	t.Run("into an empty or missing file", func(t *testing.T) {
		for _, in := range []string{"", "{}", "  \n"} {
			got, err := capture.InstallHooks([]byte(in), cmd)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range capture.HookEvents {
				if h := handlers(t, got, e); len(h) != 1 || h[0] != cmd {
					t.Fatalf("%s: %v in %s", e, h, got)
				}
			}
			if !strings.Contains(string(got), `"timeout": 10`) || strings.Contains(string(got), "matcher") {
				t.Fatalf("got %s", got)
			}
		}
	})

	t.Run("keeps other settings, their order and other hooks", func(t *testing.T) {
		in := `{"model": "opus", "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "notify && say done"}]}],
			"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "check"}]}]}, "env": {"A": "<b>"}}`
		got, err := capture.InstallHooks([]byte(in), cmd)
		if err != nil {
			t.Fatal(err)
		}
		s := string(got)
		if strings.Index(s, `"model"`) > strings.Index(s, `"hooks"`) || strings.Index(s, `"hooks"`) > strings.Index(s, `"env"`) {
			t.Fatalf("key order changed: %s", s)
		}
		if !strings.Contains(s, "notify && say done") || !strings.Contains(s, `"<b>"`) {
			t.Fatalf("values were rewritten: %s", s)
		}
		if h := handlers(t, got, "Stop"); len(h) != 2 || h[0] != "notify && say done" || h[1] != cmd {
			t.Fatalf("Stop: %v", h)
		}
		if h := handlers(t, got, "PreToolUse"); len(h) != 1 || h[0] != "check" {
			t.Fatalf("PreToolUse: %v", h)
		}
	})

	t.Run("installing twice is the same as once", func(t *testing.T) {
		once, err := capture.InstallHooks([]byte(`{"model": "opus"}`), cmd)
		if err != nil {
			t.Fatal(err)
		}
		twice, err := capture.InstallHooks(once, cmd)
		if err != nil {
			t.Fatal(err)
		}
		if string(once) != string(twice) {
			t.Fatalf("once:\n%s\ntwice:\n%s", once, twice)
		}
	})

	t.Run("replaces an entry from a binary elsewhere", func(t *testing.T) {
		old, err := capture.InstallHooks(nil, "/old/place/loci --db /tmp/x.db capture claude-code")
		if err != nil {
			t.Fatal(err)
		}
		got, err := capture.InstallHooks(old, cmd)
		if err != nil {
			t.Fatal(err)
		}
		if h := handlers(t, got, "SessionStart"); len(h) != 1 || h[0] != cmd {
			t.Fatalf("got %v", h)
		}
	})

	t.Run("invalid settings are an error", func(t *testing.T) {
		for _, in := range []string{`{"model":`, `[]`, `{"hooks": []}`, `{"hooks": {"Stop": {}}}`, `{} {}`} {
			if _, err := capture.InstallHooks([]byte(in), cmd); err == nil {
				t.Fatalf("%s: want an error", in)
			}
		}
	})
}

func TestRemoveHooks(t *testing.T) {
	t.Run("leaves other hooks and drops what is left empty", func(t *testing.T) {
		in := `{"model": "opus", "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "notify"}]}]}}`
		installed, err := capture.InstallHooks([]byte(in), cmd)
		if err != nil {
			t.Fatal(err)
		}
		got, err := capture.RemoveHooks(installed)
		if err != nil {
			t.Fatal(err)
		}
		if h := handlers(t, got, "Stop"); len(h) != 1 || h[0] != "notify" {
			t.Fatalf("Stop: %v", h)
		}
		if strings.Contains(string(got), "SessionStart") || strings.Contains(string(got), "claude-code") {
			t.Fatalf("got %s", got)
		}
	})

	t.Run("with only loci's hooks, removes the hooks key", func(t *testing.T) {
		installed, err := capture.InstallHooks([]byte(`{"model": "opus"}`), cmd)
		if err != nil {
			t.Fatal(err)
		}
		got, err := capture.RemoveHooks(installed)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(got)) != "{\n  \"model\": \"opus\"\n}" {
			t.Fatalf("got %s", got)
		}
	})
}

// allow returns permissions.allow.
func allow(t *testing.T, settings []byte) []string {
	t.Helper()
	var s struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(settings, &s); err != nil {
		t.Fatalf("invalid JSON %s: %v", settings, err)
	}
	return s.Permissions.Allow
}

func TestPermissions(t *testing.T) {
	const loci = "/usr/local/bin/loci"
	want := []string{"Bash(make check)", "Bash(" + loci + " attach:*)", "Bash(" + loci + " ls:*)", "Bash(" + loci + " remember:*)"}

	t.Run("adds loci's rules after the person's and keeps other permissions", func(t *testing.T) {
		in := `{"permissions": {"allow": ["Bash(make check)"], "deny": ["Bash(rm:*)"]}}`
		got, err := capture.InstallPermissions([]byte(in), loci)
		if err != nil {
			t.Fatal(err)
		}
		if a := allow(t, got); strings.Join(a, "|") != strings.Join(want, "|") {
			t.Fatalf("allow %v", a)
		}
		if !strings.Contains(string(got), `"Bash(rm:*)"`) {
			t.Fatalf("deny was lost: %s", got)
		}
	})

	t.Run("replaces the rules for the command the hooks ran, wherever and whatever it was", func(t *testing.T) {
		old := "'/my apps/old-loci' --store a"
		in, err := capture.InstallPermissions([]byte(`{"permissions": {"allow": ["Bash(make check)", "Bash(/other/loci ls:*)"]}}`), old)
		if err != nil {
			t.Fatal(err)
		}
		if in, err = capture.InstallHooks(in, old+" capture claude-code"); err != nil {
			t.Fatal(err)
		}
		got, err := capture.InstallPermissions(in, loci)
		if err != nil {
			t.Fatal(err)
		}
		if a := allow(t, got); strings.Join(a, "|") != "Bash(make check)|Bash(/other/loci ls:*)|"+strings.Join(want[1:], "|") {
			t.Fatalf("allow %v", a)
		}
	})

	t.Run("removing takes out only loci's rules and drops what is left empty", func(t *testing.T) {
		installed := func(settings string) []byte {
			b, err := capture.InstallPermissions([]byte(settings), loci)
			if err != nil {
				t.Fatal(err)
			}
			if b, err = capture.InstallHooks(b, loci+" capture claude-code"); err != nil {
				t.Fatal(err)
			}
			return b
		}
		got, err := capture.RemovePermissions(installed(`{"permissions": {"allow": ["Bash(make check)"]}}`))
		if err != nil {
			t.Fatal(err)
		}
		if a := allow(t, got); len(a) != 1 || a[0] != "Bash(make check)" {
			t.Fatalf("allow %v", a)
		}
		if got, err := capture.RemovePermissions(installed(`{"model": "opus"}`)); err != nil || strings.Contains(string(got), "permissions") {
			t.Fatalf("got %s, %v", got, err)
		}
	})

	t.Run("a rule already there is not added twice", func(t *testing.T) {
		once, err := capture.InstallPermissions(nil, loci)
		if err != nil {
			t.Fatal(err)
		}
		twice, err := capture.InstallPermissions(once, loci) // no hooks to find the old rules by
		if err != nil {
			t.Fatal(err)
		}
		if string(once) != string(twice) {
			t.Fatalf("once:\n%s\ntwice:\n%s", once, twice)
		}
	})

	t.Run("without loci's hooks, no rule is loci's", func(t *testing.T) {
		in := `{"permissions": {"allow": ["Bash(` + loci + ` ls:*)"]}}`
		got, err := capture.RemovePermissions([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if a := allow(t, got); len(a) != 1 {
			t.Fatalf("allow %v", a)
		}
	})

	t.Run("invalid permissions are an error", func(t *testing.T) {
		for _, in := range []string{`{"permissions": []}`, `{"permissions": {"allow": "x"}}`} {
			if _, err := capture.InstallPermissions([]byte(in), loci); err == nil {
				t.Fatalf("%s: want an error", in)
			}
		}
	})
}
