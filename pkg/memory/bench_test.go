package memory_test

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/YasserCR/galdor/pkg/memory"
)

// randVec returns a deterministic d-dim vector seeded from id so
// benchmark runs are repeatable without `-count` shuffling.
func randVec(id int, d int) []float32 {
	r := rand.New(rand.NewSource(int64(id)))
	out := make([]float32, d)
	for i := range out {
		out[i] = r.Float32()*2 - 1
	}
	return out
}

// seedStore builds an in-memory store with n chunks of d-dim
// vectors. Used by the retrieval benchmarks.
func seedStore(b *testing.B, n, d int) *memory.InMemoryStore {
	b.Helper()
	s := memory.NewInMemoryStore()
	chunks := make([]memory.Chunk, n)
	for i := range chunks {
		chunks[i] = memory.Chunk{
			ID:         fmt.Sprintf("c%d", i),
			DocumentID: "d",
			Text:       fmt.Sprintf("chunk %d about something or other", i),
			Embedding:  randVec(i, d),
		}
	}
	if err := s.Add(context.Background(), chunks); err != nil {
		b.Fatal(err)
	}
	return s
}

// BenchmarkRetrieve_Vector_100 measures cosine-similarity ranking
// over a small corpus — the common case for a single agent's
// conversational memory.
func BenchmarkRetrieve_Vector_100(b *testing.B) {
	s := seedStore(b, 100, 256)
	q := memory.Query{Embedding: randVec(99999, 256), K: 5}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Retrieve(ctx, q); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRetrieve_Vector_1k measures cosine-similarity ranking
// over a mid-size corpus — what a typical RAG knowledge base
// might look like before pgvector / qdrant become worth it.
func BenchmarkRetrieve_Vector_1k(b *testing.B) {
	s := seedStore(b, 1000, 256)
	q := memory.Query{Embedding: randVec(99999, 256), K: 5}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Retrieve(ctx, q); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRetrieve_Vector_10k measures cosine-similarity ranking
// over a corpus at the upper edge of what an in-process store
// makes sense for. Above this you want pgvector / qdrant.
func BenchmarkRetrieve_Vector_10k(b *testing.B) {
	s := seedStore(b, 10_000, 256)
	q := memory.Query{Embedding: randVec(99999, 256), K: 5}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Retrieve(ctx, q); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkHashingEmbedder measures the offline hashing embedder's
// throughput. Used for tests and for the memory-rag example —
// production callers swap in a real provider-backed Embedder.
func BenchmarkHashingEmbedder(b *testing.B) {
	emb := memory.NewHashingEmbedder(256)
	text := "Quito is the capital of Ecuador, a small country on the equator."
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := emb.Embed(ctx, []string{text}); err != nil {
			b.Fatal(err)
		}
	}
}
