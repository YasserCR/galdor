package qdrant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/memory"
)

func TestUUIDFromString_Deterministic(t *testing.T) {
	t.Parallel()
	a := uuidFromString("doc-1#0")
	b := uuidFromString("doc-1#0")
	if a != b {
		t.Errorf("non-deterministic: %q vs %q", a, b)
	}
	if a == uuidFromString("doc-1#1") {
		t.Error("different inputs hashed to the same UUID")
	}
}

func TestUUIDFromString_Shape(t *testing.T) {
	t.Parallel()
	u := uuidFromString("anything")
	if len(u) != 36 {
		t.Errorf("len = %d, want 36", len(u))
	}
	if u[8] != '-' || u[13] != '-' || u[18] != '-' || u[23] != '-' {
		t.Errorf("UUID shape wrong: %q", u)
	}
}

func TestBuildSearchBody_NoFilter(t *testing.T) {
	t.Parallel()
	body := buildSearchBody([]float32{1, 0, 0}, 5, nil)
	if body["limit"] != 5 {
		t.Errorf("limit = %v", body["limit"])
	}
	if _, ok := body["filter"]; ok {
		t.Errorf("filter should be omitted when empty: %+v", body)
	}
	if body["with_payload"] != true {
		t.Error("with_payload must be true")
	}
}

func TestBuildSearchBody_WithFilter(t *testing.T) {
	t.Parallel()
	body := buildSearchBody([]float32{1, 0, 0}, 3, map[string]string{"lang": "es"})
	f, ok := body["filter"].(map[string]any)
	if !ok {
		t.Fatalf("filter missing or wrong shape: %+v", body["filter"])
	}
	must, ok := f["must"].([]any)
	if !ok || len(must) != 1 {
		t.Fatalf("must clause wrong: %+v", f)
	}
	clause := must[0].(map[string]any)
	if clause["key"] != "lang" {
		t.Errorf("key = %v", clause["key"])
	}
}

func TestChunkFromPayload_RoundTrip(t *testing.T) {
	t.Parallel()
	payload := map[string]any{
		payloadKeyDocumentID: "doc1",
		payloadKeyIndex:      float64(3),
		payloadKeyText:       "hello",
		"lang":               "es",
		"source":             "wiki",
		"ignored_non_string": 42,
	}
	c := chunkFromPayload(payload)
	if c.DocumentID != "doc1" || c.Index != 3 || c.Text != "hello" {
		t.Errorf("chunk fields = %+v", c)
	}
	if c.Metadata["lang"] != "es" || c.Metadata["source"] != "wiki" {
		t.Errorf("metadata = %+v", c.Metadata)
	}
	if _, has := c.Metadata["ignored_non_string"]; has {
		t.Error("non-string payload value should not appear in metadata")
	}
}

// TestOpen_CreatesCollectionWhenMissing uses an httptest server to
// simulate Qdrant's REST surface so we can validate the Open flow
// (probe → 404 → create) without a real container.
func TestOpen_CreatesCollectionWhenMissing(t *testing.T) {
	t.Parallel()
	var (
		sawProbe  bool
		sawCreate bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if strings.HasSuffix(r.URL.Path, "/collections/test") {
				sawProbe = true
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"status":"not found"}`)
				return
			}
		case http.MethodPut:
			if strings.HasSuffix(r.URL.Path, "/collections/test") {
				sawCreate = true
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				vecs, _ := body["vectors"].(map[string]any)
				if vecs["distance"] != "Cosine" {
					t.Errorf("distance = %v", vecs["distance"])
				}
				if int(vecs["size"].(float64)) != 4 {
					t.Errorf("size = %v", vecs["size"])
				}
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{"result":true,"status":"ok"}`)
				return
			}
		}
		w.WriteHeader(http.StatusNotImplemented)
	}))
	defer srv.Close()

	s, err := Open(context.Background(), Config{URL: srv.URL, Collection: "test", Dim: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !sawProbe {
		t.Error("Open should probe the collection first")
	}
	if !sawCreate {
		t.Error("Open should create the collection on 404")
	}
}

func TestOpen_NoCreateWhenCollectionExists(t *testing.T) {
	t.Parallel()
	var createHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"result":{"points_count":0},"status":"ok"}`)
			return
		}
		if r.Method == http.MethodPut {
			createHit = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s, err := Open(context.Background(), Config{URL: srv.URL, Collection: "exists", Dim: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if createHit {
		t.Error("Open must not create an existing collection")
	}
}

func TestAdd_ValidatesDimAndID(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	defer srv.Close()

	s, err := Open(context.Background(), Config{URL: srv.URL, Dim: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.Add(context.Background(), []memory.Chunk{{DocumentID: "d", Embedding: []float32{1, 0, 0, 0}}})
	if err == nil {
		t.Error("expected error for empty Chunk.ID")
	}
	err = s.Add(context.Background(), []memory.Chunk{{ID: "x", DocumentID: "d", Embedding: []float32{1, 0}}})
	if err == nil {
		t.Error("expected dim-mismatch error")
	}
}

func TestRetrieve_RejectsEmptyEmbedding(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	s, _ := Open(context.Background(), Config{URL: srv.URL, Dim: 4})
	defer s.Close()
	if _, err := s.Retrieve(context.Background(), memory.Query{Text: "hi"}); err == nil {
		t.Fatal("expected error for text-only query")
	}
}
