// Command memory-rag demonstrates the full retrieval-augmented
// generation flow end-to-end against a local SQLite store and an
// offline hashing embedder. No network, no API key required.
//
// Run with:
//
//	go run ./examples/memory-rag
//
// What happens:
//
//  1. Three small "documents" about South American capitals are
//     chunked with the Recursive chunker.
//  2. Every chunk is embedded with the deterministic HashingEmbedder
//     (a stand-in for a real embedding model — quality is lexical,
//     not semantic, but the plumbing is identical).
//  3. Chunks are written to a SQLite-backed memory.Store (in-memory
//     for this example; pass a file path to Open() for persistence).
//  4. A Retriever embeds the user's question and pulls the top-3
//     chunks back out via cosine similarity.
//  5. The retrieved chunks are formatted into the system prompt of
//     a scripted Provider, which then "answers" using that context.
//
// Swap memory.HashingEmbedder for a real provider-backed Embedder
// and the scripted Provider for anthropic/openai/google/bedrock, and
// the rest of the wiring stays exactly the same. Real embedders ship
// with each provider module — for example:
//
//	import openaiprov "github.com/YasserCR/galdor/providers/openai"
//	emb, err := openaiprov.NewEmbedder(openaiprov.EmbedderConfig{
//	    APIKey: os.Getenv("OPENAI_API_KEY"),
//	    // Model defaults to text-embedding-3-small (1536-d).
//	})
//
// The same constructor works against any OpenAI-compatible endpoint
// (Mistral, MiniMax, Together, Groq) by setting BaseURL. For
// Gemini embeddings, use providers/google.NewEmbedder.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"

	"github.com/YasserCR/galdor/memory/sqlite"
	"github.com/YasserCR/galdor/pkg/memory"
	"github.com/YasserCR/galdor/pkg/memory/chunk"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

var corpus = []memory.Document{
	{
		ID:     "doc-quito",
		Source: "wiki:Quito",
		Text:   "Quito is the capital of Ecuador. Located at 2,850 m in the Andes, it is the second-highest capital city in the world after La Paz, Bolivia. Its colonial center was named a UNESCO World Heritage Site in 1978.",
	},
	{
		ID:     "doc-bogota",
		Source: "wiki:Bogotá",
		Text:   "Bogotá is the capital of Colombia. Founded in 1538 on the Sabana de Bogotá plateau, it sits at 2,640 m and is one of the largest high-altitude cities globally. It is Colombia's political and economic center.",
	},
	{
		ID:     "doc-lima",
		Source: "wiki:Lima",
		Text:   "Lima is the capital of Peru and the country's largest city. It lies on the central coast of Peru at the mouth of the Rímac River, only a few hundred meters above sea level despite being surrounded by the Andes farther inland.",
	},
}

func main() {
	ctx := context.Background()

	// 1. Open an in-process SQLite store. Pass a file path for
	//    persistence — the schema is created on first open.
	store, err := sqlite.Open(":memory:")
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// 2. Ingest the corpus: chunk → embed → add.
	embedder := memory.NewHashingEmbedder(256)
	chunker := chunk.Recursive{Size: 180, Overlap: 30}

	for _, doc := range corpus {
		chunks, err := chunker.Chunk(doc)
		if err != nil {
			log.Fatalf("chunk %q: %v", doc.ID, err)
		}
		texts := make([]string, len(chunks))
		for i, c := range chunks {
			texts[i] = c.Text
		}
		vecs, err := embedder.Embed(ctx, texts)
		if err != nil {
			log.Fatalf("embed: %v", err)
		}
		for i := range chunks {
			chunks[i].ID = chunkID(doc.ID, chunks[i].Index)
			chunks[i].Embedding = vecs[i]
		}
		if err := store.Add(ctx, chunks); err != nil {
			log.Fatalf("add: %v", err)
		}
	}

	n, _ := store.Len(ctx)
	fmt.Printf("Ingested %d chunks from %d documents.\n\n", n, len(corpus))

	// 3. Build a Retriever that embeds the query for the caller.
	retriever := &memory.Retriever{
		Store:    store,
		Embedder: embedder,
		DefaultK: 3,
	}

	question := "What is the capital of Ecuador and how high is it?"
	hits, err := retriever.Retrieve(ctx, memory.Query{Text: question})
	if err != nil {
		log.Fatalf("retrieve: %v", err)
	}

	fmt.Printf("Question: %s\n\nTop %d retrieved chunks:\n", question, len(hits))
	for i, h := range hits {
		fmt.Printf("  %d. [%s] score=%.3f\n     %s\n", i+1, h.Chunk.DocumentID, h.Score, snippet(h.Chunk.Text, 100))
	}
	fmt.Println()

	// 4. Splice the retrieved context into the system prompt of a
	//    scripted provider (stands in for any real provider).
	prov := &scriptedProvider{answer: scriptedAnswer(hits)}
	sysPrompt := "You answer questions strictly from the provided context.\n\nContext:\n" + formatContext(hits)
	resp, err := prov.Generate(ctx, provider.Request{
		Model: "demo",
		Messages: []schema.Message{
			schema.SystemMessage(sysPrompt),
			schema.UserMessage(question),
		},
	})
	if err != nil {
		log.Fatalf("provider: %v", err)
	}
	fmt.Printf("Answer: %s\n", resp.Message.Text())
}

// chunkID derives a stable, deterministic ID for a chunk from the
// document ID and the chunk's ordinal within it. Stable IDs make
// re-ingestion idempotent: the same chunk overwrites its previous
// row instead of producing a duplicate.
func chunkID(docID string, idx int) string {
	h := sha256.Sum256(fmt.Appendf(nil, "%s#%d", docID, idx))
	return hex.EncodeToString(h[:8])
}

func snippet(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "..."
}

func formatContext(hits []memory.Result) string {
	var b strings.Builder
	for i, h := range hits {
		fmt.Fprintf(&b, "[%d] (%s) %s\n", i+1, h.Chunk.DocumentID, h.Chunk.Text)
	}
	return b.String()
}

// scriptedAnswer cherry-picks the most relevant hit and produces a
// canned answer. A real Provider would let the LLM do this.
func scriptedAnswer(hits []memory.Result) string {
	if len(hits) == 0 {
		return "I don't know."
	}
	return "Based on the context: " + snippet(hits[0].Chunk.Text, 200)
}

// scriptedProvider is the minimal Provider used in examples that
// don't want to call a real API.
type scriptedProvider struct {
	answer string
}

func (*scriptedProvider) Name() string                       { return "scripted" }
func (*scriptedProvider) Capabilities() provider.Capabilities { return provider.Capabilities{} }
func (*scriptedProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}

func (p *scriptedProvider) Generate(_ context.Context, _ provider.Request) (*provider.Response, error) {
	return &provider.Response{
		Message:    schema.AssistantMessage(p.answer),
		StopReason: schema.StopReasonEndTurn,
		Model:      "scripted-1",
	}, nil
}
