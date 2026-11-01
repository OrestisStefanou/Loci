package capture

import (
	"bufio"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ReadGit returns the origin URL and the current branch of the repository
// containing dir, by reading files under .git rather than running git, which
// would cost more than the rest of a capture call. Both are provenance only,
// so anything it cannot read is returned as "" rather than as an error. The
// URL is returned as written; the capture operation normalises it and drops
// credentials and local paths.
func ReadGit(dir string) (repo, branch string) {
	if dir == "" {
		return "", ""
	}
	_, gitDir, commonDir := findGitDir(dir)
	if gitDir == "" {
		return "", ""
	}
	return originURL(filepath.Join(commonDir, "config")), headBranch(filepath.Join(gitDir, "HEAD"))
}

// RepoRoot returns the top of the working tree containing dir, and the git
// directory shared by all its worktrees (which holds info/exclude). Both are
// "" when dir is not in a repository.
func RepoRoot(dir string) (root, commonDir string) {
	root, _, commonDir = findGitDir(dir)
	return root, commonDir
}

// findGitDir walks up from dir to the first .git; root is the directory
// holding it. A .git file (a worktree or a submodule) names the real git
// directory, whose commondir file, if any, names the directory shared by all
// worktrees, which holds the config.
func findGitDir(dir string) (root, gitDir, commonDir string) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", "", ""
	}
	for {
		p := filepath.Join(dir, ".git")
		if fi, err := os.Stat(p); err == nil {
			if fi.IsDir() {
				return dir, p, p
			}
			gitDir, commonDir := linkedGitDir(p)
			if gitDir == "" {
				return "", "", ""
			}
			return dir, gitDir, commonDir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", ""
		}
		dir = parent
	}
}

func linkedGitDir(dotGit string) (gitDir, commonDir string) {
	b, err := os.ReadFile(dotGit)
	if err != nil {
		return "", ""
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return "", ""
	}
	gitDir = relativeTo(filepath.Dir(dotGit), strings.TrimSpace(target))
	commonDir = gitDir
	if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		commonDir = relativeTo(gitDir, strings.TrimSpace(string(b)))
	}
	return gitDir, commonDir
}

func relativeTo(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// headBranch returns the branch HEAD points to, or "" when detached.
func headBranch(head string) string {
	b, err := os.ReadFile(head)
	if err != nil {
		return ""
	}
	branch, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "ref: refs/heads/")
	if !ok {
		return ""
	}
	return branch
}

// originURL returns url in the [remote "origin"] section of a git config
// file. Includes and multiple URLs are not followed: the first one wins.
func originURL(config string) string {
	b, err := os.ReadFile(config)
	if err != nil {
		return ""
	}
	inOrigin := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inOrigin = strings.Join(strings.Fields(line), " ") == `[remote "origin"]`
			continue
		}
		if !inOrigin {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(name), "url") {
			return strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	return ""
}

// Exclude adds patterns to info/exclude in the shared git directory
// commonDir, skipping those already there. Unlike .gitignore, that file
// belongs to the clone and is never committed.
func Exclude(commonDir string, patterns ...string) error {
	path := filepath.Join(commonDir, "info", "exclude")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var add strings.Builder
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		add.WriteString("\n")
	}
	for _, p := range patterns {
		if !have[p] {
			add.WriteString(p + "\n")
		}
	}
	if add.Len() == 0 || add.String() == "\n" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(add.String())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
