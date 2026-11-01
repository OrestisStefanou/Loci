package domain

import (
	"net/url"
	"strings"
)

// NormalizeRepo reduces the forms of a remote repository's URL to host/path,
// lowercased: "git@github.com:Northwind/API.git", "https://github.com/northwind/api",
// "ssh://git@github.com:22/northwind/api.git" and "github.com/Northwind/API" all become
// "github.com/northwind/api". User names, credentials and ports are dropped.
// Anything that names no remote host (a local path, a file:// URL) returns "",
// because a local path carries the user's name and identifies nothing shared.
func NormalizeRepo(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" || strings.ContainsAny(s, `\ `) || strings.HasPrefix(s, "/") ||
		strings.HasPrefix(s, ".") || strings.HasPrefix(s, "~") {
		return ""
	}
	var host, path string
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return ""
		}
		host, path = u.Hostname(), u.Path
	} else {
		// scp-like [user[:password]@]host:path, or host/path already. The
		// user part ends at the last '@', so a password with ':' is dropped.
		rest := s
		if i := strings.LastIndex(rest, "@"); i >= 0 {
			rest = rest[i+1:]
		}
		var ok bool
		if host, path, ok = strings.Cut(rest, ":"); !ok || strings.Contains(host, "/") {
			host, path, _ = strings.Cut(rest, "/")
		}
	}
	path = strings.Trim(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/")
	if host == "" || path == "" {
		return ""
	}
	return strings.ToLower(host + "/" + path)
}
