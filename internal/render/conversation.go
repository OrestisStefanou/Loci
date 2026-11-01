// Package render writes stored items as Markdown for people to read: the
// output of loci show, and later loci export and the session-start context
// block (design.md sections 6 and 8). It formats and does nothing else.
package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/OrestisStefanou/Loci/internal/domain"
)

const timeFormat = "2006-01-02 15:04 UTC"

// Conversation writes one conversation as Markdown: a header with where it is
// attached and where it ran, each message under its own heading, then the
// subagent runs it started.
func Conversation(w io.Writer, c domain.ConversationSummary, msgs []domain.Message, runs []domain.ConversationSummary) error {
	ew := &errWriter{w: w}
	title := "Conversation"
	if c.Parent != "" {
		title = "Subagent run"
	}
	ew.printf("# %s %s\n\n", title, c.ID)
	ew.printf("- Attached to: %s\n", Attachment(c.Project, c.Task, c.Tags))
	ew.printf("- Started: %s\n", c.StartedAt.UTC().Format(timeFormat))
	if c.Parent != "" {
		ew.printf("- Run by: %s, in conversation %s\n", orUnknown(c.AgentType), c.Parent)
	}
	if c.Repo != "" {
		where := c.Repo
		if c.Branch != "" {
			where += " on " + c.Branch
		}
		ew.printf("- Repository: %s\n", where)
	}
	ew.printf("- Session: %s %s\n", c.Harness, c.Session)
	switch {
	case c.Messages == 0:
		ew.printf("- Messages: none\n")
	case len(msgs) < c.Messages && len(msgs) > 0:
		ew.printf("- Messages: %d to %d of %d\n", msgs[0].Seq, msgs[len(msgs)-1].Seq, c.Messages)
	default:
		ew.printf("- Messages: %d\n", c.Messages)
	}

	// Messages are Markdown themselves, headings included, so each starts
	// with a rule and a bold line rather than a heading of its own.
	for _, m := range msgs {
		ew.printf("\n---\n\n**#%d %s to %s**, %s\n\n", m.Seq, orUnknown(m.Speaker), orUnknown(m.Addressee),
			m.CreatedAt.UTC().Format(timeFormat))
		ew.printf("%s\n", strings.TrimRight(m.Content, "\n"))
	}

	if len(runs) > 0 {
		ew.printf("\n---\n\n## Subagent runs\n\n")
		for _, r := range runs {
			ew.printf("- %s %s, %s, %s\n", r.ID, orUnknown(r.AgentType), plural(r.Messages, "message"),
				r.StartedAt.UTC().Format(timeFormat))
		}
	}
	return ew.err
}

// Attachment renders where an item is attached, e.g. "northwind/EPD-1 [auth, perf]".
func Attachment(project, task string, tags []string) string {
	var b strings.Builder
	switch {
	case project != "" && task != "":
		b.WriteString(project + "/" + task)
	case project != "":
		b.WriteString(project)
	case task != "":
		b.WriteString(task)
	default:
		b.WriteString("(unattached)")
	}
	if len(tags) > 0 {
		b.WriteString(" [" + strings.Join(tags, ", ") + "]")
	}
	return b.String()
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// errWriter keeps the first write error, so a long sequence of writes is
// checked once.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err == nil {
		_, e.err = fmt.Fprintf(e.w, format, args...)
	}
}
