package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/store"
)

// openingLength is how many characters of a conversation's first message a
// listing shows.
const openingLength = 120

// SearchConversationsInput is the input of SearchConversations
// (MCP: loci_search_conversations). Every filter is optional and they combine
// with AND.
type SearchConversationsInput struct {
	// Project matches the effective project: conversations on the project's
	// tasks are included, and a subagent run with no attachment of its own
	// matches its session's (design.md section 4).
	Project string `json:"project,omitempty" jsonschema:"project slug; includes conversations on the project's tasks"`
	// Task matches that task only.
	Task string `json:"task,omitempty" jsonschema:"task key; only conversations on this task"`
	// Harness and Session find one session's conversation, as a picker in
	// the agent tool does (design.md section 7). Session needs Harness,
	// because a session id is only unique within its tool.
	Harness string `json:"harness,omitempty" jsonschema:"agent tool, e.g. claude-code"`
	Session string `json:"session,omitempty" jsonschema:"the tool's session id; needs harness"`
	// Tags must all be present on the conversation.
	Tags []string `json:"tags,omitempty" jsonschema:"tags that must all be present"`
	// Query is matched as a case-insensitive substring of the messages and,
	// when embeddings are on, by meaning; the two rankings are fused as for
	// memories (design.md section 6). A session matches through its subagent
	// runs' messages too. Each result says which message matched.
	Query string `json:"query,omitempty" jsonschema:"text to search for in the messages; matched as a substring and, when embeddings are on, by meaning"`
	// IncludeSubagents also lists subagent runs, not only sessions.
	IncludeSubagents bool `json:"include_subagents,omitempty" jsonschema:"also list subagent runs, not only the sessions' main conversations"`
	// Limit caps the result; 0 means 50, at most 500.
	Limit int `json:"limit,omitempty" jsonschema:"maximum number of conversations, default 50, at most 500"`
}

// SearchConversationsOutput is the output of SearchConversations, ordered
// as Ranking says.
type SearchConversationsOutput struct {
	Conversations []domain.ConversationSummary `json:"conversations"`
	// Ranking is how Conversations are ordered: recency, text or hybrid.
	Ranking string `json:"ranking"`
	// SemanticUnavailable is true when embeddings are on but the provider
	// could not be reached, so a query fell back to text ranking.
	SemanticUnavailable bool `json:"semantic_unavailable,omitempty"`
}

// SearchConversations lists captured conversations bounded by project, task,
// tags and text. Sessions' main conversations only, each with its count of
// subagent runs, unless subagent runs are asked for. A project, task or tag
// that does not exist has no conversations, so the result is empty rather
// than an error.
//
// Without a query the result is most recently started first. With one, it
// is the conversations with a message containing it, most recent first,
// fused with those whose messages are nearest by embedding when embeddings
// are on, and each result carries its matching message.
func (a *App) SearchConversations(ctx context.Context, in SearchConversationsInput) (SearchConversationsOutput, error) {
	out := SearchConversationsOutput{Conversations: []domain.ConversationSummary{}, Ranking: RankingRecency}
	f := store.ConversationFilter{
		Query:            strings.TrimSpace(in.Query),
		IncludeSubagents: in.IncludeSubagents,
	}
	if f.Query != "" {
		out.Ranking = RankingText
	}
	var err error
	if f.Limit, err = searchLimit(in.Limit); err != nil {
		return SearchConversationsOutput{}, err
	}
	if f.Session = strings.TrimSpace(in.Session); f.Session != "" && strings.TrimSpace(in.Harness) == "" {
		return SearchConversationsOutput{}, fmt.Errorf("session needs harness: %w", domain.ErrInvalid)
	}
	if strings.TrimSpace(in.Harness) != "" {
		if f.Harness, err = domain.NormalizeHarness(in.Harness); err != nil {
			return SearchConversationsOutput{}, err
		}
	}
	if in.Project != "" {
		slug, err := domain.NormalizeProjectSlug(in.Project)
		if err != nil {
			return SearchConversationsOutput{}, err
		}
		p, err := a.store.GetProject(ctx, slug)
		if err != nil {
			return out, ignoreNotFound(err)
		}
		f.ProjectID = &p.ID
	}
	if in.Task != "" {
		key, err := domain.NormalizeTaskKey(in.Task)
		if err != nil {
			return SearchConversationsOutput{}, err
		}
		t, err := a.store.GetTask(ctx, key)
		if err != nil {
			return out, ignoreNotFound(err)
		}
		f.TaskID = &t.ID
	}
	tags, err := normalizeTags(in.Tags)
	if err != nil {
		return SearchConversationsOutput{}, err
	}
	for _, name := range tags {
		t, err := a.store.GetTag(ctx, name)
		if err != nil {
			return out, ignoreNotFound(err)
		}
		f.TagIDs = append(f.TagIDs, t.ID)
	}

	if f.Query == "" || a.embedder == nil {
		if out.Conversations, err = a.store.ListConversations(ctx, f); err != nil {
			return SearchConversationsOutput{}, err
		}
	} else if out, err = a.searchConversationsHybrid(ctx, f, out); err != nil {
		return SearchConversationsOutput{}, err
	}
	shorten(out.Conversations, f.Query)
	return out, nil
}

// searchConversationsHybrid fuses the conversations with a substring match
// with those whose messages are nearest by embedding, both drawn from the
// same filtered set. A conversation in both lists keeps its nearest message
// as its match.
func (a *App) searchConversationsHybrid(ctx context.Context, f store.ConversationFilter, out SearchConversationsOutput) (SearchConversationsOutput, error) {
	limit := f.Limit
	f.Limit = fusionDepth(limit)
	substringMatches, err := a.store.ListConversations(ctx, f)
	if err != nil {
		return SearchConversationsOutput{}, err
	}
	vec, err := a.embedQuery(ctx, f.Query)
	if errors.Is(err, errSemanticUnavailable) {
		out.Conversations = substringMatches[:min(limit, len(substringMatches))]
		out.SemanticUnavailable = true
		return out, nil
	}
	if err != nil {
		return SearchConversationsOutput{}, err
	}
	f.QueryVector, f.Model, f.MaxDistance = vec, a.embedder.Model(), a.maxVecDist
	vectorMatches, err := a.store.ListConversations(ctx, f)
	if err != nil {
		return SearchConversationsOutput{}, err
	}

	fused := fuseRRF(conversationRank, vectorMatches, substringMatches)
	out.Conversations = fused[:min(limit, len(fused))]
	out.Ranking = RankingHybrid
	return out, nil
}

// conversationRank is what fuseRRF needs of a conversation.
func conversationRank(c domain.ConversationSummary) (domain.ID, time.Time) { return c.ID, c.StartedAt }

// searchLimit validates a search limit: 0 means the default, more than the
// maximum is capped.
func searchLimit(limit int) (int, error) {
	switch {
	case limit < 0:
		return 0, fmt.Errorf("limit must not be negative: %w", domain.ErrInvalid)
	case limit == 0:
		return defaultSearchLimit, nil
	}
	return min(limit, maxSearchLimit), nil
}

// shorten cuts each opening message and match snippet to one line of
// openingLength characters, so a listing stays one entry per conversation.
// A snippet containing the query is cut around its first occurrence.
func shorten(cs []domain.ConversationSummary, query string) {
	for i := range cs {
		cs[i].Opening = oneLine(cs[i].Opening, "")
		if cs[i].Match != nil {
			cs[i].Match.Snippet = oneLine(cs[i].Match.Snippet, query)
		}
	}
}

// snippetLead is how many characters a snippet keeps before the query.
const snippetLead = 30

// oneLine collapses whitespace in s and cuts it to openingLength characters,
// marking a cut with an ellipsis. When s contains query, case-insensitively,
// the window starts snippetLead characters before it.
func oneLine(s, query string) string {
	r := []rune(strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " "))
	if len(r) <= openingLength {
		return string(r)
	}
	start := 0
	if i := indexFold(r, []rune(strings.Join(strings.FieldsFunc(query, unicode.IsSpace), " "))); i > snippetLead {
		start = min(i-snippetLead, len(r)-openingLength+1)
	}
	var b strings.Builder
	n := openingLength
	if start > 0 {
		b.WriteString("…")
		n--
	}
	end := start + n
	if end < len(r) {
		end--
	} else {
		end = len(r)
	}
	b.WriteString(string(r[start:end]))
	if end < len(r) {
		b.WriteString("…")
	}
	return b.String()
}

// indexFold returns the index of the first occurrence of sub in s, compared
// rune by rune ignoring case, or -1.
func indexFold(s, sub []rune) int {
	if len(sub) == 0 {
		return -1
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j, c := range sub {
			if unicode.ToLower(s[i+j]) != unicode.ToLower(c) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
