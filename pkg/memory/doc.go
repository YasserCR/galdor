// Package memory defines short-term and long-term memory interfaces.
//
// Built-in implementations include an in-memory sliding window for short
// term memory and an embedded backend (SQLite + BM25) for long-term memory.
// External vector stores (pgvector, qdrant, weaviate, chroma) live as
// separate Go modules under memory/<backend>/.
//
// Status: stub (Phase 6).
package memory
