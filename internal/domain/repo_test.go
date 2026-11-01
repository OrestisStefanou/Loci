package domain

import "testing"

func TestNormalizeRepo(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"scp-like with .git", "git@github.com:Northwind/API.git", "github.com/northwind/api"},
		{"scp-like ssh alias", "github-work:northwind/api.git", "github-work/northwind/api"},
		{"scp-like password dropped", "user:tok@github.com:northwind/api", "github.com/northwind/api"},
		{"https", "https://github.com/northwind/api", "github.com/northwind/api"},
		{"https with .git and slash", "https://github.com/northwind/api.git/", "github.com/northwind/api"},
		{"https credentials dropped", "https://x-access-token:secret@github.com/northwind/api.git", "github.com/northwind/api"},
		{"ssh with port", "ssh://git@github.com:22/northwind/api.git", "github.com/northwind/api"},
		{"host/path lowercased", "github.com/Northwind/API", "github.com/northwind/api"},
		{"absolute local path", "/Users/someone/code/api", ""},
		{"relative local path", "../api", ""},
		{"home path", "~/code/api", ""},
		{"file url", "file:///srv/git/api.git", ""},
		{"windows path", `C:\Users\someone\api`, ""},
		{"host without path", "github.com", ""},
		{"empty", "  ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeRepo(tt.in); got != tt.want {
				t.Fatalf("NormalizeRepo(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
