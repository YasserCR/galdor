//go:build integration

package qdrant_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/YasserCR/galdor/memory/qdrant"
	"github.com/YasserCR/galdor/pkg/memory"
)

// These tests hit a real Qdrant instance. They run only under the
// "integration" build tag AND when GALDOR_QDRANT_URL is set. Optional
// GALDOR_QDRANT_API_KEY enables auth for Qdrant Cloud.
//
//	export GALDOR_QDRANT_URL='http://localhost:6333'
//	go test -tags=integration ./memory/qdrant/...
//
// A throw-away container works:
//
//	docker run --rm -p 6333:6333 qdrant/qdrant
//
// Each test uses a unique collection so concurrent runs don't collide.

func newIntegrationStore(t *testing.T, collection string) *qdrant.Store {
	t.Helper()
	base := os.Getenv("GALDOR_QDRANT_URL")
	if base == "" {
		t.Skip("GALDOR_QDRANT_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := qdrant.Open(ctx, qdrant.Config{
		URL:        base,
		APIKey:     os.Getenv("GALDOR_QDRANT_API_KEY"),
		Collection: collection,
		Dim:        4,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.Delete(context.Background(), "test_doc")
		_ = s.Close()
	})
	return s
}

func TestIntegration_AddAndRetrieve(t *testing.T) {
	s := newIntegrationStore(t, "galdor_chunks_t1")
	ctx := context.Background()
	chunks := []memory.Chunk{
		{ID: "n1", DocumentID: "test_doc", Index: 0, Text: "near", Embedding: []float32{1, 0, 0, 0}},
		{ID: "n2", DocumentID: "test_doc", Index: 1, Text: "mid", Embedding: []float32{0, 1, 0, 0}},
		{ID: "n3", DocumentID: "test_doc", Index: 2, Text: "far", Embedding: []float32{-1, 0, 0, 0}},
	}
	if err := s.Add(ctx, chunks); err != nil {
		t.Fatal(err)
	}
	res, err := s.Retrieve(ctx, memory.Query{Embedding: []float32{1, 0, 0, 0}, K: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Fatalf("got %d results, want 3", len(res))
	}
	// Qdrant's cosine score: identical vectors → 1.0
	if res[0].Score < 0.99 {
		t.Errorf("top score = %v, want ~1.0", res[0].Score)
	}
	if res[0].Chunk.DocumentID != "test_doc" || res[0].Chunk.Text != "near" {
		t.Errorf("top chunk = %+v", res[0].Chunk)
	}
}

func TestIntegration_UpsertReplacesPoint(t *testing.T) {
	s := newIntegrationStore(t, "galdor_chunks_t2")
	ctx := context.Background()
	first := memory.Chunk{ID: "x", DocumentID: "test_doc", Text: "v1", Embedding: []float32{1, 0, 0, 0}}
	if err := s.Add(ctx, []memory.Chunk{first}); err != nil {
		t.Fatal(err)
	}
	second := memory.Chunk{ID: "x", DocumentID: "test_doc", Text: "v2", Embedding: []float32{0, 1, 0, 0}}
	if err := s.Add(ctx, []memory.Chunk{second}); err != nil {
		t.Fatal(err)
	}
	n, _ := s.Len(ctx)
	if n != 1 {
		t.Errorf("Len after upsert = %d, want 1 (same Chunk.ID must dedupe)", n)
	}
}

func TestIntegration_MetadataFilter(t *testing.T) {
	s := newIntegrationStore(t, "galdor_chunks_t3")
	ctx := context.Background()
	_ = s.Add(ctx, []memory.Chunk{
		{ID: "en", DocumentID: "test_doc", Text: "english", Embedding: []float32{1, 0, 0, 0}, Metadata: map[string]string{"lang": "en"}},
		{ID: "es", DocumentID: "test_doc", Text: "spanish", Embedding: []float32{1, 0, 0, 0}, Metadata: map[string]string{"lang": "es"}},
	})
	res, err := s.Retrieve(ctx, memory.Query{
		Embedding: []float32{1, 0, 0, 0},
		K:         5,
		Filter:    map[string]string{"lang": "es"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Chunk.Metadata["lang"] != "es" {
		t.Fatalf("filter not applied: %+v", res)
	}
}

func TestIntegration_DeleteByDocument(t *testing.T) {
	s := newIntegrationStore(t, "galdor_chunks_t4")
	ctx := context.Background()
	_ = s.Add(ctx, []memory.Chunk{
		{ID: "a", DocumentID: "test_doc", Text: "x", Embedding: []float32{1, 0, 0, 0}},
		{ID: "b", DocumentID: "test_doc", Text: "y", Embedding: []float32{0, 1, 0, 0}},
	})
	if err := s.Delete(ctx, "test_doc"); err != nil {
		t.Fatal(err)
	}
	n, _ := s.Len(ctx)
	if n != 0 {
		t.Errorf("Len after Delete = %d, want 0", n)
	}
}
