// Package sqlite provides an embedded memory.Store backed by SQLite.
//
// Lexical retrieval uses an FTS5 virtual table with the built-in BM25
// ranker; vector retrieval iterates the chunks table and computes
// cosine similarity in process. The two modes are picked per query:
// when memory.Query.Embedding is set, vector search runs; otherwise
// FTS5 BM25 runs on memory.Query.Text. Hybrid scoring is left as a
// future optimization.
//
// The backing driver is the pure-Go modernc.org/sqlite — no CGO and
// therefore no platform-specific build hurdles. The cost is that very
// large corpora hit the brute-force cosine path; pgvector / qdrant
// adapters land in their own modules for that case.
package sqlite
