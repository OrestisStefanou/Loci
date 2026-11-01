package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/embed"
	"github.com/OrestisStefanou/Loci/internal/store"
)

// Search result bounds.
const (
	defaultSearchLimit = 50
	maxSearchLimit     = 500

	// minFusionDepth is how many candidates each ranked list contributes to
	// fusion at least; it grows to 3 x limit, up to maxSearchLimit.
	minFusionDepth = 100
	// rrfK is the reciprocal rank fusion constant (design.md section 6).
	rrfK = 60
)

// queryEmbedTimeout bounds embedding a search query. A provider slower than
// this is treated as unavailable, so search falls back to text ranking rather
// than hanging. A query takes milliseconds once the model is loaded and the
// load itself under a second (nomic-embed-text, darwin/arm64).
var queryEmbedTimeout = 3 * time.Second

// Ranking values in SearchMemoriesOutput.
const (
	RankingRecency = "recency" // no query: most recent first
	RankingText    = "text"    // substring matches only, most recent first
	RankingHybrid  = "hybrid"  // substring and vector matches fused by RRF
)

// SearchMemoriesInput is the input of SearchMemories (MCP: loci_search_memories).
// Every filter is optional and they combine with AND.
type SearchMemoriesInput struct {
	// Project matches the effective project: memories on the project's tasks
	// are included (design.md section 4).
	Project string `json:"project,omitempty" jsonschema:"project slug; includes memories on the project's tasks"`
	// Task matches that task only.
	Task string `json:"task,omitempty" jsonschema:"task key; only memories on this task"`
	// Tags must all be present.
	Tags []string `json:"tags,omitempty" jsonschema:"tags that must all be present"`
	// Query is matched as a case-insensitive substring of the content and,
	// when embeddings are on, by meaning; the two rankings are fused
	// (design.md section 6). Substring stands in for full-text search until
	// it lands (section 13, point 3).
	Query string `json:"query,omitempty" jsonschema:"text to search for; matched as a substring and, when embeddings are on, by meaning"`
	// IncludeInactive also returns memories marked inactive.
	IncludeInactive bool `json:"include_inactive,omitempty" jsonschema:"also return memories marked inactive"`
	// Limit caps the result; 0 means 50, at most 500.
	Limit int `json:"limit,omitempty" jsonschema:"maximum number of memories, default 50, at most 500"`
}

// SearchMemoriesOutput is the output of SearchMemories, ordered as Ranking says.
type SearchMemoriesOutput struct {
	Memories []domain.Memory `json:"memories"`
	// Ranking is how Memories are ordered: recency, text or hybrid.
	Ranking string `json:"ranking"`
	// SemanticUnavailable is true when embeddings are on but the provider
	// could not be reached, so a query fell back to text ranking.
	SemanticUnavailable bool `json:"semantic_unavailable,omitempty"`
}

// SearchMemories lists memories bounded by project, task, tags and text. A
// project, task or tag that does not exist has no memories, so the result is
// empty rather than an error.
//
// Without a query the result is most recent first. With one, it is the
// substring matches, fused with the nearest memories by embedding when
// embeddings are on. If the provider cannot be reached the result falls back
// to the substring matches and says so.
func (a *App) SearchMemories(ctx context.Context, in SearchMemoriesInput) (SearchMemoriesOutput, error) {
	out := SearchMemoriesOutput{Memories: []domain.Memory{}, Ranking: RankingRecency}
	if strings.TrimSpace(in.Query) != "" {
		out.Ranking = RankingText
	}
	f := store.MemoryFilter{
		Query:           strings.TrimSpace(in.Query),
		IncludeInactive: in.IncludeInactive,
	}
	var err error
	if f.Limit, err = searchLimit(in.Limit); err != nil {
		return SearchMemoriesOutput{}, err
	}

	if in.Project != "" {
		slug, err := domain.NormalizeProjectSlug(in.Project)
		if err != nil {
			return SearchMemoriesOutput{}, err
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
			return SearchMemoriesOutput{}, err
		}
		t, err := a.store.GetTask(ctx, key)
		if err != nil {
			return out, ignoreNotFound(err)
		}
		f.TaskID = &t.ID
	}
	tags, err := normalizeTags(in.Tags)
	if err != nil {
		return SearchMemoriesOutput{}, err
	}
	for _, name := range tags {
		t, err := a.store.GetTag(ctx, name)
		if err != nil {
			return out, ignoreNotFound(err)
		}
		f.TagIDs = append(f.TagIDs, t.ID)
	}

	if f.Query == "" || a.embedder == nil {
		out.Memories, err = a.store.ListMemories(ctx, f)
		if err != nil {
			return SearchMemoriesOutput{}, err
		}
		return out, nil
	}
	return a.searchHybrid(ctx, f, out)
}

// searchHybrid fuses the substring matches with the nearest memories by
// embedding, both drawn from the same filtered set.
func (a *App) searchHybrid(ctx context.Context, f store.MemoryFilter, out SearchMemoriesOutput) (SearchMemoriesOutput, error) {
	limit := f.Limit
	f.Limit = fusionDepth(limit)
	substringMatches, err := a.store.ListMemories(ctx, f)
	if err != nil {
		return SearchMemoriesOutput{}, err
	}
	vec, err := a.embedQuery(ctx, f.Query)
	if errors.Is(err, errSemanticUnavailable) {
		out.Memories = substringMatches[:min(limit, len(substringMatches))]
		out.SemanticUnavailable = true
		return out, nil
	}
	if err != nil {
		return SearchMemoriesOutput{}, err
	}
	f.QueryVector, f.Model, f.MaxDistance = vec, a.embedder.Model(), a.maxVecDist
	vectorMatches, err := a.store.ListMemories(ctx, f)
	if err != nil {
		return SearchMemoriesOutput{}, err
	}

	fused := fuseRRF(memoryRank, substringMatches, vectorMatches)
	out.Memories = fused[:min(limit, len(fused))]
	out.Ranking = RankingHybrid
	return out, nil
}

// fusionDepth is how many candidates each ranked list contributes to fusion
// for a result of limit items.
func fusionDepth(limit int) int {
	return min(max(minFusionDepth, 3*limit), maxSearchLimit)
}

// errSemanticUnavailable means the query could not be embedded because the
// provider is down or too slow, so search falls back to text ranking.
var errSemanticUnavailable = errors.New("semantic search unavailable")

// embedQuery embeds a search query within queryEmbedTimeout. It returns
// errSemanticUnavailable when the provider cannot be reached or does not
// answer in time, and any other error as is.
func (a *App) embedQuery(ctx context.Context, query string) ([]float32, error) {
	embedCtx, cancel := context.WithTimeout(ctx, queryEmbedTimeout)
	queryVectors, err := a.embedder.Embed(embedCtx, embed.Query, []string{query})
	timedOut := errors.Is(embedCtx.Err(), context.DeadlineExceeded)
	cancel()
	// The caller giving up is never a fallback, even though the provider may
	// report its expired deadline as unavailable.
	if ctx.Err() != nil {
		return nil, fmt.Errorf("embed query: %w", ctx.Err())
	}
	if errors.Is(err, domain.ErrUnavailable) || (err != nil && timedOut) {
		return nil, errSemanticUnavailable
	}
	if err == nil {
		err = checkVectors(queryVectors)
	}
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	return queryVectors[0], nil
}

// memoryRank is what fuseRRF needs of a memory.
func memoryRank(m domain.Memory) (domain.ID, time.Time) { return m.ID, m.CreatedAt }

// fuseRRF merges ranked lists by reciprocal rank fusion: an item scores
// 1/(rrfK + rank) for each list it appears in, rank counted from 1. Ties go
// to the newer item, then the greater ID. An item in several lists is kept
// as it first appears, so the earlier list's copy wins.
func fuseRRF[T any](rank func(T) (domain.ID, time.Time), lists ...[]T) []T {
	score := map[domain.ID]float64{}
	out := []T{}
	for _, list := range lists {
		for i, item := range list {
			id, _ := rank(item)
			if _, seen := score[id]; !seen {
				out = append(out, item)
			}
			score[id] += 1 / float64(rrfK+i+1)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai, at := rank(out[i])
		bi, bt := rank(out[j])
		if score[ai] != score[bi] {
			return score[ai] > score[bi]
		}
		if !at.Equal(bt) {
			return at.After(bt)
		}
		return ai > bi
	})
	return out
}

// ignoreNotFound turns a not-found error into nil, so an unknown filter value
// yields an empty result, and passes every other error through.
func ignoreNotFound(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	return err
}
