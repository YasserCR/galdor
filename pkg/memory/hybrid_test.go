package memory

import (
	"context"
	"errors"
	"testing"
)

// stubSearcher returns a fixed ranking, ignoring the query. It lets the
// fusion logic be tested in isolation from any real store.
type stubSearcher struct {
	res []Result
	err error
}

func (s stubSearcher) Retrieve(_ context.Context, _ Query) ([]Result, error) {
	return s.res, s.err
}

// ranking builds a ranked result slice from chunk IDs (rank = index).
func ranking(ids ...string) []Result {
	out := make([]Result, len(ids))
	for i, id := range ids {
		out[i] = Result{Chunk: Chunk{ID: id, DocumentID: id, Text: id}}
	}
	return out
}

func ids(rs []Result) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Chunk.ID
	}
	return out
}

func TestHybridRetriever_FusesRankings(t *testing.T) {
	// A: X, Y, Z   B: Y, X, W
	// X = 1/61 + 1/62,  Y = 1/62 + 1/61  (equal, X seen first → X before Y)
	// Z = 1/63,         W = 1/63         (equal, Z seen first → Z before W)
	h := &HybridRetriever{
		Sources: []Searcher{
			stubSearcher{res: ranking("X", "Y", "Z")},
			stubSearcher{res: ranking("Y", "X", "W")},
		},
	}
	got, err := h.Retrieve(context.Background(), Query{Text: "q"})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	want := []string{"X", "Y", "Z", "W"}
	if g := ids(got); !equalStrings(g, want) {
		t.Fatalf("order = %v, want %v", g, want)
	}
	if got[0].Score <= got[2].Score {
		t.Fatalf("expected fused top score (%.5f) > tail score (%.5f)", got[0].Score, got[2].Score)
	}
	// X and Y must tie exactly on score; only first-seen order separates them.
	if got[0].Score != got[1].Score {
		t.Fatalf("X and Y should tie: %.6f vs %.6f", got[0].Score, got[1].Score)
	}
}

func TestHybridRetriever_ExactRRFScore(t *testing.T) {
	h := &HybridRetriever{Sources: []Searcher{stubSearcher{res: ranking("A", "B")}}}
	got, err := h.Retrieve(context.Background(), Query{Text: "q"})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	// Single source: A at rank 0 → 1/(60+1); B at rank 1 → 1/(60+2).
	wantA := float32(1.0 / 61.0)
	wantB := float32(1.0 / 62.0)
	if got[0].Score != wantA || got[1].Score != wantB {
		t.Fatalf("scores = %.6f,%.6f want %.6f,%.6f", got[0].Score, got[1].Score, wantA, wantB)
	}
}

func TestHybridRetriever_RespectsK(t *testing.T) {
	h := &HybridRetriever{
		Sources: []Searcher{stubSearcher{res: ranking("A", "B", "C", "D", "E")}},
		K:       2,
	}
	got, err := h.Retrieve(context.Background(), Query{Text: "q"})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (K)", len(got))
	}
	// Query.K overrides the struct K.
	got, err = h.Retrieve(context.Background(), Query{Text: "q", K: 3})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (Query.K)", len(got))
	}
}

func TestHybridRetriever_NoSources(t *testing.T) {
	h := &HybridRetriever{}
	if _, err := h.Retrieve(context.Background(), Query{Text: "q"}); err == nil {
		t.Fatal("expected error for empty Sources")
	}
}

func TestHybridRetriever_NilSource(t *testing.T) {
	h := &HybridRetriever{Sources: []Searcher{nil}}
	if _, err := h.Retrieve(context.Background(), Query{Text: "q"}); err == nil {
		t.Fatal("expected error for nil Source")
	}
}

func TestHybridRetriever_PropagatesSourceError(t *testing.T) {
	sentinel := errors.New("boom")
	h := &HybridRetriever{Sources: []Searcher{
		stubSearcher{res: ranking("A")},
		stubSearcher{err: sentinel},
	}}
	if _, err := h.Retrieve(context.Background(), Query{Text: "q"}); !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want %v", err, sentinel)
	}
}

func TestHybridRetriever_IDlessChunksFuseByComposite(t *testing.T) {
	// Two chunks with empty ID but distinct (DocumentID, Index) must not
	// collide into one fused entry.
	a := Result{Chunk: Chunk{DocumentID: "doc", Index: 0, Text: "a"}}
	b := Result{Chunk: Chunk{DocumentID: "doc", Index: 1, Text: "b"}}
	h := &HybridRetriever{Sources: []Searcher{stubSearcher{res: []Result{a, b}}}}
	got, err := h.Retrieve(context.Background(), Query{Text: "q"})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 distinct ID-less chunks", len(got))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
