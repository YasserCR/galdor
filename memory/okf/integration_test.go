package okf

import (
	"context"
	"testing"

	"github.com/YasserCR/galdor/pkg/memory"
)

// TestIntegration_EndToEnd exercises the whole backend against the fixture
// bundle: load → index → BM25 retrieve → delete → re-query.
func TestIntegration_EndToEnd(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)

	n, err := s.Len(ctx)
	if err != nil {
		t.Fatalf("Len: %v", err)
	}
	if n != 3 {
		t.Fatalf("indexed %d chunks, want 3", n)
	}

	// "past_due" appears only in the subscriptions table.
	res, err := s.Retrieve(ctx, memory.Query{Text: "past_due lifecycle state", K: 3})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got := topConcept(t, res); got != "tables/subscriptions" {
		t.Fatalf("top concept = %q, want tables/subscriptions", got)
	}

	// Delete the concept and confirm it is gone (re-ingestion story).
	if delErr := s.Delete(ctx, "tables/subscriptions"); delErr != nil {
		t.Fatalf("Delete: %v", delErr)
	}
	if n, _ = s.Len(ctx); n != 2 {
		t.Fatalf("after delete Len = %d, want 2", n)
	}
	res, err = s.Retrieve(ctx, memory.Query{Text: "past_due lifecycle state", K: 3})
	if err != nil {
		t.Fatalf("Retrieve after delete: %v", err)
	}
	for _, r := range res {
		if r.Chunk.Metadata[MetaConceptID] == "tables/subscriptions" {
			t.Fatal("deleted concept still retrievable")
		}
	}
}

// TestIntegration_HybridComposition wires the OKF store as the lexical
// source and a hashing-embedded vector store as the dense source under a
// core HybridRetriever — the shape examples/okf-rag uses.
func TestIntegration_HybridComposition(t *testing.T) {
	ctx := context.Background()

	docs, _, err := Load(bundlePath())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	chunks := ChunkConcepts(docs)

	lexical, err := NewStore(ctx, chunks)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = lexical.Close() })

	// Dense source: same chunks, embedded with the offline hashing embedder.
	embedder := memory.NewHashingEmbedder(256)
	dense, err := NewStore(ctx, embedChunks(ctx, t, embedder, chunks))
	if err != nil {
		t.Fatalf("dense NewStore: %v", err)
	}
	t.Cleanup(func() { _ = dense.Close() })

	hybrid := &memory.HybridRetriever{
		Sources: []memory.Searcher{
			&memory.Retriever{Store: lexical},                   // BM25
			&memory.Retriever{Store: dense, Embedder: embedder}, // dense
		},
		K: 3,
	}
	res, err := hybrid.Retrieve(ctx, memory.Query{Text: "recurring revenue"})
	if err != nil {
		t.Fatalf("hybrid Retrieve: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("hybrid returned no results")
	}
	if got := topConcept(t, res); got != "references/metrics/mrr" {
		t.Fatalf("hybrid top concept = %q, want references/metrics/mrr", got)
	}
}

func embedChunks(ctx context.Context, t *testing.T, e memory.Embedder, in []memory.Chunk) []memory.Chunk {
	t.Helper()
	texts := make([]string, len(in))
	for i, c := range in {
		texts[i] = c.Text
	}
	vecs, err := e.Embed(ctx, texts)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	out := make([]memory.Chunk, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Embedding = vecs[i]
	}
	return out
}
