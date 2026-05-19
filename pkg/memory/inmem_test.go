package memory_test

import (
	"context"
	"testing"

	"github.com/YasserCR/galdor/pkg/memory"
)

func TestInMemoryStore_TextRetrieval(t *testing.T) {
	t.Parallel()
	s := memory.NewInMemoryStore()
	chunks := []memory.Chunk{
		{ID: "c1", DocumentID: "d1", Text: "Quito is the capital of Ecuador."},
		{ID: "c2", DocumentID: "d1", Text: "Bogotá is the capital of Colombia."},
		{ID: "c3", DocumentID: "d1", Text: "Lima is the capital of Peru."},
	}
	if err := s.Add(context.Background(), chunks); err != nil {
		t.Fatal(err)
	}
	res, err := s.Retrieve(context.Background(), memory.Query{Text: "capital Ecuador", K: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("expected at least one result for 'capital Ecuador'")
	}
	if res[0].Chunk.ID != "c1" {
		t.Errorf("top hit = %q, want c1", res[0].Chunk.ID)
	}
}

func TestInMemoryStore_EmbeddingRetrieval(t *testing.T) {
	t.Parallel()
	s := memory.NewInMemoryStore()
	// Vectors are 2-D for easy hand-computation: cosine of (1,0) and
	// (0.9,0.1) > cosine of (1,0) and (-1,0).
	chunks := []memory.Chunk{
		{ID: "near", DocumentID: "d", Text: "x", Embedding: []float32{0.9, 0.1}},
		{ID: "mid", DocumentID: "d", Text: "y", Embedding: []float32{0, 1}},
		{ID: "far", DocumentID: "d", Text: "z", Embedding: []float32{-1, 0}},
	}
	if err := s.Add(context.Background(), chunks); err != nil {
		t.Fatal(err)
	}
	res, err := s.Retrieve(context.Background(), memory.Query{
		Embedding: []float32{1, 0},
		K:         2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d results, want 2", len(res))
	}
	if res[0].Chunk.ID != "near" {
		t.Errorf("top hit = %q, want near", res[0].Chunk.ID)
	}
	if res[1].Chunk.ID != "mid" {
		t.Errorf("second hit = %q, want mid (cosine > 0)", res[1].Chunk.ID)
	}
}

func TestInMemoryStore_MetadataFilter(t *testing.T) {
	t.Parallel()
	s := memory.NewInMemoryStore()
	chunks := []memory.Chunk{
		{ID: "a", DocumentID: "d", Text: "capital Ecuador Quito", Metadata: map[string]string{"lang": "en"}},
		{ID: "b", DocumentID: "d", Text: "capital Ecuador Quito", Metadata: map[string]string{"lang": "es"}},
	}
	_ = s.Add(context.Background(), chunks)
	res, err := s.Retrieve(context.Background(), memory.Query{
		Text:   "capital",
		Filter: map[string]string{"lang": "es"},
		K:     5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Chunk.ID != "b" {
		t.Fatalf("filter not applied: %+v", res)
	}
}

func TestInMemoryStore_Delete(t *testing.T) {
	t.Parallel()
	s := memory.NewInMemoryStore()
	_ = s.Add(context.Background(), []memory.Chunk{
		{ID: "a", DocumentID: "d1", Text: "alpha"},
		{ID: "b", DocumentID: "d1", Text: "beta"},
		{ID: "c", DocumentID: "d2", Text: "gamma"},
	})
	if err := s.Delete(context.Background(), "d1"); err != nil {
		t.Fatal(err)
	}
	if got := s.Len(); got != 1 {
		t.Errorf("Len after Delete = %d, want 1", got)
	}
	res, _ := s.Retrieve(context.Background(), memory.Query{Text: "gamma"})
	if len(res) != 1 || res[0].Chunk.ID != "c" {
		t.Errorf("remaining chunk wrong: %+v", res)
	}
}

func TestInMemoryStore_AddIsIdempotentOnID(t *testing.T) {
	t.Parallel()
	s := memory.NewInMemoryStore()
	_ = s.Add(context.Background(), []memory.Chunk{{ID: "x", DocumentID: "d", Text: "v1"}})
	_ = s.Add(context.Background(), []memory.Chunk{{ID: "x", DocumentID: "d", Text: "v2"}})
	if got := s.Len(); got != 1 {
		t.Errorf("Len = %d, want 1 (overwrite, not duplicate)", got)
	}
	res, _ := s.Retrieve(context.Background(), memory.Query{Text: "v2"})
	if len(res) != 1 || res[0].Chunk.Text != "v2" {
		t.Errorf("overwrite failed: %+v", res)
	}
}

func TestInMemoryStore_EmptyQueryRejected(t *testing.T) {
	t.Parallel()
	s := memory.NewInMemoryStore()
	if _, err := s.Retrieve(context.Background(), memory.Query{}); err == nil {
		t.Fatal("expected error on empty Query")
	}
}
