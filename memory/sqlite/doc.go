// Package sqlite provides an embedded memory.Store backed by SQLite.
//
// Lexical retrieval uses an FTS5 virtual table with the built-in BM25
// ranker; vector retrieval computes cosine similarity in process. The
// two modes are picked per query: when memory.Query.Embedding is set,
// vector search runs; otherwise FTS5 BM25 runs on memory.Query.Text.
// Hybrid scoring is left as a future optimization.
//
// A metadata equality filter (memory.Query.Filter) is pushed into SQL
// via json_extract on the vector path, so a scoped query — e.g. a single
// topic — only reads and scores the matching subset instead of the whole
// table.
//
// The backing driver is the pure-Go modernc.org/sqlite — no CGO and
// therefore no platform-specific build hurdles. The cost is that very
// large, unfiltered corpora hit the brute-force cosine path; pgvector /
// qdrant adapters land in their own modules for that case.
package sqlite
