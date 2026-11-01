package embed_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/embed"
)

func TestOllamaEmbed(t *testing.T) {
	var got struct {
		Model    string   `json:"model"`
		Input    []string `json:"input"`
		Truncate *bool    `json:"truncate"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/embed" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		vecs := make([][]float32, len(got.Input))
		for i := range vecs {
			vecs[i] = []float32{float32(i), 0.5}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": got.Model, "embeddings": vecs})
	}))
	defer srv.Close()

	o := embed.NewOllama(embed.OllamaConfig{
		URL: srv.URL + "/", Model: "nomic-embed-text",
		DocumentPrefix: "search_document: ", QueryPrefix: "search_query: ",
	})
	if o.Model() != "nomic-embed-text" {
		t.Fatalf("model %q", o.Model())
	}

	t.Run("documents get the document prefix and ask for truncation", func(t *testing.T) {
		vecs, err := o.Embed(context.Background(), embed.Document, []string{"a", "b"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Model != "nomic-embed-text" || strings.Join(got.Input, "|") != "search_document: a|search_document: b" ||
			got.Truncate == nil || !*got.Truncate {
			t.Fatalf("request %+v", got)
		}
		if len(vecs) != 2 || vecs[1][0] != 1 || vecs[1][1] != 0.5 {
			t.Fatalf("vectors %v", vecs)
		}
	})

	t.Run("queries get the query prefix", func(t *testing.T) {
		if _, err := o.Embed(context.Background(), embed.Query, []string{"q"}); err != nil {
			t.Fatal(err)
		}
		if strings.Join(got.Input, "|") != "search_query: q" {
			t.Fatalf("request %+v", got)
		}
	})
}

func TestOllamaErrors(t *testing.T) {
	t.Run("connection refused is unavailable", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		_ = ln.Close()
		o := embed.NewOllama(embed.OllamaConfig{URL: "http://" + addr, Model: "m"})
		_, err = o.Embed(context.Background(), embed.Document, []string{"a"})
		if !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("want ErrUnavailable, got %v", err)
		}
	})

	t.Run("busy server (503) is unavailable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"error":"server busy"}`, http.StatusServiceUnavailable)
		}))
		defer srv.Close()
		o := embed.NewOllama(embed.OllamaConfig{URL: srv.URL, Model: "m"})
		_, err := o.Embed(context.Background(), embed.Document, []string{"a"})
		if !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("want ErrUnavailable, got %v", err)
		}
	})

	t.Run("timeout is unavailable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			time.Sleep(200 * time.Millisecond)
		}))
		defer srv.Close()
		o := embed.NewOllama(embed.OllamaConfig{URL: srv.URL, Model: "m", Timeout: 20 * time.Millisecond})
		_, err := o.Embed(context.Background(), embed.Document, []string{"a"})
		if !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("want ErrUnavailable, got %v", err)
		}
	})

	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{
			name: "missing model is a readable error, not unavailable",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
			},
			want: "ollama pull m",
		},
		{
			name: "server error carries the status",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "boom", http.StatusInternalServerError)
			},
			want: "500",
		},
		{
			name: "count mismatch is an error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"embeddings":[[1]]}`))
			},
			want: "1 embeddings for 2 texts",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			o := embed.NewOllama(embed.OllamaConfig{URL: srv.URL, Model: "m"})
			_, err := o.Embed(context.Background(), embed.Document, []string{"a", "b"})
			if err == nil || errors.Is(err, domain.ErrUnavailable) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestFake(t *testing.T) {
	f := embed.NewFake("fake")
	f.Set("x", []float32{1, 0, 0})
	vecs, err := f.Embed(context.Background(), embed.Document, []string{"x", "y"})
	if err != nil || vecs[0][0] != 1 || vecs[1][2] != 1 {
		t.Fatalf("got %v, %v", vecs, err)
	}
	f.Fail(domain.ErrUnavailable)
	if _, err := f.Embed(context.Background(), embed.Query, []string{"x"}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
	if len(f.Calls()) != 2 {
		t.Fatalf("calls %v", f.Calls())
	}
}
