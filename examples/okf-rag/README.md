# okf-rag

Retrieval-augmented generation over an **Open Knowledge Format (OKF)** bundle,
using galdor's BM25 backend (`memory/okf`) with an optional **hybrid** mode that
fuses BM25 with a dense ranking via `memory.HybridRetriever` (Reciprocal Rank
Fusion). No network or API key required.

## Run

```bash
go run ./examples/okf-rag                              # hybrid, default question
go run ./examples/okf-rag --mode bm25 "retention"      # pure lexical
go run ./examples/okf-rag --mode bm25 "mrr_amount"     # exact identifier match
go run ./examples/okf-rag --mode hybrid "how is recurring revenue measured"
```

Flags: `--mode bm25|hybrid` (default `hybrid`), `--k N` (default 3).

## What it shows

1. An embedded OKF bundle (`bundle/`, markdown + YAML frontmatter) is loaded and
   chunked **concept-first** by `memory/okf`.
2. **`--mode bm25`** queries the concepts lexically via galdor's SQLite/FTS5 BM25
   — great for exact identifiers and SQL snippets.
3. **`--mode hybrid`** also embeds the concepts (offline `HashingEmbedder`) into a
   vector store and fuses the BM25 and dense rankings with RRF (k=60).
4. Retrieved concepts are spliced into a scripted provider's system prompt, which
   answers from that context, citing concepts by id.

## Making it real

- Swap `memory.NewHashingEmbedder` for a provider-backed embedder
  (`providers/openai.NewEmbedder`, `providers/google.NewEmbedder`) — the dense
  side then contributes true semantic recall (the hashing embedder only captures
  lexical overlap). The wiring is identical.
- Swap the `scriptedProvider` for `anthropic` / `openai` / `google` / `bedrock`.
- Expose the bundle to an agent instead of a prompt: `okf.NewSearchTool(store)`
  returns a ReAct-callable `okf_search` tool (filterable by concept `type` or
  `tag`).

## Notes

BM25 scores are **corpus-relative**: on this tiny 4-concept bundle a term shared
by most concepts (e.g. "mrr") has near-zero IDF and thus a near-zero score, while
a discriminative term ("retention") scores clearly. The *ranking* is what
matters. The hybrid mode's RRF scores are rank-based and always meaningful.

For the reasoning behind this backend and its versioning, see ADR-016 (OKF
backend) and ADR-017 (hybrid RRF retriever).
