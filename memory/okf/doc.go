// Package okf provides a memory.Store over Open Knowledge Format (OKF)
// bundles: knowledge represented as Markdown files with YAML frontmatter,
// organized in a git-versioned directory tree. One .md file is one
// concept; markdown links between concepts form a directed graph; the only
// frontmatter field the spec requires is `type`.
//
// # What this backend does
//
// Load reads a bundle into memory.Documents (one per non-reserved .md
// file). ChunkConcepts turns them into retrieval chunks concept-first: one
// chunk per concept, split by top-level headings only when the body is
// large, with each chunk's indexed text prefixed by the concept's title,
// description and tags. Store indexes those chunks for BM25 retrieval by
// wrapping galdor's SQLite/FTS5 store — reusing that proven lexical ranker
// rather than reimplementing one — and layers OKF-aware concerns on top:
// tag-membership filtering (via the reserved FilterTag key) without
// changing the core memory.Query contract, and a ReAct-callable search
// tool (NewSearchTool).
//
// Open is the one-liner (Load + ChunkConcepts + NewStore); use the pieces
// directly when you also want the documents — for example to build a
// second, vector-backed source and fuse the two under a
// memory.HybridRetriever for hybrid search (see examples/okf-rag).
//
// # Stability and versioning
//
// This backend is a lexical (BM25) Store. Hybrid retrieval (BM25 fused
// with a dense ranking via Reciprocal Rank Fusion) is provided generically
// by memory.HybridRetriever in the core, not here.
//
// The OKF format itself is versioned independently of this module: a
// bundle declares its spec revision via `okf_version` in index.md, and this
// reader is deliberately permissive (only `type` is required; unknown
// fields, unknown types and broken links are tolerated, not rejected). A
// change to the OKF spec is therefore a text migration, not a breaking
// change to this module's Go API.
//
// # Tokenization note
//
// Because this backend wraps the SQLite/FTS5 store, tokenization follows
// FTS5's default unicode61 tokenizer: `customer_id` matches via its parts
// (`customer`, `id`) rather than as a single compound token. This differs
// slightly from OKF's reference tokenizer, which also keeps the compound.
// It is adequate for identifier and SQL matching; a module-owned tokenizer
// is a possible future refinement.
package okf
