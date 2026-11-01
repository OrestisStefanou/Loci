// Package rpc carries operations between processes and loci serve: HTTP over
// a Unix socket, JSON bodies. Each operation's Input and Output structs from
// internal/app are the request and response bodies, so the wire format needs
// no types of its own beyond errors and status.
//
//	POST /op/<name>   body: XInput, response: XOutput or an error envelope
//	GET  /status      response: Status
//	POST /shutdown    stop the server
//
// Every request carries VersionHeader. A server receiving a different version
// answers with a version mismatch and shuts down; the client then starts a
// fresh server from its own binary and retries.
package rpc

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OrestisStefanou/Loci/internal/domain"
)

// VersionHeader carries the client's version.Get().
const VersionHeader = "X-Loci-Version"

// Status describes a running server.
type Status struct {
	Version   string          `json:"version"`
	PID       int             `json:"pid"`
	DB        string          `json:"db"`
	StartedAt time.Time       `json:"started_at"`
	Embedding EmbeddingStatus `json:"embedding"`
}

// EmbeddingStatus describes the server's embedding worker.
type EmbeddingStatus struct {
	// State is off, idle (provider not contacted since the server started),
	// ok (the last call to the provider succeeded), unavailable (provider
	// unreachable, retrying) or error (the last call failed otherwise).
	State   string `json:"state"`
	Model   string `json:"model,omitempty"`
	Pending int    `json:"pending"`          // memories and messages waiting for an embedding
	Failed  int    `json:"failed,omitempty"` // set aside: the provider rejected them under Model
	Error   string `json:"error,omitempty"`
}

// ErrServerNotRunning means nothing is listening on the socket.
var ErrServerNotRunning = errors.New("loci serve is not running")

var errVersionMismatch = errors.New("loci serve is running a different version")

// Error kinds on the wire. They map one to one onto domain sentinels.
const (
	kindInvalid         = "invalid"
	kindNotFound        = "not_found"
	kindConflict        = "conflict"
	kindUnavailable     = "unavailable"
	kindVersionMismatch = "version_mismatch"
	kindInternal        = "internal"
)

type errorBody struct {
	Error wireError `json:"error"`
}

type wireError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func kindOf(err error) string {
	switch {
	case errors.Is(err, domain.ErrInvalid):
		return kindInvalid
	case errors.Is(err, domain.ErrNotFound):
		return kindNotFound
	case errors.Is(err, domain.ErrConflict):
		return kindConflict
	case errors.Is(err, domain.ErrUnavailable):
		return kindUnavailable
	default:
		return kindInternal
	}
}

// toError rebuilds an error on the client side so that errors.Is against the
// domain sentinels works the same as in-process.
func (e wireError) toError() error {
	var sentinel error
	switch e.Kind {
	case kindInvalid:
		sentinel = domain.ErrInvalid
	case kindNotFound:
		sentinel = domain.ErrNotFound
	case kindConflict:
		sentinel = domain.ErrConflict
	case kindUnavailable:
		sentinel = domain.ErrUnavailable
	case kindVersionMismatch:
		sentinel = errVersionMismatch
	default:
		return errors.New(e.Message)
	}
	// The server formats errors with the sentinel's text already at the end
	// ("...: invalid input"), so drop it before wrapping to avoid repeating it.
	return fmt.Errorf("%s: %w", strings.TrimSuffix(e.Message, ": "+sentinel.Error()), sentinel)
}
