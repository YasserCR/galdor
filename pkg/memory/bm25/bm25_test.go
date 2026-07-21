package bm25

import (
	"context"
	"sync"
	"testing"

	"github.com/YasserCR/galdor/pkg/memory"
)

// Store must satisfy the memory.Store contract.
var _ memory.Store = (*Store)(nil)

func mustAdd(t *testing.T, s *Store, chunks ...memory.Chunk) {
	t.Helper()
	if err := s.Add(context.Background(), chunks); err != nil {
		t.Fatalf("Add: %v", err)
	}
}

func chunk(id, text string, meta map[string]string) memory.Chunk {
	return memory.Chunk{ID: id, DocumentID: id, Text: text, Metadata: meta}
}

func topID(t *testing.T, res []memory.Result) string {
	t.Helper()
	if len(res) == 0 {
		t.Fatal("no results")
	}
	return res[0].Chunk.ID
}

func TestStore_RanksByRelevance(t *testing.T) {
	s := New()
	mustAdd(t,
		s,
		chunk("a", "monthly recurring revenue from subscriptions", nil),
		chunk("b", "the weather today is sunny and warm", nil),
		chunk("c", "annual revenue report and forecast", nil),
	)
	res, err := s.Retrieve(context.Background(), memory.Query{Text: "recurring revenue", K: 5})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got := topID(t, res); got != "a" {
		t.Fatalf("top = %q, want a", got)
	}
	// The unrelated chunk must not appear.
	for _, r := range res {
		if r.Chunk.ID == "b" {
			t.Fatal("irrelevant chunk b was returned")
		}
	}
}

// TestStore_CompoundIdentifier is the raison d'être of this backend: an
// identifier is findable by its whole form AND by its parts, and the doc
// carrying the literal identifier outranks one with only coincidental parts.
func TestStore_CompoundIdentifier(t *testing.T) {
	ctx := context.Background()
	s := New()
	mustAdd(t,
		s,
		chunk("literal", "the customer_id column is the table primary key", nil),
		chunk("scattered", "the customer paid and an id was later assigned", nil),
	)

	// Whole-identifier query: the literal doc wins (it matches customer_id,
	// customer AND id; the scattered doc only customer and id).
	res, err := s.Retrieve(ctx, memory.Query{Text: "customer_id", K: 5})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got := topID(t, res); got != "literal" {
		t.Fatalf("customer_id top = %q, want literal", got)
	}

	// Part query: still matches the literal doc — part-matching is NOT lost
	// (this is what an underscore-as-token-char tokenizer would break).
	res, err = s.Retrieve(ctx, memory.Query{Text: "customer", K: 5})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	var sawLiteral bool
	for _, r := range res {
		if r.Chunk.ID == "literal" {
			sawLiteral = true
		}
	}
	if !sawLiteral {
		t.Fatal(`query "customer" did not match the doc containing customer_id`)
	}
}

func TestStore_MetadataFilter(t *testing.T) {
	ctx := context.Background()
	s := New()
	mustAdd(t,
		s,
		chunk("m1", "revenue metric definition", map[string]string{"type": "Metric"}),
		chunk("t1", "revenue table columns", map[string]string{"type": "Table"}),
	)
	res, err := s.Retrieve(ctx, memory.Query{Text: "revenue", K: 5, Filter: map[string]string{"type": "Metric"}})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(res) != 1 || res[0].Chunk.ID != "m1" {
		t.Fatalf("filtered result = %v, want [m1]", ids(res))
	}
}

func TestStore_DeleteAndLen(t *testing.T) {
	ctx := context.Background()
	s := New()
	mustAdd(t,
		s,
		chunk("a", "alpha revenue", nil),
		chunk("b", "beta revenue", nil),
	)
	if n, _ := s.Len(ctx); n != 2 {
		t.Fatalf("Len = %d, want 2", n)
	}
	if err := s.Delete(ctx, "a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if n, _ := s.Len(ctx); n != 1 {
		t.Fatalf("Len after delete = %d, want 1", n)
	}
	res, _ := s.Retrieve(ctx, memory.Query{Text: "revenue", K: 5})
	if got := topID(t, res); got != "b" {
		t.Fatalf("after deleting a, top = %q, want b", got)
	}
}

func TestStore_OverwriteByID(t *testing.T) {
	ctx := context.Background()
	s := New()
	mustAdd(t, s, chunk("x", "original apple text", nil))
	mustAdd(t, s, chunk("x", "replaced banana text", nil)) // same ID
	if n, _ := s.Len(ctx); n != 1 {
		t.Fatalf("Len = %d, want 1 after overwrite", n)
	}
	// The old term is gone; the new one is searchable.
	if res, _ := s.Retrieve(ctx, memory.Query{Text: "apple"}); len(res) != 0 {
		t.Fatalf("stale term still indexed: %v", ids(res))
	}
	if res, _ := s.Retrieve(ctx, memory.Query{Text: "banana"}); len(res) != 1 {
		t.Fatalf("new term not indexed: %v", ids(res))
	}
}

func TestStore_EmptyEdgeCases(t *testing.T) {
	ctx := context.Background()
	s := New()
	if res, err := s.Retrieve(ctx, memory.Query{Text: "anything"}); err != nil || res != nil {
		t.Fatalf("empty corpus: res=%v err=%v, want nil,nil", res, err)
	}
	if _, err := s.Retrieve(ctx, memory.Query{Text: ""}); err == nil {
		t.Fatal("empty query text should error")
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	s := New()
	mustAdd(t, s, chunk("seed", "recurring revenue seed", nil))

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			_ = s.Add(ctx, []memory.Chunk{chunk("d"+itoa(i), "recurring revenue doc", nil)})
			_, _ = s.Retrieve(ctx, memory.Query{Text: "revenue", K: 3})
			_, _ = s.Len(ctx)
		})
	}
	wg.Wait()
}

func ids(res []memory.Result) []string {
	out := make([]string, len(res))
	for i, r := range res {
		out[i] = r.Chunk.ID
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
