package cli

import (
	"encoding/json"
	"io"
	"text/tabwriter"
)

// render prints v as indented JSON when --json is set, and otherwise calls
// text. Every command prints through render so --json works everywhere.
func (r *root) render(v any, text func(w io.Writer)) error {
	if r.json {
		return r.printJSON(v)
	}
	tw := tabwriter.NewWriter(r.env.Stdout, 0, 4, 2, ' ', 0)
	text(tw)
	return tw.Flush()
}

// renderDocument is render for text that is a document, such as Markdown from
// internal/render: written as is, without the column alignment that would
// rewrite tabs in stored content.
func (r *root) renderDocument(v any, text func(w io.Writer) error) error {
	if r.json {
		return r.printJSON(v)
	}
	return text(r.env.Stdout)
}

func (r *root) printJSON(v any) error {
	enc := json.NewEncoder(r.env.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
