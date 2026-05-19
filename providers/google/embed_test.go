package google

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewEmbedder_RequiresAuth(t *testing.T) {
	t.Parallel()
	if _, err := NewEmbedder(EmbedderConfig{}); err == nil {
		t.Fatal("expected error when neither APIKey nor HTTPClient is set")
	}
	// Providing a custom client is the Vertex AI escape hatch.
	if _, err := NewEmbedder(EmbedderConfig{HTTPClient: &http.Client{}}); err != nil {
		t.Errorf("HTTPClient-only config should be accepted: %v", err)
	}
}

func TestNewEmbedder_DefaultsApplied(t *testing.T) {
	t.Parallel()
	e, err := NewEmbedder(EmbedderConfig{APIKey: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if e.Model() != DefaultEmbeddingModel {
		t.Errorf("Model = %q", e.Model())
	}
	if e.Dimensions() != 768 {
		t.Errorf("default Dimensions = %d, want 768", e.Dimensions())
	}
}

func TestEmbed_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ":batchEmbedContents") {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("key") != "test-key" {
			t.Errorf("key query param missing: %q", r.URL.RawQuery)
		}
		var body embedBatchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Requests) != 2 {
			t.Errorf("Requests len = %d", len(body.Requests))
		}
		if !strings.HasPrefix(body.Requests[0].Model, "models/") {
			t.Errorf("model path should start with models/: %q", body.Requests[0].Model)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{
			"embeddings": [
				{"values": [0.1, 0.2, 0.3]},
				{"values": [0.4, 0.5, 0.6]}
			]
		}`)
	}))
	defer srv.Close()

	e, _ := NewEmbedder(EmbedderConfig{APIKey: "test-key", BaseURL: srv.URL})
	vecs, err := e.Embed(context.Background(), []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 || vecs[0][0] != 0.1 || vecs[1][2] != 0.6 {
		t.Errorf("vecs = %+v", vecs)
	}
	if e.Dimensions() != 3 {
		t.Errorf("Dimensions after Embed = %d, want 3", e.Dimensions())
	}
}

func TestEmbed_ForwardsOutputDimensionality(t *testing.T) {
	t.Parallel()
	var seen embedBatchRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seen)
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"embeddings":[{"values":[0,0,0]}]}`)
	}))
	defer srv.Close()

	e, _ := NewEmbedder(EmbedderConfig{APIKey: "x", BaseURL: srv.URL, Dim: 256})
	if _, err := e.Embed(context.Background(), []string{"hi"}); err != nil {
		t.Fatal(err)
	}
	if seen.Requests[0].OutputDimensionality != 256 {
		t.Errorf("outputDimensionality = %d, want 256", seen.Requests[0].OutputDimensionality)
	}
}

func TestEmbed_ModelPathPrefixedAutomatically(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "models/text-embedding-004") {
			t.Errorf("url path should embed model path: %q", r.URL.Path)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"embeddings":[{"values":[1]}]}`)
	}))
	defer srv.Close()

	e, _ := NewEmbedder(EmbedderConfig{APIKey: "x", BaseURL: srv.URL, Model: "text-embedding-004"})
	if _, err := e.Embed(context.Background(), []string{"hi"}); err != nil {
		t.Fatal(err)
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
		t.Errorf("expected empty, got %+v", vecs)
	}
}

func TestEmbed_PropagatesAPIError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"code":403,"message":"permission denied","status":"PERMISSION_DENIED"}}`)
	}))
	defer srv.Close()

	e, _ := NewEmbedder(EmbedderConfig{APIKey: "bad", BaseURL: srv.URL})
	_, err := e.Embed(context.Background(), []string{"hi"})
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error message = %v", err)
	}
}
