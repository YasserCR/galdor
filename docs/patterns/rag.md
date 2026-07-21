# RAG: retrieval-augmented generation

## When to use this pattern

Your agent answers from a corpus that doesn't fit in a prompt and
that changes faster than you want to fine-tune for. Chunk the
corpus, embed each chunk, store the vectors, retrieve the top-k at
question time, splice them into the system prompt. The LLM stays
stock; the freshness comes from the store.

## Minimal sketch

```go
store, _ := sqlite.Open("./rag.db")
defer store.Close()

embedder := memory.NewHashingEmbedder(256)
chunker := chunk.Recursive{Size: 180, Overlap: 30}

for _, doc := range corpus {
    chunks, _ := chunker.Chunk(doc)
    texts := make([]string, len(chunks))
    for i, c := range chunks {
        texts[i] = c.Text
    }
    vecs, _ := embedder.Embed(ctx, texts)
    for i := range chunks {
        chunks[i].ID = chunkID(doc.ID, chunks[i].Index)
        chunks[i].Embedding = vecs[i]
    }
    _ = store.Add(ctx, chunks)
}

retriever := &memory.Retriever{Store: store, Embedder: embedder, DefaultK: 3}
hits, _ := retriever.Retrieve(ctx, memory.Query{Text: question})

sys := "Answer strictly from the context.\n\nContext:\n" + format(hits)
resp, _ := prov.Generate(ctx, provider.Request{
    Model: "claude-haiku-4-5",
    Messages: []schema.Message{
        schema.SystemMessage(sys),
        schema.UserMessage(question),
    },
})
```

## Walkthrough

1. **Chunk.** `chunk.Recursive` splits each `memory.Document` on
   paragraph, sentence and word boundaries until every chunk fits
   `Size` runes with `Overlap` runes of carry-over context. Overlap
   is what keeps an answer from getting truncated at a chunk seam.
2. **Embed.** `memory.Embedder.Embed` turns a `[]string` into a
   `[][]float32`. The example uses `memory.NewHashingEmbedder`, a
   deterministic offline stand-in. For production, swap it for a
   provider-backed embedder (see *Common variations*).
3. **Store.** `memory.Store.Add` writes chunks plus their vectors.
   The four shipped backends share one interface, so the rest of
   the code is identical whichever you pick.
4. **Retrieve.** `memory.Retriever` is the convenience wrapper that
   embeds the query for you and returns `[]memory.Result` sorted by
   cosine similarity (BM25 + cosine in the SQLite backend).
5. **Splice.** Format the hits into the system prompt and call the
   provider. The LLM stays unchanged — the only difference between
   "no RAG" and "RAG" is the prefix in the system message.

## Common variations

### Swap the embedder for a real one

The offline `HashingEmbedder` is lexical, not semantic. For
production:

```go
import openaiprov "github.com/YasserCR/galdor/providers/openai"

emb, _ := openaiprov.NewEmbedder(openaiprov.EmbedderConfig{
    APIKey: os.Getenv("OPENAI_API_KEY"),
})
```

Defaults to `text-embedding-3-small` (1536-d). `BaseURL` works
against any OpenAI-compatible endpoint (Mistral, MiniMax, Together,
Groq, vLLM). For Gemini use `providers/google.NewEmbedder`.

The dimensionality you pick locks the store. Re-embed if you
change models — vectors of different dims aren't comparable.

### Swap the embedder for a self-hosted endpoint

When the model runs in your own infrastructure — HuggingFace TEI,
Infinity, vLLM-embeddings, an OpenAI-compatible sidecar — use
`pkg/embedder.HTTPEmbedder` instead of a provider package. Same
interface, no SDK or credentials:

```go
import "github.com/YasserCR/galdor/pkg/embedder"

// HuggingFace TEI sidecar.
emb, _ := embedder.NewHTTPEmbedder(embedder.HTTPConfig{
    URL:   "http://tei:80",
    Shape: embedder.ShapeTEI,
})

// vLLM or anything else serving OpenAI's /v1/embeddings shape.
emb, _ = embedder.NewHTTPEmbedder(embedder.HTTPConfig{
    URL:   "http://vllm:8000/v1/embeddings",
    Model: "BAAI/bge-base-en-v1.5",
})

// Optional: probe once at startup so a slow-loading sidecar fails
// loudly here instead of on the first user query.
if err := emb.Ping(ctx); err != nil {
    log.Fatal("embeddings sidecar not ready: ", err)
}
```

`HTTPEmbedder` retries 5xx and 429 with capped exponential backoff
(three attempts), and returns a typed `*EmbedError` carrying the
status code on terminal failures. Inputs are batched at
`HTTPConfig.BatchSize` (default 32) so a single `Embed` call against
a long corpus splits into multiple HTTP requests transparently. See
the [embedder concept page](../concepts/embedder.md) for the full
surface, including the OpenAI vs. TEI wire-shape distinction and
413-handling guidance.

### Swap the backend

| Backend | Constructor | Use when |
|---|---|---|
| in-mem | `memory.NewInMemoryStore()` | tests, one-shot scripts |
| SQLite | `sqlite.Open(path)` | single-process, embedded apps, the default |
| pgvector | `pgvector.Open(ctx, pgvector.Config{ConnString, Dim})` | Postgres-centric stacks |
| qdrant | `qdrant.Open(ctx, qdrant.Config{URL, Dim})` | dedicated vector DB |

Every backend implements `memory.Store`; the only line that changes
is the constructor. `Dim` is fixed at open time for pgvector /
qdrant — pick to match your embedder.

### Stable chunk IDs

Use a deterministic ID (e.g., `sha256(docID + "#" + index)`) so
re-ingesting the same document overwrites its previous chunks
instead of duplicating them.

### Filter on metadata

`memory.Query.Filter` is a map of key-equals-value constraints
against `chunk.Metadata`. Use it for tenant isolation
(`"tenant": tenantID`) or version pinning (`"version": "2026-Q2"`).

## Gotchas

- **Top-k is not a quality signal.** `DefaultK: 3` is a starting
  point; tune to your corpus. Too few hits and the LLM hallucinates
  around the gap; too many and noise crowds out the signal.
- **Chunk size interacts with retrieval.** A 4000-char chunk is
  unlikely to be a precise hit for a 5-word question. Smaller +
  overlap usually beats larger.
- **Dimensions are sticky.** Once a pgvector / qdrant collection is
  created with `Dim=1536`, every chunk you `Add` must match. To
  switch models, create a new collection and re-ingest.
- **`HashingEmbedder` is for plumbing only.** It has no semantic
  understanding. Use it in tests and the offline example;
  benchmarks against a real embedder will look catastrophic.
- **The retrieved context becomes the prompt.** RAG inherits every
  prompt-injection risk of the underlying source. Treat retrieved
  text as user input, not as trusted instructions.

## Hybrid and knowledge-in-git variants

- **Hybrid retrieval.** When exact terms matter as much as meaning (identifiers,
  SQL, product names), fuse a BM25 ranking with a dense one using
  `memory.HybridRetriever` (RRF) instead of choosing one. Any `Store`
  or `Retriever` is a fusion `Source`.
- **Knowledge in git (OKF).** The `memory/okf` backend retrieves over an Open
  Knowledge Format bundle — markdown + YAML frontmatter versioned in git, no
  vector database required. It is a BM25 `Store` you can compose into a hybrid,
  and it adds a knowledge layer on top: a link graph with graph-expanded
  retrieval, progressive-disclosure browsing, and `okf_search` / `okf_browse`
  tools for agents. See the [OKF concept](../concepts/okf.md).

## Links

- Runnable example: [examples/memory-rag](../../examples/memory-rag/)
- Runnable example: [examples/okf-rag](../../examples/okf-rag/) — BM25 + hybrid over an OKF bundle
- Concept: [memory](../concepts/memory.md)
- Concept: [provider](../concepts/provider.md)
- Related: [replay-tests](replay-tests.md) — once RAG works, lock
  in regression tests against a fixture.
