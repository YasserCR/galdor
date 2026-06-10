package qdrant

import "testing"

// Regression for audit H8: Chunk.ID must round-trip — it's stored as the
// __chunk_id payload key and restored on read (the point ID is a one-way
// hash, so the payload is the only way back).
func TestChunkFromPayload_RestoresChunkID(t *testing.T) {
	c := chunkFromPayload(map[string]any{
		payloadKeyChunkID:    "my-id",
		payloadKeyDocumentID: "doc",
		payloadKeyText:       "hello",
		"topic":              "x",
	})
	if c.ID != "my-id" {
		t.Fatalf("Chunk.ID must round-trip (regression of H8), got %q", c.ID)
	}
	if c.DocumentID != "doc" || c.Text != "hello" {
		t.Errorf("chunk fields = %+v", c)
	}
	if c.Metadata["topic"] != "x" {
		t.Errorf("metadata = %+v", c.Metadata)
	}
}
