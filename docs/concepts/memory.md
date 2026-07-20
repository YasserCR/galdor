# memory

`pkg/memory` is the persistence layer for everything an agent needs to remember beyond the current turn. Two orthogonal concerns live here: **short-term memory** (`Window`), which keeps the running conversation bounded for the next LLM call, and **long-term memory** (`Store`), which holds documents that get chunked, embedded, and retrieved on demand. The interfaces are small and the backends are swappable — the same agent code works against an in-process map, a SQLite + BM25 file, a Postgres + pgvector cluster, or a Qdrant collection by changing one constructor.

## Core types

```go
type Document struct {
    ID        string
    Source    string
    Text      string
    Metadata  map[string]string
    CreatedAt time.Time
}

type Chunk struct {
    ID         string
    DocumentID string
    Index      int
    Text       string
    Embedding  []float32
    Metadata   map[string]string
}

type Query struct {
    Text      string
    Embedding []float32
    K         int
    Filter    map[string]string
}

type Result struct {
    Chunk Chunk
    Score float32
}

type Store interface {
    Add(ctx context.Context, chunks []Chunk) error
    Retrieve(ctx context.Context, q Query) ([]Result, error)
    Delete(ctx context.Context, documentID string) error
    Close() error
}

type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    Dimensions() int
}
```

Score's meaning is store-specific (cosine for vector stores, BM25 for lexical) but higher always means more relevant. `Query.Filter` is exact-match metadata containment; stores that can't enforce it silently ignore it.

## Short-term: `Window`

A bounded conversation buffer. Caps by message count and/or estimated tokens (4-chars-per-token heuristic). A `Summarizer` hook compresses evicted turns into a system message tagged `Name: "summary"` instead of dropping them.

```go
w := &memory.Window{
    MaxMessages: 20,
    MaxTokens:   4000,
    Summarizer: memory.SummarizerFunc(func(ctx context.Context, evicted []schema.Message) (string, error) {
        return summarizeWithLLM(ctx, evicted)
    }),
}
w.Append(schema.UserMessage("hello"))
msgs, _ := w.Snapshot(ctx)
```

`Snapshot` is where trimming happens — it preserves a leading system message, prepends the accumulated summary when present, and returns a slice safe to mutate.

## Long-term: chunk, embed, store, retrieve

The end-to-end RAG flow:

```go
import (
    "github.com/YasserCR/galdor/pkg/memory"
    "github.com/YasserCR/galdor/pkg/memory/chunk"
    "github.com/YasserCR/galdor/memory/sqlite"
)

store, _ := sqlite.Open("./corpus.db")
defer store.Close()

embedder := memory.NewHashingEmbedder(256)
chunker := chunk.Recursive{Size: 512, Overlap: 64}

chunks, _ := chunker.Chunk(memory.Document{
    ID: "doc-1", Source: "wiki:Quito",
    Text: "Quito is the capital of Ecuador...",
})

texts := make([]string, len(chunks))
for i, c := range chunks {
    texts[i] = c.Text
}
vecs, _ := embedder.Embed(ctx, texts)
for i := range chunks {
    chunks[i].ID = fmt.Sprintf("doc-1-%d", i)
    chunks[i].Embedding = vecs[i]
}
_ = store.Add(ctx, chunks)

retriever := &memory.Retriever{Store: store, Embedder: embedder, DefaultK: 3}
hits, _ := retriever.Retrieve(ctx, memory.Query{Text: "capital of Ecuador"})
```

`Retriever` is the convenience wrapper: it embeds `Query.Text` when `Embedding` is empty and forwards to the underlying `Store`. Use it so caller code doesn't need to know whether the backend is lexical, vector, or hybrid.

### Hybrid retrieval (RRF)

`HybridRetriever` fuses several retrieval `Sources` with Reciprocal Rank Fusion (RRF, k=60) — the standard way to combine a lexical (BM25) ranking with a dense (vector) one without calibrating scores between them. Any `memory.Store` or `*memory.Retriever` is a `Searcher`, so a lexical source and a dense source compose directly:

```go
hybrid := &memory.HybridRetriever{
    Sources: []memory.Searcher{
        &memory.Retriever{Store: bm25Store},                    // lexical
        &memory.Retriever{Store: vecStore, Embedder: embedder}, // dense
    },
    K: 5,
}
hits, _ := hybrid.Retrieve(ctx, memory.Query{Text: "recurring revenue"})
```

Each source gets the same `Query`; the dense `Retriever` embeds the text internally, the lexical one ignores the embedding. See [`examples/okf-rag`](../../examples/okf-rag/).

## Chunkers

All three live under [`pkg/memory/chunk`](../../pkg/memory/chunk/) and implement the same one-method interface.

```go
chunk.FixedSize{Size: 512, Overlap: 64}
chunk.Recursive{Size: 512, Overlap: 64, Separators: chunk.DefaultSeparators}
chunk.Sentence{Size: 512}
```

`FixedSize` cuts on rune boundaries with optional overlap. `Recursive` tries each separator in `DefaultSeparators` (`"\n\n"`, `"\n"`, `". "`, `"? "`, `"! "`, `" "`, `""`) before falling back to a rune split — this is the prose default. `Sentence` packs whole sentences and never splits one across chunks.

## Embedders

Three things live in `pkg/memory`:

- `Embedder` interface — `Embed(ctx, texts) ([][]float32, error)` and `Dimensions() int`.
- `EmbedderFunc{Dim, Fn}` — adapter for plain functions.
- `HashingEmbedder{Dim}` (`NewHashingEmbedder(dim)`) — deterministic, network-free, L2-normalized feature-hashed vectors. Lexical-overlap quality only; use it to wire RAG end-to-end without an API key, then swap.

Provider-backed embedders ship with the provider modules:

```go
import (
    openaiprov "github.com/YasserCR/galdor/providers/openai"
    googleprov "github.com/YasserCR/galdor/providers/google"
)

oai, _ := openaiprov.NewEmbedder(openaiprov.EmbedderConfig{
    APIKey: os.Getenv("OPENAI_API_KEY"),
})

gem, _ := googleprov.NewEmbedder(googleprov.EmbedderConfig{
    APIKey: os.Getenv("GOOGLE_API_KEY"),
})
```

OpenAI's embedder also covers Mistral / MiniMax / Together / Groq / Azure / vLLM via `BaseURL`. Both detect the native dimensionality from the first call when `Dim` is zero, or forward `Dim` to the API to truncate (OpenAI v3 and Gemini both support it).

## Backends

| Backend | Module | Retrieval | When |
|---|---|---|---|
| `InMemoryStore` | `pkg/memory` | cosine (when vectors) or substring | tests, examples |
| `sqlite.Store` | `memory/sqlite` | FTS5 BM25, or cosine over BLOB embeddings | single-process production |
| `pgvector.Store` | `memory/pgvector` | cosine via `vector(N)` column | Postgres stacks |
| `qdrant.Store` | `memory/qdrant` | cosine over HTTP | dedicated vector DB |
| `s3vectors.Store` | `memory/s3vectors` | cosine via Amazon S3 Vectors | serverless AWS vector storage |
| `okf.Store` | `memory/okf` | FTS5 BM25 over an OKF bundle | knowledge in git (markdown + frontmatter) |

```go
sql, _ := sqlite.Open("./corpus.db")
pg,  _ := pgvector.Open(ctx, pgvector.Config{
    ConnString: os.Getenv("GALDOR_PGVECTOR_URL"),
    Dim:        1536,
})
qd,  _ := qdrant.Open(ctx, qdrant.Config{
    URL:        os.Getenv("GALDOR_QDRANT_URL"),
    Collection: "docs", Dim: 1536,
})
```

Each module's integration tests are gated by an env var: `GALDOR_PGVECTOR_URL` (libpq connection string) and `GALDOR_QDRANT_URL` (HTTP base URL). Unset means the test skips.

## Gotchas

- `sqlite.Store` requires non-empty `Chunk.ID` on `Add` — it does not generate IDs the way `InMemoryStore` does. Use a stable hash so re-ingestion is idempotent.
- `pgvector.Store` and `qdrant.Store` are vector-only. Calling `Retrieve` with `Query.Text` set but `Query.Embedding` empty returns an error. Wrap them in a `Retriever` with an `Embedder` to accept text queries.
- `InMemoryStore` ranks lexical queries with a crude substring score (hits / query-tokens). For real BM25 lexical retrieval, use `memory/sqlite`.
- The pgvector table name is interpolated into DDL; the package enforces `[a-z0-9_]+` to make that safe. Postgres-quoted identifiers aren't supported.
- Qdrant point IDs must be UUIDs or unsigned ints; the adapter SHA-1s `Chunk.ID` into a deterministic UUID so re-ingestion is still idempotent.
- `Window.Snapshot` mutates the window — evicted messages are dropped from internal storage so they aren't re-summarized on the next call.

## See also

- [provider](provider.md), [schema](schema.md) — the types `Embedder` and `Window` flow into.
- [observability](observability.md) — wrap any `Embedder` call site in a span by instrumenting the upstream `Provider`.
- [`examples/memory-rag`](../../examples/memory-rag/) — full chunk → embed → SQLite → retrieve flow.
- [`examples/okf-rag`](../../examples/okf-rag/) — BM25 and hybrid (RRF) retrieval over an OKF knowledge bundle.
