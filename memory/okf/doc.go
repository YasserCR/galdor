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
// description and tags. Store indexes those chunks for BM25 retrieval using
// galdor's native lexical index (memory/bm25) and layers OKF-aware concerns
// on top: tag-membership filtering (via the reserved FilterTag key) without
// changing the core memory.Query contract, and a ReAct-callable search
// tool (NewSearchTool).
//
// Open is the one-liner (Load + ChunkConcepts + NewStore); use the pieces
// directly when you also want the documents — for example to build a
// second, vector-backed source and fuse the two under a
// memory.HybridRetriever for hybrid search (see examples/okf-rag).
//
// # The whole bundle
//
// Load reads only the concepts. LoadBundle reads everything else the spec
// layers on top and returns a *Bundle:
//
//   - index.md progressive disclosure (§6): Bundle.Indexes holds each
//     directory's parsed index, and IndexFor / SynthesizeIndex serve one
//     (real or generated on the fly, in §6's entry format) for any
//     directory.
//   - log.md change history (§7): Bundle.Logs holds each directory's parsed
//     changelog — date-grouped entries under ISO-8601 `##` headings, with
//     the conventional bold kind marker (**Update**, **Creation**, ...)
//     extracted per entry.
//   - Citations (§8): Bundle.Citations / ParseCitations return the
//     numbered `[n] [text](url)` entries of a concept's # Citations
//     section, structured.
//   - okf_version (§11): Bundle.Version, read from the root index.md (the
//     only index.md where frontmatter is permitted).
//   - Producer-defined frontmatter keys (§4.1 Extensions) are preserved
//     under MetaExtraPrefix ("fm.") and written back by Marshal, so
//     round-tripping keeps unknown keys as the spec asks of consumers.
//   - The link graph (§5.3): Bundle.Outlinks / Inlinks / Neighborhood walk
//     the directed graph the concepts' cross-links form, and GraphExpander
//     wraps a Store to append a hit's neighbors to retrieval results.
//   - The directory hierarchy (§3): Bundle.Children / Dirs / Parent expose
//     the tree an id like "tables/orders" encodes.
//
// Two ReAct-callable tools sit on top: NewSearchTool finds concepts by text
// (with type / tag / timestamp / section filters), and NewBrowseTool walks
// the directory tree — search versus browse. Everything a Bundle surfaces
// beyond the concepts is optional per §9: a broken link, a missing
// okf_version or a log.md that fails to parse is a Warning, never an error.
//
// # Writing and validating
//
// The package writes OKF as well as reads it. Marshal renders a concept
// back to markdown + frontmatter, and WriteBundle writes a whole Bundle
// (concepts plus index.md / log.md) to disk — the inverse of LoadBundle for
// the standard fields. Bundle.Validate is the strict counterpart to the
// permissive loader: where LoadBundle never rejects, Validate reports every
// authoring problem (missing type, missing recommended fields, malformed
// timestamps, broken links, an unknown okf_version) as errors and warnings,
// for a producer or CI to gate on with HasErrors.
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
// # Tokenization
//
// The BM25 backend uses memory/bm25's code-aware tokenizer, which keeps a
// compound identifier AND emits its parts: `customer_id` is indexed as
// `customer_id`, `customer` and `id` (likewise `customerId`). A query for
// the whole identifier, or for any part, therefore matches — and the
// concept carrying the literal identifier outranks one that merely mentions
// the parts. This matches OKF's reference tokenizer, which also keeps the
// compound; galdor owns the tokenizer rather than inheriting an external
// engine's split-only default.
package okf
