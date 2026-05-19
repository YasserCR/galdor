//go:build integration

package pgvector_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/YasserCR/galdor/memory/pgvector"
	"github.com/YasserCR/galdor/pkg/memory"
)

// These tests hit a real Postgres + pgvector instance. They run only
// under the "integration" build tag AND when GALDOR_PGVECTOR_URL is
// set, e.g.:
//
//	export GALDOR_PGVECTOR_URL='postgres://galdor:galdor@localhost:5432/galdor?sslmode=disable'
//	go test -tags=integration ./memory/pgvector/...
//
// A throw-away container works:
//
//	docker run --rm -e POSTGRES_PASSWORD=galdor -e POSTGRES_USER=galdor \
//	    -e POSTGRES_DB=galdor -p 5432:5432 pgvector/pgvector:pg16
//
// Each test uses a unique table so concurrent runs don't collide.

func newIntegrationStore(t *testing.T, table string) *pgvector.Store {
	t.Helper()
	url := os.Getenv("GALDOR_PGVECTOR_URL")
	if url == "" {
		t.Skip("GALDOR_PGVECTOR_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := pgvector.Open(ctx, pgvector.Config{
		ConnString: url,
		Table:      table,
		Dim:        4,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Best-effort cleanup so re-runs are clean.
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
	if res[0].Chunk.ID != "n1" {
		t.Errorf("top hit = %q, want n1", res[0].Chunk.ID)
	}
	// Score = 1 - cosine_distance; identical vectors → 1.0
	if res[0].Score < 0.99 {
		t.Errorf("identical-vector score = %v, want ~1.0", res[0].Score)
	}
}

func TestIntegration_UpsertReplacesRow(t *testing.T) {
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
		t.Errorf("Len after upsert = %d, want 1", n)
	}
	res, _ := s.Retrieve(ctx, memory.Query{Embedding: []float32{0, 1, 0, 0}, K: 1})
	if len(res) != 1 || res[0].Chunk.Text != "v2" {
		t.Errorf("upsert did not replace text: %+v", res)
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
	if len(res) != 1 || res[0].Chunk.ID != "es" {
		t.Fatalf("filter not applied: %+v", res)
	}
}

func TestIntegration_RejectsDimMismatch(t *testing.T) {
	s := newIntegrationStore(t, "galdor_chunks_t4")
	err := s.Add(context.Background(), []memory.Chunk{
		{ID: "bad", DocumentID: "test_doc", Text: "x", Embedding: []float32{1, 0}}, // 2-dim, table is 4-dim
	})
	if err == nil {
		t.Fatal("expected dim-mismatch error")
	}
}
