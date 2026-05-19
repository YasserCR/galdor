package memory

import "time"

// Document is the ingestion unit: a piece of source content together
// with its origin and any metadata the caller wants to round-trip
// through retrieval. Documents are chunked before being written to a
// Store; the chunks carry a back-reference via Chunk.DocumentID.
type Document struct {
	// ID uniquely identifies the document inside a Store. Callers may
	// assign it (stable IDs make re-ingestion idempotent); when empty,
	// the Store assigns one.
	ID string

	// Source is the human-readable origin (file path, URL, ticket
	// reference, ...). Stored as-is; not interpreted.
	Source string

	// Text is the full document body. Chunkers split this.
	Text string

	// Metadata is opaque key/value data that flows through chunking
	// and is preserved on every resulting Chunk.
	Metadata map[string]string

	// CreatedAt records when the document was ingested. Stores that
	// support time-based filtering use this field.
	CreatedAt time.Time
}

// Chunk is the retrieval unit: a span of text small enough to embed
// and rank, together with the metadata needed to reconstruct its
// origin. A Store contains Chunks; Documents are upstream of them.
type Chunk struct {
	// ID uniquely identifies the chunk inside a Store.
	ID string

	// DocumentID points back to the parent Document.
	DocumentID string

	// Index is the chunk's ordinal within its parent document
	// (0-based). Useful for assembling neighbor windows at query time.
	Index int

	// Text is the chunk body.
	Text string

	// Embedding is the dense vector representation of Text. Stores
	// that operate on text-only retrieval (e.g., BM25) may leave it
	// nil; vector stores require it to be populated before Add.
	Embedding []float32

	// Metadata carries through from the parent Document.
	Metadata map[string]string
}

// Query is a retrieval request. At least one of Text or Embedding
// must be set. Stores that support both modes pick whichever is
// available; pure vector stores ignore Text, pure text stores ignore
// Embedding.
type Query struct {
	// Text is the natural-language query. Used by lexical stores
	// (BM25) and by hybrid stores; vector-only stores ignore it
	// unless an Embedder is plugged in upstream.
	Text string

	// Embedding is the dense vector representation of the query.
	// Required by vector-only stores.
	Embedding []float32

	// K is the maximum number of results to return. Zero or negative
	// values mean "store default" (typically 5).
	K int

	// Filter, when non-nil, restricts results to chunks whose
	// Metadata satisfies every key/value pair (exact match). Stores
	// that cannot filter ignore this field.
	Filter map[string]string
}

// Result is one hit returned by Store.Retrieve. Score's meaning is
// store-specific (cosine distance for vector stores, BM25 score for
// lexical stores), but higher always means "more relevant".
type Result struct {
	Chunk Chunk
	Score float32
}
