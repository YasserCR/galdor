package sqlite_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/YasserCR/galdor/pkg/memory"
)

// Regression for audit H11: a metadata key containing a "." (or spaces)
// must match. The old unquoted "$."+key path treated "a.b" as a nested
// lookup and never matched a flat key.
func TestRetrieve_VectorFilterKeyWithDot(t *testing.T) {
	s := newTestStore(t)
	emb := []float32{1, 0, 0}
	if err := s.Add(context.Background(), []memory.Chunk{
		{ID: "c1", DocumentID: "d", Text: "x", Embedding: emb, Metadata: map[string]string{"a.b": "want"}},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Retrieve(context.Background(), memory.Query{
		Embedding: emb, K: 5, Filter: map[string]string{"a.b": "want"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("a metadata key with a dot must match (regression of H11), got %d results", len(res))
	}
}

// Regression for audit H9: the lexical metadata filter must be pushed into
// SQL, so a matching chunk ranked below the old `k*4` overfetch window is
// still found instead of being filtered out to 0 results.
func TestRetrieve_LexicalFilterBeyondOverfetch(t *testing.T) {
	s := newTestStore(t)
	var chunks []memory.Chunk
	for i := 0; i < 20; i++ { // high-TF decoys rank above the targets
		chunks = append(chunks, memory.Chunk{
			ID: fmt.Sprintf("o%d", i), DocumentID: "d",
			Text: "apple apple apple apple apple", Metadata: map[string]string{"topic": "other"},
		})
	}
	for i := 0; i < 3; i++ {
		chunks = append(chunks, memory.Chunk{
			ID: fmt.Sprintf("w%d", i), DocumentID: "d",
			Text: "apple", Metadata: map[string]string{"topic": "want"},
		})
	}
	if err := s.Add(context.Background(), chunks); err != nil {
		t.Fatal(err)
	}
	res, err := s.Retrieve(context.Background(), memory.Query{
		Text: "apple", K: 2, Filter: map[string]string{"topic": "want"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("filtered lexical retrieve must find matching chunks ranked below the overfetch (regression of H9)")
	}
	for _, r := range res {
		if r.Chunk.Metadata["topic"] != "want" {
			t.Errorf("unexpected topic %q", r.Chunk.Metadata["topic"])
		}
	}
}
