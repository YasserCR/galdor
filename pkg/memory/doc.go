// Package memory defines short-term and long-term memory primitives.
//
// Short-term memory keeps the running conversation bounded — by message
// count or by token budget — and is what feeds the LLM on every turn.
// Window is the in-process implementation; a Summarizer hook can be
// plugged in to compress overflowing turns instead of dropping them.
//
// Long-term memory is the retrieval side: documents are chunked,
// optionally embedded, and stored in a Store. A Retriever queries the
// Store at agent time to bring relevant context back into the prompt.
// Store is an interface; this package ships an in-memory implementation
// (InMemoryStore) for tests, examples and quick prototypes. SQLite + BM25
// and the external vector backends (pgvector, qdrant) ship as separate
// modules under memory/<backend>/.
//
// Chunking helpers live in subpackage memory/chunk.
package memory
