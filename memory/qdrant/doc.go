// Package qdrant provides a memory.Store backed by Qdrant.
//
// The adapter speaks Qdrant's HTTP REST API rather than gRPC to keep
// the dependency surface minimal (net/http + encoding/json from the
// standard library). The cost is a small amount of per-request JSON
// overhead; the benefit is no protobuf toolchain and a much smaller
// transitive dependency tree.
//
// Collections are created on Open with the configured dimensionality
// and the Cosine distance metric; if the collection already exists,
// Open is a no-op. Galdor's memory.Result.Score follows the
// higher-is-better convention used everywhere; Qdrant's cosine score
// already follows the same convention so no inversion is needed.
//
// Metadata round-trips through Qdrant's payload object. The Chunk
// fields (DocumentID, Index, Text) are stored as reserved payload
// keys (`__document_id`, `__index`, `__text`) so they don't collide
// with user-supplied metadata.
package qdrant
