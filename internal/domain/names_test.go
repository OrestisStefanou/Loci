package domain

import (
	"errors"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name    string
		fn      func(string) (string, error)
		in      string
		want    string
		wantErr bool
	}{
		{"task uppercased", NormalizeTaskKey, " epd-1271 ", "EPD-1271", false},
		{"task empty", NormalizeTaskKey, "  ", "", true},
		{"task with space", NormalizeTaskKey, "EPD 1", "", true},
		{"project lowercased", NormalizeProjectSlug, "Northwind", "northwind", false},
		{"project with slash", NormalizeProjectSlug, "a/b", "", true},
		{"tag lowercased", NormalizeTag, "Client-Onboarding", "client-onboarding", false},
		{"tag leading dash", NormalizeTag, "-x", "", true},
		{"handle lowercased", NormalizeHandle, " Claude ", "claude", false},
		{"handle with at sign", NormalizeHandle, "a@b", "", true},
		{"harness lowercased", NormalizeHarness, " Claude-Code ", "claude-code", false},
		{"harness empty", NormalizeHarness, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.fn(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("want ErrInvalid, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
