package store_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/store"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

// BenchmarkConversationSearch measures conversation search by vector and by
// substring over a store of benchConversations sessions with benchMessages messages
// each, 768-dimension vectors as nomic-embed-text produces (design.md
// section 13, point 4). Run with:
//
//	go test ./internal/store -run '^$' -bench ConversationSearch -benchtime 20x
//
// One project holds a tenth of the sessions, for the bounded case.
// LOCI_BENCH_CONVERSATIONS changes the number of sessions.
func BenchmarkConversationSearch(b *testing.B) {
	benchConversations := 1000
	if n, err := strconv.Atoi(os.Getenv("LOCI_BENCH_CONVERSATIONS")); err == nil {
		benchConversations = n
	}
	s, query := benchStore(b, benchConversations)
	ctx := context.Background()
	for _, bc := range []struct {
		name string
		f    store.ConversationFilter
	}{
		{"vector, all sessions", store.ConversationFilter{QueryVector: query, Model: "m"}},
		{"vector, one project", store.ConversationFilter{QueryVector: query, Model: "m", ProjectID: ptr("p1")}},
		{"substring, all sessions", store.ConversationFilter{Query: "m07"}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			f := bc.f
			f.Limit = 150
			for b.Loop() {
				if _, err := s.ListConversations(ctx, f); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// benchStore returns a store of benchConversations sessions with 50
// embedded messages each, a tenth of them on project p1, and a query vector.
func benchStore(b *testing.B, benchConversations int) (*store.Store, []float32) {
	const (
		benchMessages = 50
		dims          = 768
	)
	setup := time.Now()
	ctx := context.Background()
	s := testutil.NewStore(b)
	rng := rand.New(rand.NewPCG(1, 2))
	vector := func() []float32 {
		v := make([]float32, dims)
		for i := range v {
			v[i] = rng.Float32()*2 - 1
		}
		return v
	}
	if _, err := s.EnsureProject(ctx, domain.Project{ID: "p1", Slug: "bench", CreatedAt: testutil.Epoch}); err != nil {
		b.Fatal(err)
	}
	for c := range benchConversations {
		conv := fmt.Sprintf("c%04d", c)
		var project *domain.ID
		if c%10 == 0 {
			project = ptr("p1")
		}
		if err := s.WithTx(ctx, func(tx *store.Tx) error {
			if _, _, err := tx.EnsureConversation(ctx, store.ConversationRow{ID: conv, Harness: "h", SessionID: conv,
				ProjectID: project, StartedAt: testutil.Epoch}); err != nil {
				return err
			}
			for m := range benchMessages {
				id := fmt.Sprintf("%s-m%02d", conv, m)
				if err := tx.InsertMessage(ctx, store.MessageRow{ID: id, ConversationID: conv, Role: domain.RoleUser,
					Content: "message " + id, CreatedAt: testutil.Epoch}); err != nil {
					return err
				}
				if err := tx.SetEmbedding(ctx, store.EmbedMessages, id, vector(), "m"); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
	query := vector()
	b.Logf("setup: %d messages in %v", benchConversations*benchMessages, time.Since(setup))
	return s, query
}
