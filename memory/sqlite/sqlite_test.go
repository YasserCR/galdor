package sqlite_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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
	// error; quoting each token as a literal neutralizes the operators.
	res, err := s.Retrieve(ctx, memory.Query{Text: `"hello" *world*: +foo -bar`, K: 5})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(res) != 1 {
		t.Errorf("got %d results, want 1", len(res))
	}
}

func TestRetrieve_LexicalTolerantOfBooleanKeywords(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.Add(ctx, []memory.Chunk{
		{ID: "c", DocumentID: "d", Text: "Hello world."},
	})
	// FTS5 treats AND/OR/NOT as operators; a natural query containing
	// them (or a lone keyword) used to raise "fts5: syntax error near
	// ...". Quoting each token as a literal must keep these queries safe.
	for _, q := range []string{
		"hello AND world",
		"hello OR world",
		"AND OR NOT",
		"OR",
		"world NOT gardening",
	} {
		if _, err := s.Retrieve(ctx, memory.Query{Text: q, K: 5}); err != nil {
			t.Errorf("query %q: unexpected error %v", q, err)
		}
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

// Regression for audit M14 (sqlite half): a query whose embedding
// dimension doesn't match a stored chunk must error, not silently score
// over a truncated prefix. inmem already has its own M14 test; this pins
// the sqlite cosine path the audit named alongside it.
func TestRetrieve_VectorDimensionMismatchErrors(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.Add(ctx, []memory.Chunk{
		{ID: "c1", DocumentID: "d", Text: "x", Embedding: []float32{1, 0, 0, 0}}, // 4-dim
	})
	_, err := s.Retrieve(ctx, memory.Query{Embedding: []float32{1, 0, 0}, K: 5}) // 3-dim
	if err == nil {
		t.Fatal("a dimension mismatch must error (regression of M14)")
	}
	if !strings.Contains(err.Error(), "dimension mismatch") {
		t.Errorf("error should name the mismatch, got %v", err)
	}
}

func TestRetrieve_VectorMetadataFilter(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	// Two topics. The nearest vector to the query lives in topic "b"; the
	// filter must scope the scan so only topic "a" is considered.
	_ = s.Add(ctx, []memory.Chunk{
		{ID: "a1", DocumentID: "d", Text: "x", Embedding: []float32{0.2, 1}, Metadata: map[string]string{"topic": "a"}},
		{ID: "b1", DocumentID: "d", Text: "y", Embedding: []float32{1, 0}, Metadata: map[string]string{"topic": "b"}},
	})
	res, err := s.Retrieve(ctx, memory.Query{
		Embedding: []float32{1, 0},
		Filter:    map[string]string{"topic": "a"},
		K:         5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Chunk.ID != "a1" {
		t.Fatalf("vector filter not applied (should only see topic a): %+v", res)
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
	if addErr := s1.Add(context.Background(), []memory.Chunk{
		{ID: "x", DocumentID: "d", Text: "the answer is 42"},
	}); addErr != nil {
		t.Fatal(addErr)
	}
	if closeErr := s1.Close(); closeErr != nil {
		t.Fatal(closeErr)
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

// Regression (audit low): the FTS5 external-content index keys on the chunks
// rowid; with an implicit (TEXT PRIMARY KEY) rowid, VACUUM renumbers rows and
// desyncs the index. The schema now aliases rowid to a stable INTEGER PRIMARY
// KEY, so a lexical query still finds the right chunk after a VACUUM.
func TestRetrieve_LexicalSurvivesVACUUM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.db")
	s, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Add several chunks, then delete an early one to create a rowid gap that
	// VACUUM will compact (renumbering later rows under the old schema).
	if err = s.Add(ctx, []memory.Chunk{
		{ID: "a", DocumentID: "d", Index: 0, Text: "alpha alpha"},
		{ID: "b", DocumentID: "d", Index: 1, Text: "bravo bravo"},
		{ID: "c", DocumentID: "d", Index: 2, Text: "charlie charlie unicorn"},
	}); err != nil {
		t.Fatal(err)
	}
	// Delete the whole document (a, b, c), then re-add only b and c so a's
	// rowid slot becomes a gap that VACUUM compacts.
	if err = s.Delete(ctx, "d"); err != nil {
		t.Fatal(err)
	}
	if err = s.Add(ctx, []memory.Chunk{
		{ID: "b", DocumentID: "d2", Index: 0, Text: "bravo bravo"},
		{ID: "c", DocumentID: "d2", Index: 1, Text: "charlie charlie unicorn"},
	}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	// VACUUM via a separate raw connection (driver already registered).
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec("VACUUM"); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	// Reopen and confirm FTS still finds the unique term in the right chunk.
	s2, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	res, err := s2.Retrieve(ctx, memory.Query{Text: "unicorn", K: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 || res[0].Chunk.ID != "c" {
		t.Fatalf("FTS desynced after VACUUM: got %+v, want chunk c", res)
	}
}
