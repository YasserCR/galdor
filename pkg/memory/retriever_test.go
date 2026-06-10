package memory_test

import (
	"context"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/memory"
)

// badEmbedder returns a caller-controlled number of vectors, to exercise the
// Retriever's contract check.
type badEmbedder struct{ vecs [][]float32 }

func (b badEmbedder) Embed(_ context.Context, _ []string) ([][]float32, error) {
	return b.vecs, nil
}
func (b badEmbedder) Dimensions() int { return 3 }

// Regression (audit low): when the embedder returns anything other than one
// vector for one query text, the Retriever must error rather than silently
// forward an embedding-less query (degrading a vector search to lexical).
func TestRetriever_EmbedderWrongVectorCountErrors(t *testing.T) {
	t.Parallel()
	cases := map[string][][]float32{
		"zero vectors":     {},
		"too many vectors": {{1, 2, 3}, {4, 5, 6}},
		"empty vector":     {{}},
	}
	for name, vecs := range cases {
		t.Run(name, func(t *testing.T) {
			r := &memory.Retriever{
				Store:    memory.NewInMemoryStore(),
				Embedder: badEmbedder{vecs: vecs},
			}
			_, err := r.Retrieve(context.Background(), memory.Query{Text: "hello", K: 3})
			if err == nil || !strings.Contains(err.Error(), "embedder") {
				t.Fatalf("expected an embedder contract error, got %v", err)
			}
		})
	}
}

// The happy path (exactly one non-empty vector) must still work.
func TestRetriever_EmbedderSingleVectorOK(t *testing.T) {
	t.Parallel()
	r := &memory.Retriever{
		Store:    memory.NewInMemoryStore(),
		Embedder: badEmbedder{vecs: [][]float32{{1, 2, 3}}},
	}
	if _, err := r.Retrieve(context.Background(), memory.Query{Text: "hello", K: 3}); err != nil {
		t.Fatalf("single-vector embed must succeed, got %v", err)
	}
}

// Regression (audit low): InMemoryStore must not alias the caller's
// Embedding slice / Metadata map. Mutating them after Add (or mutating a
// retrieved result) must not corrupt stored data.
func TestInMemoryStore_DoesNotAliasCallerData(t *testing.T) {
	t.Parallel()
	s := memory.NewInMemoryStore()
	emb := []float32{1, 0, 0}
	meta := map[string]string{"k": "v"}
	if err := s.Add(context.Background(), []memory.Chunk{
		{ID: "x", DocumentID: "d", Text: "hello", Embedding: emb, Metadata: meta},
	}); err != nil {
		t.Fatal(err)
	}
	// Mutate the caller's originals after Add.
	emb[0] = 999
	meta["k"] = "TAMPERED"

	res, err := s.Retrieve(context.Background(), memory.Query{Embedding: []float32{1, 0, 0}, K: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("got %d results", len(res))
	}
	if res[0].Chunk.Embedding[0] != 1 {
		t.Errorf("stored embedding aliased caller slice: got %v", res[0].Chunk.Embedding)
	}
	if res[0].Chunk.Metadata["k"] != "v" {
		t.Errorf("stored metadata aliased caller map: got %q", res[0].Chunk.Metadata["k"])
	}
	// Mutating the RETURNED result must not corrupt the store either.
	res[0].Chunk.Metadata["k"] = "MUTATED_RESULT"
	res2, _ := s.Retrieve(context.Background(), memory.Query{Embedding: []float32{1, 0, 0}, K: 1})
	if res2[0].Chunk.Metadata["k"] != "v" {
		t.Errorf("mutating a result corrupted the store: got %q", res2[0].Chunk.Metadata["k"])
	}
}
