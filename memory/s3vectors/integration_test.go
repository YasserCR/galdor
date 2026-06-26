//go:build integration

package s3vectors_test

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/YasserCR/galdor/memory/s3vectors"
	"github.com/YasserCR/galdor/pkg/memory"
)

// These tests hit a real Amazon S3 Vectors bucket. They run only under
// the "integration" build tag AND when GALDOR_S3VECTORS_BUCKET is set.
// Credentials are resolved via the default AWS credential chain; set
// GALDOR_S3VECTORS_REGION (or AWS_REGION) to target a region where
// S3 Vectors is available.
//
//	export GALDOR_S3VECTORS_BUCKET='my-vector-bucket'
//	export GALDOR_S3VECTORS_REGION='us-east-1'
//	go test -tags=integration ./memory/s3vectors/...
//
// The vector bucket must already exist; each test creates (and reuses)
// its own index so concurrent runs don't collide. Indexes are left in
// place between runs; only the test document's vectors are cleaned up.
//
// S3 Vectors provides strong write consistency (a vector is immediately
// visible to Query/List after Add), so these assertions read right after
// writing without polling.

func newIntegrationStore(t *testing.T, index string) *s3vectors.Store {
	return newIntegrationStoreDim(t, index, 4)
}

func newIntegrationStoreDim(t *testing.T, index string, dim int) *s3vectors.Store {
	t.Helper()
	bucket := os.Getenv("GALDOR_S3VECTORS_BUCKET")
	if bucket == "" {
		t.Skip("GALDOR_S3VECTORS_BUCKET not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s, err := s3vectors.Open(ctx, s3vectors.Config{
		Bucket: bucket,
		Index:  index,
		Region: os.Getenv("GALDOR_S3VECTORS_REGION"),
		Dim:    dim,
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

// fixedEmbedder returns the same vector for any text; used to exercise
// the memory.Retriever composition (Text query → embed → Store.Retrieve)
// without a live embedding provider.
type fixedEmbedder struct{ vec []float32 }

func (e fixedEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = e.vec
	}
	return out, nil
}

func (e fixedEmbedder) Dimensions() int { return len(e.vec) }

func TestIntegration_AddAndRetrieve(t *testing.T) {
	s := newIntegrationStore(t, "galdor-chunks-t1")
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
	// n3 is anti-correlated (cosine -1) to the query and is dropped, for
	// parity with the sqlite / in-memory backends. n1 (identical, score 1)
	// and n2 (orthogonal, score 0) remain.
	if len(res) != 2 {
		t.Fatalf("got %d results, want 2 (anti-correlated n3 dropped)", len(res))
	}
	for _, r := range res {
		if r.Chunk.ID == "n3" {
			t.Error("anti-correlated chunk n3 should be dropped (negative cosine)")
		}
	}
	if res[0].Score < 0.99 {
		t.Errorf("top score = %v, want ~1.0 (1 - distance for identical vectors)", res[0].Score)
	}
	if res[0].Chunk.DocumentID != "test_doc" || res[0].Chunk.Text != "near" {
		t.Errorf("top chunk = %+v", res[0].Chunk)
	}
}

func TestIntegration_UpsertReplacesVector(t *testing.T) {
	s := newIntegrationStore(t, "galdor-chunks-t2")
	ctx := context.Background()
	first := memory.Chunk{ID: "x", DocumentID: "test_doc", Text: "v1", Embedding: []float32{1, 0, 0, 0}}
	if err := s.Add(ctx, []memory.Chunk{first}); err != nil {
		t.Fatal(err)
	}
	second := memory.Chunk{ID: "x", DocumentID: "test_doc", Text: "v2", Embedding: []float32{0, 1, 0, 0}}
	if err := s.Add(ctx, []memory.Chunk{second}); err != nil {
		t.Fatal(err)
	}
	n, err := s.Len(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("Len after upsert = %d, want 1 (same Chunk.ID must dedupe)", n)
	}
}

func TestIntegration_MetadataFilter(t *testing.T) {
	s := newIntegrationStore(t, "galdor-chunks-t3")
	ctx := context.Background()
	if err := s.Add(ctx, []memory.Chunk{
		{ID: "en", DocumentID: "test_doc", Text: "english", Embedding: []float32{1, 0, 0, 0}, Metadata: map[string]string{"lang": "en"}},
		{ID: "es", DocumentID: "test_doc", Text: "spanish", Embedding: []float32{1, 0, 0, 0}, Metadata: map[string]string{"lang": "es"}},
	}); err != nil {
		t.Fatal(err)
	}
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
	s := newIntegrationStore(t, "galdor-chunks-t4")
	ctx := context.Background()
	if err := s.Add(ctx, []memory.Chunk{
		{ID: "a", DocumentID: "test_doc", Text: "x", Embedding: []float32{1, 0, 0, 0}},
		{ID: "b", DocumentID: "test_doc", Text: "y", Embedding: []float32{0, 1, 0, 0}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "test_doc"); err != nil {
		t.Fatal(err)
	}
	n, err := s.Len(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("Len after Delete = %d, want 0", n)
	}
	// Deleting again is a no-op (not an error).
	if err := s.Delete(ctx, "test_doc"); err != nil {
		t.Errorf("second Delete should be a no-op, got %v", err)
	}
}

// TestIntegration_Scale exercises the volume paths against the live
// service at a realistic dimension (1024, Titan v2): Add batching
// (>500 vectors → multiple PutVectors), Retrieve pagination (K>100, but
// the service caps a page at 100), and Delete over a multi-page index
// (>1000 vectors → multiple ListVectors pages + multiple DeleteVectors
// batches). This is the heavy test; it stores ~1200 1024-dim vectors
// briefly, then removes them.
func TestIntegration_Scale(t *testing.T) {
	const (
		dim = 1024
		n   = 1200 // > 1000 (list page) and > 500 (put batch)
		doc = "scale_doc"
	)
	s := newIntegrationStoreDim(t, "galdor-chunks-scale", dim)
	ctx := context.Background()
	t.Cleanup(func() { _ = s.Delete(context.Background(), doc) })

	chunks := make([]memory.Chunk, n)
	for i := range chunks {
		emb := make([]float32, dim)
		// emb[0] decreases with i → cosine with query [1,0,...] is positive
		// for all (no drops) and strictly ranks chunk 0 highest.
		emb[0] = 2.0 - float32(i)/float32(n)
		emb[1+(i%(dim-1))] = 0.3 // spread so vectors aren't identical
		chunks[i] = memory.Chunk{
			ID: "s" + strconv.Itoa(i), DocumentID: doc, Index: i,
			Text:      "chunk " + strconv.Itoa(i),
			Embedding: emb,
			Metadata:  map[string]string{"batch": "scale"},
		}
	}

	// Add: 1200 > maxPutBatch(500) → 3 PutVectors batches.
	if err := s.Add(ctx, chunks); err != nil {
		t.Fatal(err)
	}

	// Len scans ListVectors; >1000 vectors → multi-page list.
	if got, err := s.Len(ctx); err != nil {
		t.Fatal(err)
	} else if got != n {
		t.Fatalf("Len = %d, want %d", got, n)
	}

	// Retrieve with K=250 > 100 results/page → Retrieve must paginate.
	q := make([]float32, dim)
	q[0] = 1
	res, err := s.Retrieve(ctx, memory.Query{Embedding: q, K: 250})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Retrieve K=250 returned %d results", len(res))
	if len(res) <= 100 {
		t.Fatalf("got %d results; want >100 (proves QueryVectors pagination across pages)", len(res))
	}
	if len(res) > 250 {
		t.Fatalf("got %d results; must not exceed K=250", len(res))
	}
	for i := 1; i < len(res); i++ {
		if res[i].Score > res[i-1].Score {
			t.Errorf("results not in descending score at %d: %v > %v", i, res[i].Score, res[i-1].Score)
			break
		}
	}
	if res[0].Chunk.Text == "" {
		t.Error("top chunk Text is empty (round-trip broken at 1024-dim)")
	}

	// Delete the whole document: 1200 vectors → multi-page ListVectors +
	// multiple DeleteVectors batches.
	if err := s.Delete(ctx, doc); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Len(ctx); err != nil {
		t.Fatal(err)
	} else if got != 0 {
		t.Errorf("Len after Delete = %d, want 0", got)
	}
}

// TestIntegration_MultiKeyFilterAND confirms a multi-key filter is an
// exact-match AND of all pairs (only the chunk matching every pair is
// returned), against the live native metadata filter.
func TestIntegration_MultiKeyFilterAND(t *testing.T) {
	s := newIntegrationStore(t, "galdor-chunks-t5")
	ctx := context.Background()
	q := []float32{1, 0, 0, 0}
	if err := s.Add(ctx, []memory.Chunk{
		{ID: "a", DocumentID: "test_doc", Text: "a", Embedding: q, Metadata: map[string]string{"type": "skill", "category": "backend"}},
		{ID: "b", DocumentID: "test_doc", Text: "b", Embedding: q, Metadata: map[string]string{"type": "skill", "category": "frontend"}},
		{ID: "c", DocumentID: "test_doc", Text: "c", Embedding: q, Metadata: map[string]string{"type": "doc", "category": "backend"}},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Retrieve(ctx, memory.Query{
		Embedding: q, K: 10,
		Filter: map[string]string{"type": "skill", "category": "backend"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Only "a" satisfies BOTH type=skill AND category=backend.
	if len(res) != 1 || res[0].Chunk.ID != "a" {
		t.Fatalf("multi-key AND filter wrong: want exactly [a], got %+v", res)
	}
}

// TestIntegration_ViaRetriever confirms the drop-in behind memory.Retriever
// (AC #6): a Text-only query is embedded upstream, then delegated to the
// Store — no caller code knows the backend is S3 Vectors.
func TestIntegration_ViaRetriever(t *testing.T) {
	s := newIntegrationStore(t, "galdor-chunks-t6")
	ctx := context.Background()
	if err := s.Add(ctx, []memory.Chunk{
		{ID: "r1", DocumentID: "test_doc", Text: "hello", Embedding: []float32{1, 0, 0, 0}},
	}); err != nil {
		t.Fatal(err)
	}
	r := memory.Retriever{Store: s, Embedder: fixedEmbedder{vec: []float32{1, 0, 0, 0}}, DefaultK: 5}
	res, err := r.Retrieve(ctx, memory.Query{Text: "anything"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) < 1 || res[0].Chunk.ID != "r1" {
		t.Fatalf("retriever path failed: %+v", res)
	}
}
