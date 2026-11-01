package daemon

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
)

// Embedding worker states, as loci status shows them.
const (
	embedOff         = "off"
	embedIdle        = "idle" // nothing embedded yet since start: provider not contacted
	embedOK          = "ok"
	embedUnavailable = "unavailable"
	embedError       = "error"
)

// Embedding worker timing. idleTick is how soon a new memory is embedded;
// backoff doubles from minBackoff to maxBackoff while the provider is down.
var (
	idleTick   = 3 * time.Second
	minBackoff = 5 * time.Second
	maxBackoff = 60 * time.Second
)

// embedWorker fills missing embeddings in the background (design.md section
// 10). It runs app.Embed in a loop and remembers the outcome of the last pass
// for loci status.
type embedWorker struct {
	app *app.App
	log *slog.Logger

	mu    sync.Mutex
	state string
	err   string
}

func newEmbedWorker(a *app.App, log *slog.Logger) *embedWorker {
	return &embedWorker{app: a, log: log, state: embedIdle}
}

// run embeds until ctx is done: back to back while a backlog remains, every
// idleTick when caught up, with backoff while the provider is unreachable.
func (w *embedWorker) run(ctx context.Context) {
	var backoff time.Duration
	for {
		out, err := w.app.Embed(ctx, app.EmbedInput{})
		if ctx.Err() != nil {
			return
		}
		wait := idleTick
		switch {
		case errors.Is(err, domain.ErrUnavailable):
			backoff = min(max(2*backoff, minBackoff), maxBackoff)
			wait = backoff
			w.record(embedUnavailable, err)
		case err != nil:
			backoff = 0
			w.record(embedError, err)
		case out.Embedded > 0:
			backoff = 0
			w.record(embedOK, nil)
			if len(out.Failed) > 0 {
				w.log.Warn("embedding provider rejected rows, set aside", "model", out.Model, "ids", out.Failed)
			}
			if out.Pending > 0 {
				continue
			}
		default:
			backoff = 0 // nothing to embed: the provider was not called, keep the last state
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// record stores the outcome of a pass and logs only when it changes, so a
// provider that stays down does not fill the log.
func (w *embedWorker) record(state string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	w.mu.Lock()
	prev, prevMsg := w.state, w.err
	w.state, w.err = state, msg
	w.mu.Unlock()

	switch {
	case state == embedUnavailable && prev != embedUnavailable:
		w.log.Warn("embedding provider unavailable, retrying with backoff", "err", msg)
	case state == embedError && (prev != embedError || msg != prevMsg):
		w.log.Error("embedding failed", "err", msg)
	case state == embedOK && prev != embedOK && prev != embedIdle:
		w.log.Info("embedding provider reachable again")
	}
}

// status returns the last pass's state and error.
func (w *embedWorker) status() (state, err string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state, w.err
}
