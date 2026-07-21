// Command okf-rag demonstrates retrieval-augmented generation over an
// Open Knowledge Format (OKF) bundle using galdor's BM25 backend, with an
// optional hybrid (BM25 + dense, fused with Reciprocal Rank Fusion) mode.
// No network or API key is required.
//
// Run with:
//
//	go run ./examples/okf-rag                            # hybrid, default question
//	go run ./examples/okf-rag --mode bm25 "retention"    # pure lexical
//	go run ./examples/okf-rag --mode bm25 "mrr_amount"   # exact identifier match
//	go run ./examples/okf-rag --mode hybrid "how is recurring revenue measured"
//
// Note: BM25 scores are corpus-relative. On this 4-concept bundle a term
// shared by most concepts (e.g. "mrr") gets a small IDF and scores low,
// while a discriminative term ("retention") scores clearly. The ranking is
// what matters; the hybrid mode's RRF scores are rank-based and always
// meaningful.
//
// What happens:
//
//  1. An embedded OKF bundle (markdown + YAML frontmatter) is loaded and
//     chunked concept-first by the memory/okf backend.
//  2. --mode bm25: the concepts are queried lexically via galdor's native
//     BM25 index, whose code-aware tokenizer nails exact identifiers like
//     `mrr_amount` (indexed whole and as `mrr` + `amount`).
//  3. --mode hybrid: the same concepts are ALSO embedded (offline
//     HashingEmbedder) into a vector store, and a memory.HybridRetriever
//     fuses the BM25 and dense rankings with RRF (k=60).
//  4. The retrieved concepts are formatted into the system prompt of a
//     scripted provider, which "answers" from that context.
//
// The HashingEmbedder is a lexical stand-in — swap it for a real
// provider-backed Embedder (providers/openai.NewEmbedder, providers/
// google.NewEmbedder) and the dense side gains true semantic recall; the
// wiring stays identical.
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/YasserCR/galdor/memory/okf"
	"github.com/YasserCR/galdor/memory/sqlite"
	"github.com/YasserCR/galdor/pkg/memory"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

//go:embed bundle
var bundleFS embed.FS

func main() {
	mode := flag.String("mode", "hybrid", "retrieval mode: bm25 | hybrid")
	k := flag.Int("k", 3, "number of concepts to retrieve")
	flag.Parse()

	question := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if question == "" {
		question = "How is monthly recurring revenue measured?"
	}
	if err := run(context.Background(), *mode, *k, question); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, mode string, k int, question string) error {
	// 1. Load + chunk the embedded OKF bundle.
	docs, warnings, err := okf.LoadFS(bundleFS, "bundle")
	if err != nil {
		return fmt.Errorf("load bundle: %w", err)
	}
	for _, w := range warnings {
		log.Printf("warning: %s", w)
	}
	chunks := okf.ChunkConcepts(docs)

	// 2. Lexical (BM25) source — the native OKF backend.
	lexical, err := okf.NewStore(ctx, chunks)
	if err != nil {
		return fmt.Errorf("build lexical store: %w", err)
	}
	defer func() { _ = lexical.Close() }()

	var hits []memory.Result
	switch mode {
	case "bm25":
		hits, err = lexical.Retrieve(ctx, memory.Query{Text: question, K: k})
	case "hybrid":
		hits, err = hybridRetrieve(ctx, lexical, chunks, question, k)
	default:
		return fmt.Errorf("unknown --mode %q (want bm25 or hybrid)", mode)
	}
	if err != nil {
		return fmt.Errorf("retrieve: %w", err)
	}

	fmt.Printf("Loaded %d concepts. Mode: %s.\n\nQuestion: %s\n\nTop %d concepts:\n",
		len(docs), mode, question, len(hits))
	for i, h := range hits {
		fmt.Printf("  %d. [%s] (%s) score=%.4f\n     %s\n",
			i+1, h.Chunk.Metadata[okf.MetaConceptID], h.Chunk.Metadata[okf.MetaType],
			h.Score, snippet(h.Chunk.Text, 110))
	}
	fmt.Println()

	// 4. Splice retrieved context into a scripted provider's system prompt.
	prov := &scriptedProvider{answer: scriptedAnswer(hits)}
	sysPrompt := "You answer strictly from the provided OKF concepts, citing each by id.\n\nContext:\n" + formatContext(hits)
	resp, err := prov.Generate(ctx, provider.Request{
		Model: "demo",
		Messages: []schema.Message{
			schema.SystemMessage(sysPrompt),
			schema.UserMessage(question),
		},
	})
	if err != nil {
		return fmt.Errorf("provider: %w", err)
	}
	fmt.Printf("Answer: %s\n", resp.Message.Text())
	return nil
}

// hybridRetrieve builds a dense vector source over the same chunks and
// fuses it with the lexical source under a HybridRetriever.
func hybridRetrieve(ctx context.Context, lexical *okf.Store, chunks []memory.Chunk, question string, k int) ([]memory.Result, error) {
	embedder := memory.NewHashingEmbedder(256)

	dense, err := sqlite.Open(":memory:")
	if err != nil {
		return nil, err
	}
	defer func() { _ = dense.Close() }()

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}
	vecs, err := embedder.Embed(ctx, texts)
	if err != nil {
		return nil, err
	}
	embedded := make([]memory.Chunk, len(chunks))
	for i := range chunks {
		embedded[i] = chunks[i]
		embedded[i].Embedding = vecs[i]
	}
	if err := dense.Add(ctx, embedded); err != nil {
		return nil, err
	}

	hybrid := &memory.HybridRetriever{
		Sources: []memory.Searcher{
			&memory.Retriever{Store: lexical},                   // BM25
			&memory.Retriever{Store: dense, Embedder: embedder}, // dense
		},
		K: k,
	}
	return hybrid.Retrieve(ctx, memory.Query{Text: question})
}

func snippet(s string, n int) string {
	for _, line := range strings.Split(s, "\n")[1:] { // skip folded header line
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "|") {
			continue
		}
		if len([]rune(t)) > n {
			return string([]rune(t)[:n]) + "..."
		}
		return t
	}
	return ""
}

func formatContext(hits []memory.Result) string {
	var b strings.Builder
	for i, h := range hits {
		fmt.Fprintf(&b, "[%d] (%s) %s\n", i+1, h.Chunk.Metadata[okf.MetaConceptID], strings.TrimSpace(h.Chunk.Text))
	}
	return b.String()
}

func scriptedAnswer(hits []memory.Result) string {
	if len(hits) == 0 {
		return "That is not covered by the bundle."
	}
	return "Based on " + hits[0].Chunk.Metadata[okf.MetaConceptID] + ": " + snippet(hits[0].Chunk.Text, 200)
}

// scriptedProvider is the minimal Provider used in offline examples.
type scriptedProvider struct{ answer string }

func (*scriptedProvider) Name() string                        { return "scripted" }
func (*scriptedProvider) Capabilities() provider.Capabilities { return provider.Capabilities{} }
func (*scriptedProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}

//nolint:unparam // deterministic fixture: the error return is required by provider.Provider
func (p *scriptedProvider) Generate(_ context.Context, _ provider.Request) (*provider.Response, error) {
	return &provider.Response{
		Message:    schema.AssistantMessage(p.answer),
		StopReason: schema.StopReasonEndTurn,
		Model:      "scripted-1",
	}, nil
}
