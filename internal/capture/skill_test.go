package capture_test

import (
	"strings"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/capture"
)

func TestSkill(t *testing.T) {
	got := capture.Skill("/usr/local/bin/loci --store northwind")
	s := string(got)
	if !strings.HasPrefix(s, "---\nname: loci\ndescription: ") || !capture.IsSkill(got) {
		t.Fatalf("not a loci skill:\n%s", s)
	}
	if !strings.Contains(s, "`/usr/local/bin/loci --store northwind attach <id> --task <KEY>`") || strings.Contains(s, "{loci}") {
		t.Fatalf("command not filled in:\n%s", s)
	}
	if capture.IsSkill([]byte("---\nname: loci\n---\nmy own skill\n")) {
		t.Fatal("a skill without the marker counts as loci's")
	}
}
