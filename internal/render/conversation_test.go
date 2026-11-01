package render_test

import (
	"strings"
	"testing"
	"time"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/render"
)

func TestConversation(t *testing.T) {
	at := time.Date(2026, 10, 11, 14, 2, 0, 0, time.UTC)
	session := domain.ConversationSummary{
		Conversation: domain.Conversation{ID: "C1", Harness: "claude-code", Session: "s1", Project: "fees-v2", Task: "EPD-1",
			Tags: []string{"decided"}, Repo: "github.com/northwind/api", Branch: "main", StartedAt: at},
		Messages: 4, Subagents: 1,
	}
	msgs := []domain.Message{
		{Seq: 2, Speaker: "claude-code", Addressee: "orestis", Content: "Answer\twith a tab\n", CreatedAt: at},
		{Seq: 3, Speaker: "orestis", Addressee: "claude-code", Content: "Next", CreatedAt: at},
	}
	runs := []domain.ConversationSummary{{Conversation: domain.Conversation{ID: "C2", AgentType: "Explore", StartedAt: at}, Messages: 1}}

	tests := []struct {
		name string
		c    domain.ConversationSummary
		msgs []domain.Message
		runs []domain.ConversationSummary
		want []string
	}{
		{
			name: "a session in a window, with its runs",
			c:    session, msgs: msgs, runs: runs,
			want: []string{
				"# Conversation C1\n",
				"- Attached to: fees-v2/EPD-1 [decided]\n",
				"- Repository: github.com/northwind/api on main\n",
				"- Messages: 2 to 3 of 4\n",
				"---\n\n**#2 claude-code to orestis**, 2026-10-11 14:02 UTC\n\nAnswer\twith a tab\n",
				"## Subagent runs\n\n- C2 Explore, 1 message, 2026-10-11 14:02 UTC\n",
			},
		},
		{
			name: "a subagent run names its session",
			c: domain.ConversationSummary{Conversation: domain.Conversation{ID: "C2", Parent: "C1", AgentType: "Explore",
				Harness: "claude-code", Session: "s1", StartedAt: at}},
			want: []string{"# Subagent run C2\n", "- Attached to: (unattached)\n", "- Run by: Explore, in conversation C1\n", "- Messages: none\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			if err := render.Conversation(&b, tt.c, tt.msgs, tt.runs); err != nil {
				t.Fatal(err)
			}
			for _, w := range tt.want {
				if !strings.Contains(b.String(), w) {
					t.Fatalf("missing %q in:\n%s", w, b.String())
				}
			}
		})
	}
}
