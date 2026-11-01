package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
)

// Control is what the handler needs from the server that hosts it.
type Control struct {
	Version  string
	Status   func() Status
	Shutdown func() // must not block; the server stops after the response is written
	Logger   *slog.Logger
}

// NewHandler serves every operation of svc. Register each new operation here
// with one handle call; the route name is the operation's snake_case name.
func NewHandler(svc app.Service, ctl Control) http.Handler {
	mux := http.NewServeMux()
	handle(mux, ctl.Logger, "set_task_project", svc.SetTaskProject)
	handle(mux, ctl.Logger, "list", svc.List)
	handle(mux, ctl.Logger, "remember", svc.Remember)
	handle(mux, ctl.Logger, "search_memories", svc.SearchMemories)
	handle(mux, ctl.Logger, "embed", svc.Embed)
	handle(mux, ctl.Logger, "mark", svc.Mark)
	handle(mux, ctl.Logger, "attach", svc.Attach)
	handle(mux, ctl.Logger, "capture_start", svc.CaptureStart)
	handle(mux, ctl.Logger, "capture_message", svc.CaptureMessage)
	handle(mux, ctl.Logger, "search_conversations", svc.SearchConversations)
	handle(mux, ctl.Logger, "get_conversation", svc.GetConversation)

	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, ctl.Status())
	})
	mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		ctl.Shutdown()
	})
	return checkVersion(ctl, mux)
}

// handle registers POST /op/<name>, decoding In and encoding Out as JSON.
func handle[In, Out any](mux *http.ServeMux, log *slog.Logger, name string, op func(context.Context, In) (Out, error)) {
	mux.HandleFunc("POST /op/"+name, func(w http.ResponseWriter, r *http.Request) {
		var in In
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			writeError(w, fmt.Errorf("decode %s input: %w: %w", name, err, domain.ErrInvalid))
			return
		}
		out, err := op(r.Context(), in)
		if err != nil {
			if kindOf(err) == kindInternal {
				log.Error("operation failed", "op", name, "err", err)
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// checkVersion rejects operations from a client built from a different
// binary and shuts the server down so the client can start a current one.
// Status and shutdown are exempt so any version can inspect or stop a server.
func checkVersion(ctl Control, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" || r.URL.Path == "/shutdown" {
			next.ServeHTTP(w, r)
			return
		}
		if got := r.Header.Get(VersionHeader); got != ctl.Version {
			ctl.Logger.Info("client version differs, shutting down", "server", ctl.Version, "client", got)
			writeJSON(w, http.StatusConflict, errorBody{Error: wireError{
				Kind:    kindVersionMismatch,
				Message: fmt.Sprintf("server is %s, client is %s", ctl.Version, got),
			}})
			ctl.Shutdown()
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, err error) {
	kind := kindOf(err)
	status := map[string]int{
		kindInvalid:     http.StatusBadRequest,
		kindNotFound:    http.StatusNotFound,
		kindConflict:    http.StatusConflict,
		kindUnavailable: http.StatusServiceUnavailable,
	}[kind]
	if status == 0 {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, errorBody{Error: wireError{Kind: kind, Message: err.Error()}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
