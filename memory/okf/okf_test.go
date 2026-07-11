package okf

import (
	"context"
	"testing"

	"github.com/YasserCR/galdor/pkg/memory"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), bundlePath())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func topConcept(t *testing.T, res []memory.Result) string {
	t.Helper()
	if len(res) == 0 {
		t.Fatal("no results")
	}
	return res[0].Chunk.Metadata[MetaConceptID]
}

func TestStore_RetrieveBM25(t *testing.T) {
	s := openStore(t)
	res, err := s.Retrieve(context.Background(), memory.Query{Text: "monthly recurring revenue", K: 3})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got := topConcept(t, res); got != "references/metrics/mrr" {
		t.Fatalf("top concept = %q, want references/metrics/mrr", got)
	}
}

func TestStore_TypeFilter(t *testing.T) {
	s := openStore(t)
	// "revenue" also matches the Warehouse Table; the type filter must
	// exclude it, leaving only Metric concepts.
	res, err := s.Retrieve(context.Background(), memory.Query{
		Text:   "revenue",
		K:      5,
		Filter: map[string]string{MetaType: "Metric"},
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected at least one Metric hit")
	}
	for _, r := range res {
		if r.Chunk.Metadata[MetaType] != "Metric" {
			t.Fatalf("type filter leaked a %q result", r.Chunk.Metadata[MetaType])
		}
	}
}

func TestStore_TagFilter(t *testing.T) {
	s := openStore(t)
	// "mrr" matches both the mrr metric and the subscriptions table, but
	// only the table carries the `billing` tag.
	res, err := s.Retrieve(context.Background(), memory.Query{
		Text:   "mrr",
		K:      5,
		Filter: map[string]string{FilterTag: "billing"},
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("tag filter returned %d results, want 1", len(res))
	}
	if got := res[0].Chunk.Metadata[MetaConceptID]; got != "tables/subscriptions" {
		t.Fatalf("tag-filtered concept = %q, want tables/subscriptions", got)
	}
}

func TestStore_TagFilterDoesNotMutateCallerMap(t *testing.T) {
	s := openStore(t)
	filter := map[string]string{FilterTag: "billing"}
	_, err := s.Retrieve(context.Background(), memory.Query{Text: "mrr", Filter: filter})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if _, ok := filter[FilterTag]; !ok {
		t.Fatal("Retrieve mutated the caller's Filter map")
	}
}

func TestNewSearchTool(t *testing.T) {
	s := openStore(t)
	tl, err := NewSearchTool(s)
	if err != nil {
		t.Fatalf("NewSearchTool: %v", err)
	}
	if tl.Name() != "okf_search" {
		t.Fatalf("tool name = %q", tl.Name())
	}
	out, err := tl.Execute(context.Background(), SearchInput{Query: "monthly recurring revenue", K: 3})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(out.Hits) == 0 {
		t.Fatal("no hits")
	}
	if out.Hits[0].ConceptID != "references/metrics/mrr" {
		t.Fatalf("top hit = %q, want references/metrics/mrr", out.Hits[0].ConceptID)
	}
	if out.Hits[0].Title == "" || out.Hits[0].Snippet == "" {
		t.Fatalf("hit missing title/snippet: %+v", out.Hits[0])
	}

	// Type filter via the tool input.
	out, err = tl.Execute(context.Background(), SearchInput{Query: "revenue", Type: "Metric", K: 5})
	if err != nil {
		t.Fatalf("Execute (typed): %v", err)
	}
	for _, h := range out.Hits {
		if h.Type != "Metric" {
			t.Fatalf("type filter via tool leaked %q", h.Type)
		}
	}
}
