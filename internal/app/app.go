// Package app implements Loci's operations (design.md section 6). It is the
// only place business logic lives. Adapters (cli now, mcp and capture later)
// parse input, call one operation, and render its output; they never touch
// the store directly.
//
// Every operation follows the same shape, one file per operation:
//
//	type XInput struct { ... }   // json tags: reused as the MCP tool schema
//	type XOutput struct { ... }  // json tags: what --json prints
//	func (a *App) X(ctx context.Context, in XInput) (XOutput, error)
//
// Operations validate and normalise their own input and return errors that
// wrap the domain sentinels (domain.ErrInvalid, ErrNotFound, ErrConflict).
package app

import (
	"sync"
	"time"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/embed"
	"github.com/OrestisStefanou/Loci/internal/store"
)

// Deps are the App's dependencies. All are required except Embedder.
type Deps struct {
	Store *store.Store
	Clock domain.Clock
	IDs   domain.IDGen
	// UserHandle is the person running Loci (config user.handle), already
	// normalised. It is the author of anything saved without an explicit
	// agent author.
	UserHandle string
	// Embedder computes vectors for semantic search. Nil means embedding is
	// off: Embed does nothing and search ranks by text alone.
	Embedder embed.Embedder
	// MaxVectorDistance drops vector matches farther than this cosine
	// distance from the query (config embedding.max_distance). Zero keeps all.
	MaxVectorDistance float64
}

// App runs operations against a store.
type App struct {
	store      *store.Store
	clock      domain.Clock
	ids        domain.IDGen
	userHandle string
	embedder   embed.Embedder
	maxVecDist float64
	// captureMu serialises capture writes, so concurrent sessions never race
	// on a conversation's next seq or on finding the message for a prompt.
	// One server owns the database, so a mutex is enough.
	captureMu sync.Mutex
	// embedMu serialises Embed, so the worker and `loci embed` never embed
	// the same rows at once.
	embedMu sync.Mutex
}

// New returns an App.
func New(d Deps) *App {
	return &App{store: d.Store, clock: d.Clock, ids: d.IDs, userHandle: d.UserHandle,
		embedder: d.Embedder, maxVecDist: d.MaxVectorDistance}
}

// newID returns a fresh ID and the time it was minted at, so a row's ID and
// created_at agree.
func (a *App) newID() (domain.ID, time.Time) {
	now := a.clock.Now()
	return a.ids.New(now), now
}
