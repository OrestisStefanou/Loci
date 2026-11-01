package app_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/embed"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

func remember(t *testing.T, a *app.App, contents ...string) {
	t.Helper()
	for _, c := range contents {
		if _, err := a.Remember(context.Background(), app.RememberInput{Content: c, Project: "northwind"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEmbed(t *testing.T) {
	ctx := context.Background()

	t.Run("embeds in batches until nothing is pending", func(t *testing.T) {
		fake := embed.NewFake("fake-model")
		a := testutil.NewAppWithEmbedder(t, fake)
		remember(t, a, "one", "two", "three")

		out, err := a.Embed(ctx, app.EmbedInput{Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if want := (app.EmbedOutput{Model: "fake-model", Embedded: 2, Pending: 1, Enabled: true}); !reflect.DeepEqual(out, want) {
			t.Fatalf("first batch: got %+v, want %+v", out, want)
		}
		out, err = a.Embed(ctx, app.EmbedInput{Limit: 2})
		if err != nil || out.Embedded != 1 || out.Pending != 0 {
			t.Fatalf("second batch: got %+v, %v", out, err)
		}
		out, err = a.Embed(ctx, app.EmbedInput{})
		if err != nil || out.Embedded != 0 || out.Pending != 0 {
			t.Fatalf("nothing left: got %+v, %v", out, err)
		}
		if n := len(fake.Calls()); n != 2 {
			t.Fatalf("want 2 provider calls, got %d", n)
		}
		model, pending, _, err := a.EmbeddingBacklog(ctx)
		if err != nil || model != "fake-model" || pending != 0 {
			t.Fatalf("backlog: %q %d %v", model, pending, err)
		}
	})

	t.Run("backfill embeds everything again", func(t *testing.T) {
		a := testutil.NewAppWithEmbedder(t, embed.NewFake("fake-model"))
		remember(t, a, "one", "two")
		if _, err := a.Embed(ctx, app.EmbedInput{}); err != nil {
			t.Fatal(err)
		}
		out, err := a.Embed(ctx, app.EmbedInput{Backfill: true})
		if err != nil || out.Embedded != 2 || out.Pending != 0 {
			t.Fatalf("got %+v, %v", out, err)
		}
	})

	t.Run("messages are embedded too, after pending memories", func(t *testing.T) {
		fake := embed.NewFake("fake-model")
		a := testutil.NewAppWithEmbedder(t, fake)
		captured(t, a, app.CaptureStartInput{CaptureKey: key("s1", "")}, "prompt", "answer")
		remember(t, a, "memory")
		model, pending, _, err := a.EmbeddingBacklog(ctx)
		if err != nil || model != "fake-model" || pending != 3 {
			t.Fatalf("backlog before: %q %d %v", model, pending, err)
		}

		out, err := a.Embed(ctx, app.EmbedInput{Limit: 2})
		if err != nil || out.Embedded != 2 || out.Pending != 1 {
			t.Fatalf("first batch: got %+v, %v", out, err)
		}
		if got := fake.Calls(); len(got) != 1 || fmt.Sprint(got[0]) != "[memory prompt]" {
			t.Fatalf("the newer memory goes first, then the oldest message: %q", got)
		}
		if out, err = a.Embed(ctx, app.EmbedInput{}); err != nil || out.Embedded != 1 || out.Pending != 0 {
			t.Fatalf("second batch: got %+v, %v", out, err)
		}

		out, err = a.Embed(ctx, app.EmbedInput{Backfill: true})
		if err != nil || out.Embedded != 3 || out.Pending != 0 {
			t.Fatalf("backfill re-embeds messages too: got %+v, %v", out, err)
		}
	})

	t.Run("embedding off does nothing", func(t *testing.T) {
		a := testutil.NewApp(t)
		remember(t, a, "one")
		out, err := a.Embed(ctx, app.EmbedInput{})
		if err != nil || !reflect.DeepEqual(out, app.EmbedOutput{}) {
			t.Fatalf("got %+v, %v", out, err)
		}
		if model, pending, _, err := a.EmbeddingBacklog(ctx); err != nil || model != "" || pending != 0 {
			t.Fatalf("backlog: %q %d %v", model, pending, err)
		}
	})

	t.Run("unavailable provider leaves rows pending", func(t *testing.T) {
		fake := embed.NewFake("fake-model")
		fake.Fail(fmt.Errorf("refused: %w", domain.ErrUnavailable))
		a := testutil.NewAppWithEmbedder(t, fake)
		remember(t, a, "one")
		if _, err := a.Embed(ctx, app.EmbedInput{}); !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("want ErrUnavailable, got %v", err)
		}
		if _, pending, _, _ := a.EmbeddingBacklog(ctx); pending != 1 {
			t.Fatalf("pending %d", pending)
		}
		fake.Fail(nil)
		if out, err := a.Embed(ctx, app.EmbedInput{}); err != nil || out.Embedded != 1 {
			t.Fatalf("after recovery: %+v, %v", out, err)
		}
	})

	t.Run("a row the provider keeps rejecting is set aside and stops blocking", func(t *testing.T) {
		for name, bad := range map[string]func(*embed.Fake){
			"empty vector":     func(f *embed.Fake) { f.Set("bad", []float32{}) },
			"other size":       func(f *embed.Fake) { f.Set("bad", []float32{1, 0}) },
			"not a number":     func(f *embed.Fake) { f.Set("bad", []float32{float32(math.NaN()), 0, 1}) },
			"provider refuses": func(f *embed.Fake) { f.Reject("bad", errors.New("ollama: 500 Internal Server Error")) },
		} {
			t.Run(name, func(t *testing.T) {
				fake := embed.NewFake("fake-model")
				bad(fake)
				a := testutil.NewAppWithEmbedder(t, fake)
				remember(t, a, "bad", "good") // bad is oldest, so it heads the queue
				out, err := a.Embed(ctx, app.EmbedInput{})
				if err != nil || out.Embedded != 1 || len(out.Failed) != 1 || out.Pending != 0 {
					t.Fatalf("got %+v, %v", out, err)
				}
				if _, pending, failed, _ := a.EmbeddingBacklog(ctx); pending != 0 || failed != 1 {
					t.Fatalf("pending %d, failed %d", pending, failed)
				}
				remember(t, a, "newer")
				if out, err := a.Embed(ctx, app.EmbedInput{}); err != nil || out.Embedded != 1 || len(out.Failed) != 0 {
					t.Fatalf("a newer row is embedded and the failed one is not retried: %+v, %v", out, err)
				}
			})
		}
	})

	t.Run("when every row fails, nothing is set aside and the error is returned", func(t *testing.T) {
		fake := embed.NewFake("fake-model")
		fake.Set("one", []float32{float32(math.NaN()), 0, 1})
		fake.Reject("two", errors.New("ollama: 500 Internal Server Error"))
		a := testutil.NewAppWithEmbedder(t, fake)
		remember(t, a, "one", "two")
		if _, err := a.Embed(ctx, app.EmbedInput{}); err == nil || errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("got %v", err)
		}
		if _, pending, failed, _ := a.EmbeddingBacklog(ctx); pending != 2 || failed != 0 {
			t.Fatalf("pending %d, failed %d", pending, failed)
		}
	})

	t.Run("a lone failing row stays pending until another row succeeds beside it", func(t *testing.T) {
		fake := embed.NewFake("fake-model")
		fake.Reject("bad", errors.New("ollama: 500 Internal Server Error"))
		a := testutil.NewAppWithEmbedder(t, fake)
		remember(t, a, "bad")
		if _, err := a.Embed(ctx, app.EmbedInput{}); err == nil {
			t.Fatal("want the error")
		}
		remember(t, a, "good")
		if out, err := a.Embed(ctx, app.EmbedInput{}); err != nil || out.Embedded != 1 || len(out.Failed) != 1 {
			t.Fatalf("got %+v, %v", out, err)
		}
	})

	t.Run("an unavailable provider during the retries sets nothing aside", func(t *testing.T) {
		fake := embed.NewFake("fake-model")
		fake.Reject("bad", errors.New("ollama: 500 Internal Server Error"))
		fake.Reject("good", fmt.Errorf("refused: %w", domain.ErrUnavailable))
		a := testutil.NewAppWithEmbedder(t, fake)
		remember(t, a, "bad", "good")
		if _, err := a.Embed(ctx, app.EmbedInput{}); !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("got %v", err)
		}
		if _, pending, failed, _ := a.EmbeddingBacklog(ctx); pending != 2 || failed != 0 {
			t.Fatalf("pending %d, failed %d", pending, failed)
		}
	})

	t.Run("failed rows are tried again under another model and after backfill", func(t *testing.T) {
		fake := embed.NewFake("fake-model")
		fake.Reject("bad", errors.New("ollama: 500 Internal Server Error"))
		s := testutil.NewStore(t)
		a := app.New(app.Deps{Store: s, Clock: testutil.Clock{}, IDs: &testutil.SeqIDs{}, UserHandle: testutil.UserHandle, Embedder: fake})
		remember(t, a, "bad", "good")
		if out, err := a.Embed(ctx, app.EmbedInput{}); err != nil || len(out.Failed) != 1 {
			t.Fatalf("got %+v, %v", out, err)
		}

		other := embed.NewFake("other-model")
		b := app.New(app.Deps{Store: s, Clock: testutil.Clock{}, IDs: &testutil.SeqIDs{}, UserHandle: testutil.UserHandle, Embedder: other})
		if _, pending, failed, _ := b.EmbeddingBacklog(ctx); pending != 2 || failed != 0 {
			t.Fatalf("another model: pending %d, failed %d", pending, failed)
		}

		fake.Reject("bad", nil)
		if out, err := a.Embed(ctx, app.EmbedInput{Backfill: true}); err != nil || out.Embedded != 2 || len(out.Failed) != 0 {
			t.Fatalf("backfill: %+v, %v", out, err)
		}
		if _, _, failed, _ := a.EmbeddingBacklog(ctx); failed != 0 {
			t.Fatalf("failed %d after backfill", failed)
		}
	})

	t.Run("negative limit is invalid", func(t *testing.T) {
		a := testutil.NewAppWithEmbedder(t, embed.NewFake("fake-model"))
		if _, err := a.Embed(ctx, app.EmbedInput{Limit: -1}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("got %v", err)
		}
	})
}
