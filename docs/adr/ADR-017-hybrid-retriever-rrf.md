# ADR-017 — Hybrid retrieval via Reciprocal Rank Fusion (`memory.HybridRetriever`)

- **Status:** Accepted
- **Date:** 2026-07-10
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

`pkg/memory` retrieves by **one** modality at a time: lexical (BM25, when the
`Query` carries no `Embedding`) or vector (when it does). There is no way to
**fuse** two rankings. The industry-standard way to combine a lexical ranking
with a dense one is **Reciprocal Rank Fusion (RRF)** (Cormack, Clarke & Büttcher,
SIGIR 2009): sum `1/(k+rank)` per document across lists, with `k=60` the
near-universal default; it needs no score calibration between systems, which is
exactly why it is the standard way to fuse a BM25 ranking with a vector one.

The concrete trigger is the OKF integration (ADR-016): its proposal *measures*
that hybrid wins (on typo queries, BM25 and dense score 0.75 each but RRF combines
them to 1.0 — "fusion doesn't average, it adds evidence"). Without a fuser,
`examples/okf-rag` cannot reproduce that thesis. But the need is **not
OKF-specific**: any deployment with a lexical store and a vector store has it.
So fusion goes into the **core** as a generic piece, not inside a backend.

Governing constraints: `pkg/` API stable under SemVer (additive-only in a minor);
no new core deps without an ADR (RRF is pure Go — none); score convention is
higher-is-better.

## Decisions

### D1. `HybridRetriever` is an additive composer, not a `Store`

A new type is added to `pkg/memory` that **composes** retrieval sources and fuses
their rankings; it does not implement `Add`/`Delete` (it is not a store). No
existing signature changes (`Store`, `Query`, `Result`, `Retriever` are
untouched) ⇒ purely additive (minor).

```go
// Searcher is the minimal capability HybridRetriever composes. Both
// memory.Store and *memory.Retriever satisfy it unchanged.
type Searcher interface {
    Retrieve(ctx context.Context, q Query) ([]Result, error)
}

type HybridRetriever struct {
    Sources []Searcher // e.g. [okfStore (lexical), vectorRetriever (dense)]
    K       int        // final top-k (Query.K wins; else K; else 5)
    RRFK    int        // fusion constant (default 60)
    Pool    int        // per-source fetch depth (default max(4*K, 20))
}

func (h *HybridRetriever) Retrieve(ctx context.Context, q Query) ([]Result, error)
```

### D2. Each source is queried with the **same** Query; the source picks the modality

`HybridRetriever` does **not** embed or tokenize: it calls `Retrieve(ctx, q)` on
each source with the query as-is. A vector source is passed as a
`*memory.Retriever{Store: vec, Embedder: emb}` that internally turns `q.Text` into
an embedding; a lexical source (the OKF store, or a `*Retriever` with a nil
Embedder) ignores the embedding and does BM25. The fuser reuses existing pieces
without knowing their modalities, and adding a third source is trivial.

### D3. Fuse by `Chunk.ID` with RRF; score = Σ `1/(RRFK+rank)`

Each source yields a ranked list; rank (1-based) accumulates `1/(RRFK+rank)` per
`Chunk.ID`. Results sort descending (higher = better, consistent with the rest)
and the top-`K` are returned. A `Pool` (≥ K) is fetched per source so fusion has
overlap to work with. The returned `Result` carries the `Chunk` from the first
source that surfaced it. ID-less chunks fall back to a `(DocumentID, Index)`
composite key so they do not collide.

### D4. Safe, deterministic defaults

`RRFK` defaults to **60** (the literature and OKF-reference default). Score ties
break **stably** by first-seen order, so results are deterministic and
replay-friendly (`pkg/replay`). A source error is **propagated**, not masked, so a
degraded ranking is never returned silently.

### D5. Lives in `pkg/memory`, no new deps

RRF is stdlib `sort`/maps. It does not trip §5. It is documented with a runnable
`ExampleHybridRetriever` in the package, which doubles as a live test.

## Consequences

**Positive.**
- Enables hybrid search for **any** combination of backends, not just OKF.
- Reuses `Store`/`Retriever`/`Embedder`; minimal surface (one type + a
  one-method interface).
- Additive and dependency-free ⇒ a clean minor; nothing existing breaks.
- Reproduces the OKF proposal's thesis in Go; `okf-rag --mode hybrid` demos it.

**Negative.**
- RRF ignores score magnitude (ranks only); robust but does not exploit
  confidence signals. Weighted fusion and cross-encoder reranking are out of
  scope.
- Querying N sources multiplies per-query work; mitigated by the bounded pool
  and, if needed later, concurrent retrieval.

## Out of scope (deferred)

- **Weighted fusion** (per-source weights) or score normalization (CombSUM/MNZ).
- **Cross-encoder reranking** as a later stage.
- **Concurrent source retrieval** (goroutines + errgroup).
- **Graph expansion** inside the fuser (belongs to the example / OKF adapter,
  ADR-016 D4).

## References

- `pkg/memory/hybrid.go`, `pkg/memory/hybrid_test.go`,
  `pkg/memory/example_hybrid_test.go`.
- `pkg/memory/store.go` (`Retriever`, `Store`), `pkg/memory/types.go` — reused
  unchanged.
- ADR-016 — OKF knowledge backend (the motivating consumer; co-released).
- Cormack, Clarke & Büttcher, *Reciprocal Rank Fusion outperforms Condorcet and
  individual rank learning methods*, SIGIR 2009.
