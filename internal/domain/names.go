package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// Project slugs, task keys, tag names, actor handles and harness names are
// the whole identity of those entities (design.md section 4). Normalising them
// in one place prevents duplicates such as "epd-1271" and "EPD-1271".

var (
	slugRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	taskKeyRe = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]*$`)
)

const maxNameLen = 128

// NormalizeProjectSlug trims and lowercases a project slug.
func NormalizeProjectSlug(s string) (string, error) {
	return normalize("project", strings.ToLower(strings.TrimSpace(s)), slugRe)
}

// NormalizeTaskKey trims and uppercases a task key, e.g. "epd-1271" -> "EPD-1271".
func NormalizeTaskKey(s string) (string, error) {
	return normalize("task", strings.ToUpper(strings.TrimSpace(s)), taskKeyRe)
}

// NormalizeTag trims and lowercases a tag name.
func NormalizeTag(s string) (string, error) {
	return normalize("tag", strings.ToLower(strings.TrimSpace(s)), slugRe)
}

// NormalizeHandle trims and lowercases an actor handle, e.g. "Claude" -> "claude".
func NormalizeHandle(s string) (string, error) {
	return normalize("handle", strings.ToLower(strings.TrimSpace(s)), slugRe)
}

// NormalizeStore trims and lowercases the name of a store: one database,
// such as "northwind" for one company's repositories.
func NormalizeStore(s string) (string, error) {
	return normalize("store", strings.ToLower(strings.TrimSpace(s)), slugRe)
}

// NormalizeHarness trims and lowercases the name of the agent tool a
// conversation was captured from, e.g. "claude-code".
func NormalizeHarness(s string) (string, error) {
	return normalize("harness", strings.ToLower(strings.TrimSpace(s)), slugRe)
}

func normalize(kind, s string, re *regexp.Regexp) (string, error) {
	switch {
	case s == "":
		return "", fmt.Errorf("%s must not be empty: %w", kind, ErrInvalid)
	case len(s) > maxNameLen:
		return "", fmt.Errorf("%s longer than %d characters: %w", kind, maxNameLen, ErrInvalid)
	case !re.MatchString(s):
		return "", fmt.Errorf("%s %q may contain only letters, digits, '.', '_' and '-': %w", kind, s, ErrInvalid)
	}
	return s, nil
}
