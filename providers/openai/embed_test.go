package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewEmbedder_RequiresAPIKey(t *testing.T) {
	t.Parallel()
	if _, err := NewEmbedder(EmbedderConfig{}); err == nil {
		t.Fatal("expected error for empty APIKey")
	}
}

func TestNewEmbedder_DefaultsApplied(t *testing.T) {
	t.Parallel()
	e, err := NewEmbedder(EmbedderConfig{APIKey: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if e.Model() != DefaultEmbeddingModel {
		t.Errorf("Model = %q, want %q", e.Model(), DefaultEmbeddingModel)
	}
	if e.Dimensions() != 1536 {
		t.Errorf("default Dimensions = %d, want 1536 (text-embedding-3-small)", e.Dimensions())
	}
}

func TestEmbedder_DimensionsForLargeModel(t *testing.T) {
	t.Parallel()
	e, _ := NewEmbedder(EmbedderConfig{APIKey: "x", Model: "text-embedding-3-large"})
	if e.Dimensions() != 3072 {
		t.Errorf("Dimensions = %d, want 3072", e.Dimensions())
	}
}

func TestEmbed_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		var body embedRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != DefaultEmbeddingModel {
			t.Errorf("Model in request = %q", body.Model)
		}
		if len(body.Input) != 2 {
			t.Errorf("Input len = %d", len(body.Input))
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{
			"data": [
				{"index": 0, "embedding": [0.1, 0.2, 0.3]},
				{"index": 1, "embedding": [0.4, 0.5, 0.6]}
			],
			"model": "text-embedding-3-small",
			"usage": {"prompt_tokens": 8, "total_tokens": 8}
		}`)
	}))
	defer srv.Close()

	e, _ := NewEmbedder(EmbedderConfig{APIKey: "test-key", BaseURL: srv.URL})
	vecs, err := e.Embed(context.Background(), []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 {
		t.Fatalf("vec count = %d, want 2", len(vecs))
	}
	if vecs[0][0] != 0.1 || vecs[1][2] != 0.6 {
		t.Errorf("values = %+v", vecs)
	}
	// Dimensions() should now reflect the observed dim.
	if e.Dimensions() != 3 {
		t.Errorf("Dimensions after Embed = %d, want 3", e.Dimensions())
	}
}

func TestEmbed_HonorsResponseIndexOrdering(t *testing.T) {
	t.Parallel()
	// API returns vectors out of order; the adapter must reorder.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{
			"data": [
				{"index": 1, "embedding": [9.0]},
				{"index": 0, "embedding": [1.0]}
			]
		}`)
	}))
	defer srv.Close()

	e, _ := NewEmbedder(EmbedderConfig{APIKey: "x", BaseURL: srv.URL})
	vecs, err := e.Embed(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if vecs[0][0] != 1.0 || vecs[1][0] != 9.0 {
		t.Errorf("ordering not honored: %+v", vecs)
	}
}

func TestEmbed_ForwardsDimensions(t *testing.T) {
	t.Parallel()
	var seen embedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seen)
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[0,0,0]}]}`)
	}))
	defer srv.Close()

	e, _ := NewEmbedder(EmbedderConfig{APIKey: "x", BaseURL: srv.URL, Dim: 256})
	if _, err := e.Embed(context.Background(), []string{"hi"}); err != nil {
		t.Fatal(err)
	}
	if seen.Dimensions != 256 {
		t.Errorf("dimensions field = %d, want 256", seen.Dimensions)
	}
}

func TestEmbed_EmptyInputReturnsNil(t *testing.T) {
	t.Parallel()
	e, _ := NewEmbedder(EmbedderConfig{APIKey: "x"})
	vecs, err := e.Embed(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 0 {
		t.Errorf("vecs = %+v, want empty", vecs)
	}
}

func TestEmbed_PropagatesAPIError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid api key","type":"authentication_error"}}`)
	}))
	defer srv.Close()

	e, _ := NewEmbedder(EmbedderConfig{APIKey: "bad", BaseURL: srv.URL})
	_, err := e.Embed(context.Background(), []string{"hi"})
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("error message = %v", err)
	}
}
