package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/OrestisStefanou/Loci/internal/app"
)

// Client implements app.Service by forwarding each operation to loci serve.
// Add one method per operation, each a single call to call().
type Client struct {
	socket  string
	version string
	start   func(context.Context) error
	http    *http.Client

	// StartTimeout bounds how long the client waits for a server it started
	// (or one that is shutting down) before giving up.
	StartTimeout time.Duration
}

var _ app.Service = (*Client)(nil)

// NewClient returns a client for the server on socket. If start is non-nil,
// the client calls it to launch a server when none is reachable or the
// running one has a different version, then retries once. Pass nil for
// commands that must not start a server (status, stop).
func NewClient(socket, version string, start func(context.Context) error) *Client {
	return &Client{
		socket:  socket,
		version: version,
		start:   start,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socket)
				},
			},
		},
		StartTimeout: 5 * time.Second,
	}
}

func (c *Client) SetTaskProject(ctx context.Context, in app.SetTaskProjectInput) (app.SetTaskProjectOutput, error) {
	return call[app.SetTaskProjectOutput](ctx, c, "set_task_project", in)
}

func (c *Client) List(ctx context.Context, in app.ListInput) (app.ListOutput, error) {
	return call[app.ListOutput](ctx, c, "list", in)
}

func (c *Client) Remember(ctx context.Context, in app.RememberInput) (app.RememberOutput, error) {
	return call[app.RememberOutput](ctx, c, "remember", in)
}

func (c *Client) SearchMemories(ctx context.Context, in app.SearchMemoriesInput) (app.SearchMemoriesOutput, error) {
	return call[app.SearchMemoriesOutput](ctx, c, "search_memories", in)
}

func (c *Client) Embed(ctx context.Context, in app.EmbedInput) (app.EmbedOutput, error) {
	return call[app.EmbedOutput](ctx, c, "embed", in)
}

func (c *Client) Mark(ctx context.Context, in app.MarkInput) (app.MarkOutput, error) {
	return call[app.MarkOutput](ctx, c, "mark", in)
}

func (c *Client) Attach(ctx context.Context, in app.AttachInput) (app.AttachOutput, error) {
	return call[app.AttachOutput](ctx, c, "attach", in)
}

func (c *Client) CaptureStart(ctx context.Context, in app.CaptureStartInput) (app.CaptureStartOutput, error) {
	return call[app.CaptureStartOutput](ctx, c, "capture_start", in)
}

func (c *Client) CaptureMessage(ctx context.Context, in app.CaptureMessageInput) (app.CaptureMessageOutput, error) {
	return call[app.CaptureMessageOutput](ctx, c, "capture_message", in)
}

func (c *Client) SearchConversations(ctx context.Context, in app.SearchConversationsInput) (app.SearchConversationsOutput, error) {
	return call[app.SearchConversationsOutput](ctx, c, "search_conversations", in)
}

func (c *Client) GetConversation(ctx context.Context, in app.GetConversationInput) (app.GetConversationOutput, error) {
	return call[app.GetConversationOutput](ctx, c, "get_conversation", in)
}

// Status reports the running server, or ErrServerNotRunning. It never starts one.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var st Status
	err := c.roundTrip(ctx, http.MethodGet, "/status", nil, &st)
	return st, err
}

// Stop asks the server to shut down and waits until it has. It returns
// ErrServerNotRunning if none is running.
func (c *Client) Stop(ctx context.Context) error {
	if err := c.roundTrip(ctx, http.MethodPost, "/shutdown", nil, nil); err != nil {
		return err
	}
	return c.waitGone(ctx)
}

func call[Out any](ctx context.Context, c *Client, name string, in any) (Out, error) {
	var out Out
	body, err := json.Marshal(in)
	if err != nil {
		return out, fmt.Errorf("encode %s input: %w", name, err)
	}
	err = c.roundTrip(ctx, http.MethodPost, "/op/"+name, body, &out)
	needsServer := errors.Is(err, ErrServerNotRunning) || errors.Is(err, errVersionMismatch)
	if c.start == nil || !needsServer {
		return out, err
	}

	// No server, or a stale one that is now shutting down: start ours, retry once.
	if errors.Is(err, errVersionMismatch) {
		if err := c.waitGone(ctx); err != nil {
			return out, err
		}
	}
	if err := c.start(ctx); err != nil {
		return out, fmt.Errorf("start loci serve: %w", err)
	}
	if err := c.waitUp(ctx); err != nil {
		return out, err
	}
	return out, c.roundTrip(ctx, http.MethodPost, "/op/"+name, body, &out)
}

func (c *Client) roundTrip(ctx context.Context, method, path string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://loci"+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set(VersionHeader, c.version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if isUnreachable(err) {
			return fmt.Errorf("%w (socket %s)", ErrServerNotRunning, c.socket)
		}
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		var eb errorBody
		if err := json.NewDecoder(resp.Body).Decode(&eb); err != nil || eb.Error.Kind == "" {
			return fmt.Errorf("loci serve: unexpected status %s", resp.Status)
		}
		return eb.Error.toError()
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *Client) waitUp(ctx context.Context) error {
	return c.poll(ctx, func() bool { return c.reachable(ctx) },
		"loci serve did not start within %s")
}

func (c *Client) waitGone(ctx context.Context) error {
	return c.poll(ctx, func() bool { return !c.reachable(ctx) },
		"loci serve did not stop within %s")
}

func (c *Client) poll(ctx context.Context, done func() bool, timeoutMsg string) error {
	deadline := time.Now().Add(c.StartTimeout)
	for !done() {
		if time.Now().After(deadline) {
			return fmt.Errorf(timeoutMsg, c.StartTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil
}

func (c *Client) reachable(ctx context.Context) bool {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func isUnreachable(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) || errors.Is(err, os.ErrNotExist)
}
