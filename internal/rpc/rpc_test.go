package rpc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/app"
	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/embed"
	"github.com/OrestisStefanou/Loci/internal/rpc"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

// serve runs the handler over a Unix socket and returns the socket path and
// a counter of shutdown requests.
func serve(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	return serveApp(t, testutil.NewApp(t))
}

// serveApp is serve over a given app.
func serveApp(t *testing.T, a *app.App) (string, *atomic.Int32) {
	t.Helper()
	socket := filepath.Join(testutil.ShortDir(t), "s.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var shutdowns atomic.Int32
	h := rpc.NewHandler(a, rpc.Control{
		Version:  "v1",
		Status:   func() rpc.Status { return rpc.Status{Version: "v1"} },
		Shutdown: func() { shutdowns.Add(1) },
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return socket, &shutdowns
}

func post(t *testing.T, socket, path, version string, body any) (int, map[string]any) {
	t.Helper()
	c := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, "http://loci"+path, bytes.NewReader(b))
	req.Header.Set(rpc.VersionHeader, version)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestUnknownFieldsAreInvalid(t *testing.T) {
	socket, _ := serve(t)
	status, body := post(t, socket, "/op/list", "v1", map[string]any{"kind": "tasks", "bogus": 1})
	if status != http.StatusBadRequest || body["error"].(map[string]any)["kind"] != "invalid" {
		t.Fatalf("got %d %v", status, body)
	}
}

func TestVersionMismatchShutsDown(t *testing.T) {
	socket, shutdowns := serve(t)
	status, body := post(t, socket, "/op/list", "v0", map[string]any{"kind": "tasks"})
	if status != http.StatusConflict || body["error"].(map[string]any)["kind"] != "version_mismatch" {
		t.Fatalf("got %d %v", status, body)
	}
	if shutdowns.Load() != 1 {
		t.Fatalf("want 1 shutdown, got %d", shutdowns.Load())
	}
}

func TestStatusIgnoresVersion(t *testing.T) {
	socket, shutdowns := serve(t)
	st, err := rpc.NewClient(socket, "some-other-version", nil).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != "v1" || shutdowns.Load() != 0 {
		t.Fatalf("got %+v, %d shutdowns", st, shutdowns.Load())
	}
}

// Every app.Service method must be reachable through rpc.Client: a missing
// handle(...) line or a misspelled route name fails here. Zero-value input is
// fine; invalid-input errors prove the route exists.
func TestEveryOperationHasARoute(t *testing.T) {
	socket, _ := serve(t)
	client := reflect.ValueOf(rpc.NewClient(socket, "v1", nil))
	svc := reflect.TypeFor[app.Service]()
	for i := range svc.NumMethod() {
		m := svc.Method(i)
		t.Run(m.Name, func(t *testing.T) {
			in := reflect.New(m.Type.In(1)).Elem()
			res := client.MethodByName(m.Name).Call([]reflect.Value{reflect.ValueOf(context.Background()), in})
			if err, _ := res[1].Interface().(error); err != nil && strings.Contains(err.Error(), "unexpected status") {
				t.Fatalf("no route for %s: %v", m.Name, err)
			}
		})
	}
}

func TestUnavailableKeepsItsKind(t *testing.T) {
	fake := embed.NewFake("m")
	fake.Fail(fmt.Errorf("ollama down: %w", domain.ErrUnavailable))
	a := testutil.NewAppWithEmbedder(t, fake)
	if _, err := a.Remember(context.Background(), app.RememberInput{Content: "x"}); err != nil {
		t.Fatal(err)
	}
	socket, _ := serveApp(t, a)
	_, err := rpc.NewClient(socket, "v1", nil).Embed(context.Background(), app.EmbedInput{})
	if !errors.Is(err, domain.ErrUnavailable) || strings.Count(err.Error(), "unavailable") != 1 {
		t.Fatalf("got %v", err)
	}
}
