package capture_test

import (
	"path/filepath"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/capture"
)

func TestReadGit(t *testing.T) {
	const origin = "[core]\n\tbare = false\n[remote \"upstream\"]\n\turl = git@github.com:other/api.git\n" +
		"[remote \"origin\"]\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n\turl = https://github.com/northwind/api.git\n"

	tests := []struct {
		name               string
		setup              func(t *testing.T, root string) string // returns the cwd
		wantRepo, wantBran string
	}{
		{"branch and origin", func(t *testing.T, root string) string {
			writeGit(t, root, "ref: refs/heads/main\n", origin)
			return root
		}, "https://github.com/northwind/api.git", "main"},
		{"from a subdirectory", func(t *testing.T, root string) string {
			writeGit(t, root, "ref: refs/heads/feat/x\n", origin)
			return filepath.Join(root, "internal", "app")
		}, "https://github.com/northwind/api.git", "feat/x"},
		{"detached HEAD has no branch", func(t *testing.T, root string) string {
			writeGit(t, root, "4b825dc642cb6eb9a060e54bf8d69288fbee4904\n", origin)
			return root
		}, "https://github.com/northwind/api.git", ""},
		{"no origin", func(t *testing.T, root string) string {
			writeGit(t, root, "ref: refs/heads/main\n", "[remote \"upstream\"]\n\turl = git@github.com:other/api.git\n")
			return root
		}, "", "main"},
		{"a worktree reads HEAD from its own git dir and config from the common one", func(t *testing.T, root string) string {
			writeGit(t, filepath.Join(root, "main"), "ref: refs/heads/main\n", origin)
			wtGit := filepath.Join(root, "main", ".git", "worktrees", "wt")
			writeFile(t, filepath.Join(wtGit, "HEAD"), "ref: refs/heads/feat/wt\n")
			writeFile(t, filepath.Join(wtGit, "commondir"), "../..\n")
			writeFile(t, filepath.Join(root, "wt", ".git"), "gitdir: "+wtGit+"\n")
			return filepath.Join(root, "wt")
		}, "https://github.com/northwind/api.git", "feat/wt"},
		{"a relative gitdir", func(t *testing.T, root string) string {
			writeGit(t, filepath.Join(root, "real"), "ref: refs/heads/dev\n", origin)
			writeFile(t, filepath.Join(root, "repo", ".git"), "gitdir: ../real/.git\n")
			return filepath.Join(root, "repo")
		}, "https://github.com/northwind/api.git", "dev"},
		{"not a repository", func(t *testing.T, root string) string { return root }, "", ""},
		{"no directory", func(*testing.T, string) string { return "" }, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cwd := tc.setup(t, t.TempDir())
			repo, branch := capture.ReadGit(cwd)
			if repo != tc.wantRepo || branch != tc.wantBran {
				t.Fatalf("got (%q, %q), want (%q, %q)", repo, branch, tc.wantRepo, tc.wantBran)
			}
		})
	}
}

func TestRepoRoot(t *testing.T) {
	root := t.TempDir()
	writeGit(t, filepath.Join(root, "main"), "ref: refs/heads/main\n", "")
	wtGit := filepath.Join(root, "main", ".git", "worktrees", "wt")
	writeFile(t, filepath.Join(wtGit, "HEAD"), "ref: refs/heads/feat/wt\n")
	writeFile(t, filepath.Join(wtGit, "commondir"), "../..\n")
	writeFile(t, filepath.Join(root, "wt", "src", "x.go"), "")
	writeFile(t, filepath.Join(root, "wt", ".git"), "gitdir: "+wtGit+"\n")

	tests := []struct{ name, dir, wantRoot, wantCommon string }{
		{"a repository", filepath.Join(root, "main"), filepath.Join(root, "main"), filepath.Join(root, "main", ".git")},
		{"a worktree shares the main git directory", filepath.Join(root, "wt", "src"), filepath.Join(root, "wt"), filepath.Join(root, "main", ".git")},
		{"not a repository", root, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotRoot, gotCommon := capture.RepoRoot(tc.dir)
			if gotRoot != tc.wantRoot || gotCommon != tc.wantCommon {
				t.Fatalf("got (%q, %q), want (%q, %q)", gotRoot, gotCommon, tc.wantRoot, tc.wantCommon)
			}
		})
	}
}
