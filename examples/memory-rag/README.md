# examples/memory-rag

The full retrieval-augmented generation flow end-to-end against a
local SQLite store and an offline hashing embedder. No network and
no API key required — the plumbing is identical to a real setup,
only the embedder and provider are deterministic stand-ins.

## Run

```bash
go run ./examples/memory-rag
```

Expected output:

```
Ingested 6 chunks from 3 documents.

Question: What is the capital of Ecuador and how high is it?

Top 3 retrieved chunks:
  1. [doc-bogota] score=0.620
     Bogotá is the capital of Colombia. Founded in 1538 on the Sabana de Bogotá plateau, it sits at 2,640...
  2. [doc-quito] score=0.585
     Quito is the capital of Ecuador. Located at 2,850 m in the Andes, it is the second-highest capital c...
  3. [doc-lima] score=0.519
     Lima is the capital of Peru and the country's largest city.

Answer: Based on the context: Bogotá is the capital of Colombia. Founded in 1538 on the Sabana de Bogotá plateau, it sits at 2,640 m and is one of the largest high-altitude cities globally.
```

Note the retrieval ranking: the `HashingEmbedder` matches on lexical
overlap, not meaning, so "capital" pulls Bogotá above Quito. That is
the point — swap in a real semantic embedder and the same wiring
returns Quito first.

## What it shows

- **The ingest pipeline is chunk → embed → add.** `chunk.Recursive`
  splits each `memory.Document`; the embedder vectorizes every chunk;
  `store.Add` writes them to the SQLite-backed `memory.Store`.
- **Stable chunk IDs make re-ingestion idempotent.** `chunkID`
  derives a deterministic ID from the document ID + ordinal, so the
  same chunk overwrites its row instead of duplicating.
- **`memory.Retriever` embeds the query for you.** Give it a `Store`,
  an `Embedder` and a `DefaultK`; it returns the top-K chunks by
  cosine similarity.
- **Retrieved context is spliced into the system prompt.** The
  scripted provider then "answers" from that context — exactly where
  a real LLM call would go.

## Run against real components

Swap two pieces; the rest of the wiring is unchanged:

- **Embedder** — replace `memory.NewHashingEmbedder` with a
  provider-backed one, e.g.:

  ```go
  import openaiprov "github.com/YasserCR/galdor/providers/openai"

  emb, _ := openaiprov.NewEmbedder(openaiprov.EmbedderConfig{
      APIKey: os.Getenv("OPENAI_API_KEY"), // text-embedding-3-small (1536-d)
  })
  ```

  The same constructor works against any OpenAI-compatible endpoint
  by setting `BaseURL`; for Gemini use `providers/google.NewEmbedder`.

- **Store** — `sqlite.Open(":memory:")` becomes a file path for
  persistence, or swap the module for `memory/pgvector` /
  `memory/qdrant` for an indexed store at scale.

- **Provider** — replace the scripted provider with
  anthropic/openai/google/bedrock to let the model write the answer.
