package embedder

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewHTTPEmbedder_validation(t *testing.T) {
	if _, err := NewHTTPEmbedder(HTTPConfig{}); err == nil {
		t.Fatal("expected error on empty URL")
	}
	if _, err := NewHTTPEmbedder(HTTPConfig{URL: "http://x", Shape: Shape("nope")}); err == nil {
		t.Fatal("expected error on unknown shape")
	}
	e, err := NewHTTPEmbedder(HTTPConfig{URL: "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	if e.shape != ShapeOpenAI {
		t.Fatalf("default shape: got %q want %q", e.shape, ShapeOpenAI)
	}
	if e.batchSize != defaultBatchSize {
		t.Fatalf("default batch: got %d want %d", e.batchSize, defaultBatchSize)
	}
	if !strings.HasSuffix(e.url, "/embeddings") {
		t.Fatalf("openai url should suffix /embeddings: %s", e.url)
	}

	// TEI URL suffix handling.
	tei, _ := NewHTTPEmbedder(HTTPConfig{URL: "http://x/", Shape: ShapeTEI})
	if !strings.HasSuffix(tei.url, "/embed") {
		t.Fatalf("tei url: %s", tei.url)
	}
	// Idempotent suffix.
	already, _ := NewHTTPEmbedder(HTTPConfig{URL: "http://x/v1/embeddings"})
	if already.url != "http://x/v1/embeddings" {
		t.Fatalf("url should not get double suffix: %s", already.url)
	}
}

func TestEmbed_TEIShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" {
			t.Errorf("path: %s", r.URL.Path)
		}
		var body struct {
			Inputs []string `json:"inputs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		out := make([][]float32, len(body.Inputs))
		for i := range body.Inputs {
			out[i] = []float32{float32(i), 0.5, -0.5}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	e, err := NewHTTPEmbedder(HTTPConfig{URL: srv.URL, Shape: ShapeTEI})
	if err != nil {
		t.Fatal(err)
	}
	vecs, err := e.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 || vecs[0][0] != 0 || vecs[1][0] != 1 {
		t.Fatalf("bad vectors: %v", vecs)
	}
	if e.Dimensions() != 3 {
		t.Fatalf("dim should auto-detect: got %d", e.Dimensions())
	}
}

func TestEmbed_OpenAIShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/embeddings") {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer SECRET" {
			t.Errorf("auth header: %q", r.Header.Get("Authorization"))
		}
		var body struct {
			Input []string `json:"input"`
			Model string   `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "m-1" {
			t.Errorf("model: %s", body.Model)
		}
		// Return data in shuffled order to exercise index-honoring decode.
		type item struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		data := []item{
			{Index: 1, Embedding: []float32{1, 1}},
			{Index: 0, Embedding: []float32{0, 0}},
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()

	e, _ := NewHTTPEmbedder(HTTPConfig{URL: srv.URL + "/v1/embeddings", Model: "m-1", APIKey: "SECRET"})
	vecs, err := e.Embed(context.Background(), []string{"x", "y"})
	if err != nil {
		t.Fatal(err)
	}
	if vecs[0][0] != 0 || vecs[1][0] != 1 {
		t.Fatalf("ordering not honored: %v", vecs)
	}
}

func TestEmbed_RetryOn5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "upstream down")
			return
		}
		_, _ = io.WriteString(w, `[[1,2,3]]`)
	}))
	defer srv.Close()

	e, _ := NewHTTPEmbedder(HTTPConfig{URL: srv.URL, Shape: ShapeTEI})
	vecs, err := e.Embed(context.Background(), []string{"x"})
	if err != nil {
		t.Fatalf("expected success after retry: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls = %d, want 3", got)
	}
	if len(vecs) != 1 || len(vecs[0]) != 3 {
		t.Fatalf("vecs: %v", vecs)
	}
}

func TestEmbed_ExhaustRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "still down")
	}))
	defer srv.Close()

	e, _ := NewHTTPEmbedder(HTTPConfig{URL: srv.URL, Shape: ShapeTEI})
	_, err := e.Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("expected error")
	}
	var ee *EmbedError
	if !errors.As(err, &ee) {
		t.Fatalf("expected *EmbedError, got %T: %v", err, err)
	}
	if ee.Status != http.StatusServiceUnavailable {
		t.Fatalf("status: %d", ee.Status)
	}
	if !strings.Contains(ee.Body, "still down") {
		t.Fatalf("body: %q", ee.Body)
	}
	if got := atomic.LoadInt32(&calls); got != int32(defaultMaxRetries) {
		t.Fatalf("calls = %d, want %d", got, defaultMaxRetries)
	}
}

func TestEmbed_NoRetryOn4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"bad key"}`)
	}))
	defer srv.Close()

	e, _ := NewHTTPEmbedder(HTTPConfig{URL: srv.URL, Shape: ShapeTEI})
	_, err := e.Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("expected error")
	}
	var ee *EmbedError
	if !errors.As(err, &ee) {
		t.Fatalf("expected *EmbedError, got %T", err)
	}
	if ee.Status != http.StatusUnauthorized {
		t.Fatalf("status: %d", ee.Status)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want 1 (no retry on 4xx)", got)
	}
}

func TestEmbed_413NoRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = io.WriteString(w, "payload too large")
	}))
	defer srv.Close()

	e, _ := NewHTTPEmbedder(HTTPConfig{URL: srv.URL, Shape: ShapeTEI})
	_, err := e.Embed(context.Background(), []string{"x"})
	var ee *EmbedError
	if !errors.As(err, &ee) || ee.Status != 413 {
		t.Fatalf("expected EmbedError 413, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}

func TestEmbed_ContextCancel(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)

	e, _ := NewHTTPEmbedder(HTTPConfig{URL: srv.URL, Shape: ShapeTEI})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := e.Embed(ctx, []string{"x"})
	if err == nil {
		t.Fatal("expected error on cancel")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEmbed_BatchChunking(t *testing.T) {
	var batches int32
	var mu sync.Mutex
	var seen [][]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&batches, 1)
		var body struct {
			Inputs []string `json:"inputs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		seen = append(seen, append([]string(nil), body.Inputs...))
		mu.Unlock()
		out := make([][]float32, len(body.Inputs))
		for i, s := range body.Inputs {
			out[i] = []float32{float32(len(s))}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	e, _ := NewHTTPEmbedder(HTTPConfig{URL: srv.URL, Shape: ShapeTEI, BatchSize: 2})
	inputs := []string{"a", "bb", "ccc", "dddd", "eeeee"}
	vecs, err := e.Embed(context.Background(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&batches); got != 3 {
		t.Fatalf("batches = %d, want 3", got)
	}
	if len(vecs) != len(inputs) {
		t.Fatalf("len: %d", len(vecs))
	}
	for i, v := range vecs {
		if v[0] != float32(len(inputs[i])) {
			t.Fatalf("order broken at %d: %v", i, v)
		}
	}
}

func TestEmbed_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "not json")
	}))
	defer srv.Close()
	e, _ := NewHTTPEmbedder(HTTPConfig{URL: srv.URL, Shape: ShapeTEI})
	_, err := e.Embed(context.Background(), []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestEmbed_VectorCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[[1,2]]`)
	}))
	defer srv.Close()
	e, _ := NewHTTPEmbedder(HTTPConfig{URL: srv.URL, Shape: ShapeTEI})
	_, err := e.Embed(context.Background(), []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "vectors for") {
		t.Fatalf("expected count mismatch, got %v", err)
	}
}

func TestPing(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Inputs []string `json:"inputs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Inputs) == 1 {
			seen = body.Inputs[0]
		}
		_, _ = io.WriteString(w, `[[0.1,0.2]]`)
	}))
	defer srv.Close()

	e, _ := NewHTTPEmbedder(HTTPConfig{URL: srv.URL, Shape: ShapeTEI})
	if err := e.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen != "ping" {
		t.Fatalf("ping payload: %q", seen)
	}
}

func TestPing_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	e, _ := NewHTTPEmbedder(HTTPConfig{URL: srv.URL, Shape: ShapeTEI})
	if err := e.Ping(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestEmbed_EmptyInput(t *testing.T) {
	e, _ := NewHTTPEmbedder(HTTPConfig{URL: "http://unused", Shape: ShapeTEI})
	vecs, err := e.Embed(context.Background(), nil)
	if err != nil || vecs != nil {
		t.Fatalf("expected (nil, nil), got (%v, %v)", vecs, err)
	}
}

func TestEmbedError_Format(t *testing.T) {
	e := &EmbedError{Status: 500, URL: "http://x", Body: "boom"}
	if !strings.Contains(e.Error(), "500") || !strings.Contains(e.Error(), "boom") {
		t.Fatalf("error format: %q", e.Error())
	}
	e2 := &EmbedError{Status: 500, URL: "http://x"}
	if strings.Contains(e2.Error(), ":") && strings.HasSuffix(e2.Error(), ":") {
		t.Fatalf("trailing colon: %q", e2.Error())
	}
}

func TestSnippet_Truncates(t *testing.T) {
	long := strings.Repeat("a", bodySnippetLimit+200)
	got := snippet([]byte(long))
	if len(got) != bodySnippetLimit {
		t.Fatalf("snippet len = %d, want %d", len(got), bodySnippetLimit)
	}
}
