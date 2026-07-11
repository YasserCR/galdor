# ADR-016 — OKF knowledge backend (`memory/okf`)

- **Status:** Accepted
- **Date:** 2026-07-10
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

Open Knowledge Format (OKF) is an open format (Google Cloud, draft v0.1) for
representing *knowledge* as Markdown + YAML frontmatter organized in a directory
tree (a *bundle*): one `.md` file is one *concept*; markdown links between
concepts form a directed graph; the only frontmatter field the spec requires is
`type`. It is readable, git-versionable and portable — the same "knowledge in
git" thesis that motivates galdor (`docs/concepts/memory.md`).

We want a galdor agent to consume an OKF bundle **using BM25**, which already
exists in the project via `memory/sqlite` (SQLite/FTS5). Before writing code we
fix the design and, above all, a **boundary**: what enters the core and what does
not. Governing constraints (all already in force):

- The public API under `pkg/` is **stable under SemVer** since v1.0; breaking
  changes only in a future major (`CHANGELOG.md`, ADR-001).
- **No new dependencies in `pkg/` core without an ADR; adapters bring their own
  deps in their own Go module** (`CONTRIBUTING.md` §5).
- Memory backends are independent modules satisfying `memory.Store`, swapped by
  changing one constructor (`memory/sqlite`, `pgvector`, `qdrant`, `s3vectors`).

## Decisions

### D1. OKF is a `memory.Store`, delivered as module `memory/okf`

The backend implements the existing `memory.Store` interface and ships as a
sibling of `memory/sqlite`, with its own `go.mod` and `replace ... => ../..` for
local development. Same driver/adapter pattern galdor already uses; the
dependency points **`memory/okf` → core**, never the reverse. The core does not
know OKF, so it builds and tests identically with or without this module. A
top-level `knowledge/okf` family was rejected: over-structure for a single module
whose contract is `memory.Store`.

### D2. The loader and concept-first chunking live in the module

`okf.Load` parses frontmatter+body, resolves markdown links to concept ids and
produces `memory.Document`s. `ChunkConcepts` replicates the OKF reference engine:
one concept = one chunk; split by top-level `#` headings when large; and each
chunk's indexed `Text` is prefixed with `title + description + tags`, because the
SQLite FTS5 table only indexes the `text` column. A small, dependency-free
frontmatter parser (scalars, quoted strings, inline and block lists) keeps the
module's only deps the galdor core and `memory/sqlite`.

### D3. This backend is a pure lexical `Store`; hybrid lives in the core

`memory/okf` retrieves via **BM25** (no `Embedding` in the `Query`). It does not
fuse; it stays a single-responsibility `Store`. The OKF proposal's *hybrid+RRF*
result is reproduced by a generic `memory.HybridRetriever` **in the core**
(ADR-017), released **in the same v1.2.0**; `examples/okf-rag` composes it (the
OKF store's BM25 ⊕ a vector retriever) to offer `--mode bm25` and `--mode hybrid`.
The split keeps the backend honest and additive and the fusion reusable by every
backend.

### D4. The `memory.Query` / `memory.Store` contract is not modified (boundary)

Three OKF needs do **not** justify touching the stable core; they are handled
inside the module:

- **Tag (list) filtering and case-insensitive `type`.** `Query.Filter` is
  `map[string]string` exact-match and is stable API; widening its semantics would
  risk the other backends. → `okf.Store.Retrieve` interprets a reserved
  `FilterTag` key as a tag-membership post-filter and passes real metadata keys
  (e.g. `type`) down to the SQLite store unchanged.
- **Link graph / expansion.** No graph model is added to the core for one
  backend. → `outlinks` are serialized into metadata; expansion is left to the
  example or a tool.
- **Identifier-friendly tokenization.** Configured inside the module's index (or
  documented, when wrapping SQLite's default tokenizer), not by altering
  `memory/sqlite`.

This is the load-bearing decision: **a backend does not reshape the shared
contract.** Saying "no" here is what preserves "break nothing".

### D5. A thin tool ships in the same module

`okf.NewSearchTool(store)` returns a ReAct-ready `okf_search(query, type, tag)`
tool, built **on top of** the native store. It lives in `memory/okf`, which
depends on `pkg/tool` and `pkg/memory`; the core still does not depend on OKF.
This answers "native vs tool" with *both, in order*: the store is the reusable
foundation; the tool is ergonomics for agentic orchestration.

### D6. The format version (`okf_version`) is a separate axis from the module SemVer

OKF is v0.1 (draft). Following MCP's two-axis model (protocol versioned apart from
the SDK), the module treats `okf_version` as independent from its own SemVer: it
reads **permissively** (only `type` required; unknown fields/links tolerated) and
producers may enforce the stricter set. An OKF spec change is then a text
migration, not a Go-API major bump.

### D7. Released as an additive minor in lockstep (v1.2.0)

The OKF backend does not touch `pkg/`; the co-released `memory.HybridRetriever`
(ADR-017) **adds** core API without changing any existing signature. Both changes
are purely **additive** ⇒ **minor**, not major — like `s3vectors` in v1.1.0. They
release **together in v1.2.0** because `examples/okf-rag` needs the hybrid to
reproduce the proposal's thesis, so co-shipping is grounded (the example *proves*
the hybrid on OKF data).

## Consequences

**Positive.**
- Zero breakage risk by construction: the core does not change and does not know
  OKF.
- Satisfies §5: the frontmatter parser is dependency-free; no new core deps.
- Users get BM25 RAG over OKF bundles *and* an agent tool by changing one
  constructor — consistent with "swappable backends".
- The boundary (D4) keeps the `memory` contract clean for all backends.

**Negative.**
- The native mode is lexical; the typo/semantic regime where the proposal shines
  depends on the hybrid (ADR-017) and a real embedder.
- Tag filtering and graph expansion take module/example code, not a native query.
- Wrapping SQLite means FTS5's default tokenizer (`customer_id` → `customer`,
  `id`), which differs slightly from OKF's reference tokenizer; documented.

## Out of scope (deferred)

- **Graph as a first-class citizen** in `pkg/memory` (typed edges, configurable
  expansion) — only if more than one backend needs it.
- **Dense/embeddings for OKF** beyond the example's hashing stand-in.
- **Incremental re-sync** when the git bundle changes (today: `Delete` +
  re-`Add`, like the other backends).

## References

- `memory/okf/` — `okf.go`, `bundle.go`, `frontmatter.go`, `doc.go`, tests,
  `testdata/bundle/`.
- `examples/okf-rag/` — runnable end-to-end (`--mode bm25|hybrid`).
- `pkg/memory/store.go`, `pkg/memory/types.go` — the `Store`/`Query` contract,
  satisfied **without** modification.
- `memory/s3vectors/` — precedent: backend added as an additive minor (v1.1.0).
- ADR-017 — `memory.HybridRetriever` (RRF fusion), co-released in v1.2.0.
- ADR-001 (foundational decisions), ADR-002 (provider abstraction shape —
  "module per adapter" pattern).
