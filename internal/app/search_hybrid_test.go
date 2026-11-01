package app_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/embed"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

const (
	cutoff  = "Settlement cut-off is 16:00 CET"
	tokens  = "Rotate the API tokens every day"
	tagging = "Use tags for the kind of a memory"
)

// hybridApp returns an app whose fake places the three memories on separate
// axes, with all three embedded.
func hybridApp(t *testing.T) (*app.App, *embed.Fake) {
	t.Helper()
	fake := embed.NewFake("fake-model")
	fake.Set(cutoff, []float32{1, 0, 0})
	fake.Set(tokens, []float32{0, 1, 0})
	fake.Set(tagging, []float32{0, 0, 1})
	a := testutil.NewAppWithEmbedder(t, fake)
	remember(t, a, cutoff, tokens, tagging)
	if _, err := a.Embed(context.Background(), app.EmbedInput{}); err != nil {
		t.Fatal(err)
	}
	return a, fake
}

func contents(ms []domain.Memory) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.Content)
	}
	return out
}

func TestSearchMemoriesHybrid(t *testing.T) {
	ctx := context.Background()

	t.Run("finds a memory by meaning with no substring hit", func(t *testing.T) {
		a, fake := hybridApp(t)
		fake.Set("when do payments stop", []float32{0.9, 0.1, 0})
		out, err := a.SearchMemories(ctx, app.SearchMemoriesInput{Query: " when do payments stop "})
		if err != nil {
			t.Fatal(err)
		}
		if out.Ranking != app.RankingHybrid || out.SemanticUnavailable {
			t.Fatalf("ranking %q, unavailable %v", out.Ranking, out.SemanticUnavailable)
		}
		if got := contents(out.Memories); len(got) == 0 || got[0] != cutoff {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("a memory in both lists outranks one in a single list", func(t *testing.T) {
		a, fake := hybridApp(t)
		// "tokens" is the substring hit and second by vector; "tagging" is
		// first by vector only.
		fake.Set("tokens", []float32{0, 0.6, 0.8})
		out, err := a.SearchMemories(ctx, app.SearchMemoriesInput{Query: "tokens"})
		if err != nil {
			t.Fatal(err)
		}
		if got := contents(out.Memories); !slices.Equal(got[:2], []string{tokens, tagging}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("limit applies after fusion", func(t *testing.T) {
		a, fake := hybridApp(t)
		fake.Set("cut-off", []float32{1, 0, 0})
		out, err := a.SearchMemories(ctx, app.SearchMemoriesInput{Query: "cut-off", Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if got := contents(out.Memories); !slices.Equal(got, []string{cutoff}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("max distance drops weak vector matches but never substring matches", func(t *testing.T) {
		fake := embed.NewFake("fake-model")
		fake.Set(cutoff, []float32{1, 0, 0})
		fake.Set(tokens, []float32{0, 1, 0})
		fake.Set(tagging, []float32{0, 0, 1})
		fake.Set("tags", []float32{0.9, 0, 0.1}) // near cutoff, far from tagging
		a := app.New(app.Deps{
			Store: testutil.NewStore(t), Clock: testutil.Clock{}, IDs: &testutil.SeqIDs{},
			UserHandle: testutil.UserHandle, Embedder: fake, MaxVectorDistance: 0.4,
		})
		remember(t, a, cutoff, tokens, tagging)
		if _, err := a.Embed(ctx, app.EmbedInput{}); err != nil {
			t.Fatal(err)
		}
		out, err := a.SearchMemories(ctx, app.SearchMemoriesInput{Query: "tags"})
		if err != nil {
			t.Fatal(err)
		}
		// tagging is a substring hit despite being far by vector, cutoff a
		// close vector match, tokens neither, so it is dropped. Both score
		// 1/61 and tie on time, so the greater id (tagging) comes first.
		if got := contents(out.Memories); !slices.Equal(got, []string{tagging, cutoff}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("unembedded memories still appear through the substring list", func(t *testing.T) {
		a, _ := hybridApp(t)
		remember(t, a, "fresh cut-off note, not embedded yet")
		out, err := a.SearchMemories(ctx, app.SearchMemoriesInput{Query: "fresh"})
		if err != nil {
			t.Fatal(err)
		}
		if got := contents(out.Memories); got[0] != "fresh cut-off note, not embedded yet" {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("unavailable provider falls back to text and says so", func(t *testing.T) {
		a, fake := hybridApp(t)
		fake.Fail(fmt.Errorf("refused: %w", domain.ErrUnavailable))
		out, err := a.SearchMemories(ctx, app.SearchMemoriesInput{Query: "cut-off"})
		if err != nil {
			t.Fatal(err)
		}
		if out.Ranking != app.RankingText || !out.SemanticUnavailable {
			t.Fatalf("ranking %q, unavailable %v", out.Ranking, out.SemanticUnavailable)
		}
		if got := contents(out.Memories); !slices.Equal(got, []string{cutoff}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("a provider that never answers falls back to text in time", func(t *testing.T) {
		app.SetQueryEmbedTimeout(t, 50*time.Millisecond)
		a, fake := hybridApp(t)
		fake.Hang(true)
		start := time.Now()
		out, err := a.SearchMemories(ctx, app.SearchMemoriesInput{Query: "cut-off"})
		if err != nil {
			t.Fatal(err)
		}
		if out.Ranking != app.RankingText || !out.SemanticUnavailable || len(out.Memories) != 1 {
			t.Fatalf("got %+v", out)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("took %s", elapsed)
		}
	})

	// The caller gives up while the query is being embedded: after the
	// substring list, so only the guard in searchHybrid can return the error.
	t.Run("a caller cancelling during the embed call gets an error, not a fallback", func(t *testing.T) {
		a, fake := hybridApp(t)
		fake.Hang(true)
		cctx, cancel := context.WithCancel(ctx)
		time.AfterFunc(50*time.Millisecond, cancel)
		out, err := a.SearchMemories(cctx, app.SearchMemoriesInput{Query: "cut-off"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %+v, %v", out, err)
		}
	})

	t.Run("a caller deadline expiring during the embed call is an error, not a fallback", func(t *testing.T) {
		a, fake := hybridApp(t)
		fake.Hang(true) // reports the expired deadline as unavailable, like Ollama
		cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		out, err := a.SearchMemories(cctx, app.SearchMemoriesInput{Query: "cut-off"})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %+v, %v", out, err)
		}
	})

	t.Run("an unusable query vector is an error", func(t *testing.T) {
		a, fake := hybridApp(t)
		fake.Set("cut-off", []float32{})
		if _, err := a.SearchMemories(ctx, app.SearchMemoriesInput{Query: "cut-off"}); err == nil || errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("any other provider error is returned", func(t *testing.T) {
		a, fake := hybridApp(t)
		boom := errors.New("model not found")
		fake.Fail(boom)
		if _, err := a.SearchMemories(ctx, app.SearchMemoriesInput{Query: "cut-off"}); !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("no query is recency and never calls the provider", func(t *testing.T) {
		a, fake := hybridApp(t)
		before := len(fake.Calls())
		out, err := a.SearchMemories(ctx, app.SearchMemoriesInput{})
		if err != nil {
			t.Fatal(err)
		}
		if out.Ranking != app.RankingRecency || len(out.Memories) != 3 || len(fake.Calls()) != before {
			t.Fatalf("ranking %q, %d memories, %d calls", out.Ranking, len(out.Memories), len(fake.Calls())-before)
		}
	})

	t.Run("embedding off ranks by text", func(t *testing.T) {
		a := testutil.NewApp(t)
		remember(t, a, cutoff)
		out, err := a.SearchMemories(ctx, app.SearchMemoriesInput{Query: "cut-off"})
		if err != nil {
			t.Fatal(err)
		}
		if out.Ranking != app.RankingText || out.SemanticUnavailable || len(out.Memories) != 1 {
			t.Fatalf("got %+v", out)
		}
	})
}
