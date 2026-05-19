package qdrant

import (
	"crypto/sha256"
	"encoding/hex"
)

// uuidFromString derives a deterministic UUID-shaped identifier
// from an arbitrary string. Qdrant requires point IDs to be either
// unsigned integers or syntactically valid UUIDs; galdor's Chunk.ID
// is a free-form string, so we hash it.
//
// The hash is SHA-256 truncated to 16 bytes and formatted as a UUID.
// (Stricter UUID specs reserve specific bits for version/variant;
// we don't bother — Qdrant only checks the 8-4-4-4-12 shape, not
// the bit layout.) Two chunks with the same source string hash to
// the same point ID, so re-ingestion of a stable Chunk.ID upserts
// instead of duplicating.
func uuidFromString(s string) string {
	sum := sha256.Sum256(append([]byte("galdor:"), s...))
	h := hex.EncodeToString(sum[:16])
	// Format as 8-4-4-4-12.
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
