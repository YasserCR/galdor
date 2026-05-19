package qdrant

import (
	"crypto/sha1"
	"encoding/hex"
)

// uuidFromString derives a deterministic UUID v5-style identifier
// from an arbitrary string. Qdrant requires point IDs to be either
// unsigned integers or syntactically valid UUIDs; galdor's Chunk.ID
// is a free-form string, so we hash it.
//
// The result is the SHA-1 of the input formatted as a UUID. Two
// chunks with the same ID hash to the same point ID — i.e.,
// re-ingestion of a stable Chunk.ID upserts the existing row rather
// than producing a duplicate.
func uuidFromString(s string) string {
	sum := sha1.Sum([]byte("galdor:" + s))
	h := hex.EncodeToString(sum[:16])
	// Format as 8-4-4-4-12.
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
