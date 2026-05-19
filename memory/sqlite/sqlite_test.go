package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/YasserCR/galdor/memory/sqlite"
	"github.com/YasserCR/galdor/pkg/memory"
)

func newTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpen_CreatesSchema(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	n, err := s.Len(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("Len = %d, want 0", n)
	}
}

func TestAdd_RejectsEmptyID(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	err := s.Add(context.Background(), []memory.Chunk{{DocumentID: "d", Text: "x"}})
	if err == nil {
		t.Fatal("expected error for empty Chunk.ID")
	}
}

func TestAdd_IsIdempotentOnID(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.Add(ctx, []memory.Chunk{{ID: "c1", DocumentID: "d", Text: "first"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(ctx, []memory.Chunk{{ID: "c1", DocumentID: "d", Text: "second"}}); err != nil {
		t.Fatal(err)
	}
	n, _ := s.Len(ctx)
	if n != 1 {
		t.Errorf("Len = %d after upsert, want 1", n)
	}
	res, err := s.Retrieve(ctx, memory.Query{Text: "second", K: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Chunk.Text != "second" {
		t.Errorf("upsert did not replace text: %+v", res)
	}
}

func TestRetrieve_LexicalBM25(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.Add(ctx, []memory.Chunk{
		{ID: "c1", DocumentID: "d", Text: "Quito is the capital of Ecuador."},
		{ID: "c2", DocumentID: "d", Text: "Bogotá is the capital of Colombia."},
		{ID: "c3", DocumentID: "d", Text: "Lima is the capital of Peru."},
		{ID: "c4", DocumentID: "d", Text: "Unrelated content about gardening."},
	})
	res, err := s.Retrieve(ctx, memory.Query{Text: "Ecuador capital", K: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("expected at least one result")
	}
	if res[0].Chunk.ID != "c1" {
		t.Errorf("top hit = %q, want c1", res[0].Chunk.ID)
	}
	// BM25 scores are positive (we negate the FTS5 raw score).
	if res[0].Score <= 0 {
		t.Errorf("Score = %v, want > 0", res[0].Score)
	}
}

func TestRetrieve_LexicalTolerantOfOperators(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.Add(ctx, []memory.Chunk{
		{ID: "c", DocumentID: "d", Text: "Hello world."},
	})
	// Punctuation-heavy queries used to make FTS5 raise a syntax
	// error; the sanitizer must strip operator chars.
	res, err := s.Retrieve(ctx, memory.Query{Text: `"hello" *world*: +foo -bar`, K: 5})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(res) != 1 {
		t.Errorf("got %d results, want 1", len(res))
	}
}

func TestRetrieve_VectorCosine(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.Add(ctx, []memory.Chunk{
		{ID: "near", DocumentID: "d", Text: "x", Embedding: []float32{0.9, 0.1}},
		{ID: "mid", DocumentID: "d", Text: "y", Embedding: []float32{0, 1}},
		{ID: "far", DocumentID: "d", Text: "z", Embedding: []float32{-1, 0}},
	})
	res, err := s.Retrieve(ctx, memory.Query{Embedding: []float32{1, 0}, K: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d results, want 2 (negative-cosine entry dropped)", len(res))
	}
	if res[0].Chunk.ID != "near" {
		t.Errorf("top hit = %q, want near", res[0].Chunk.ID)
	}
}

func TestRetrieve_MetadataFilter(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.Add(ctx, []memory.Chunk{
		{ID: "a", DocumentID: "d", Text: "Quito capital", Metadata: map[string]string{"lang": "en"}},
		{ID: "b", DocumentID: "d", Text: "Quito capital", Metadata: map[string]string{"lang": "es"}},
	})
	res, err := s.Retrieve(ctx, memory.Query{
		Text:   "Quito",
		Filter: map[string]string{"lang": "es"},
		K:      5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Chunk.ID != "b" {
		t.Fatalf("filter not applied: %+v", res)
	}
}

func TestDelete_ByDocument(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.Add(ctx, []memory.Chunk{
		{ID: "a", DocumentID: "d1", Text: "alpha"},
		{ID: "b", DocumentID: "d1", Text: "beta"},
		{ID: "c", DocumentID: "d2", Text: "gamma"},
	})
	if err := s.Delete(ctx, "d1"); err != nil {
		t.Fatal(err)
	}
	n, _ := s.Len(ctx)
	if n != 1 {
		t.Errorf("Len after Delete = %d, want 1", n)
	}
	res, _ := s.Retrieve(ctx, memory.Query{Text: "gamma", K: 5})
	if len(res) != 1 || res[0].Chunk.ID != "c" {
		t.Errorf("remaining chunk wrong: %+v", res)
	}
}

func TestPersistAcrossReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.db")

	s1, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Add(context.Background(), []memory.Chunk{
		{ID: "x", DocumentID: "d", Text: "the answer is 42"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// Re-open and verify the chunk is still there.
	s2, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	res, err := s2.Retrieve(context.Background(), memory.Query{Text: "answer", K: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Chunk.ID != "x" {
		t.Errorf("after reopen, retrieve = %+v", res)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("db file should exist on disk: %v", err)
	}
}

func TestRetrieve_RejectsEmptyQuery(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	if _, err := s.Retrieve(context.Background(), memory.Query{}); err == nil {
		t.Fatal("expected error on empty query")
	}
}
