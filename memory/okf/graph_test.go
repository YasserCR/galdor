package okf

import (
	"context"
	"sync"
	"testing"

	"github.com/YasserCR/galdor/pkg/memory"
)

func TestBundle_OutlinksInlinks(t *testing.T) {
	b := loadBundle(t)

	out := b.Outlinks("references/metrics/mrr")
	if len(out) != 1 || out[0] != "tables/subscriptions" {
		t.Fatalf("Outlinks(mrr) = %v, want [tables/subscriptions]", out)
	}
	// The reverse edge: the table is linked from the metric.
	in := b.Inlinks("tables/subscriptions")
	if len(in) != 1 || in[0] != "references/metrics/mrr" {
		t.Fatalf("Inlinks(subscriptions) = %v, want [references/metrics/mrr]", in)
	}
	// Nothing links to the metric, and it is a leaf's source.
	if in := b.Inlinks("references/metrics/mrr"); len(in) != 0 {
		t.Fatalf("Inlinks(mrr) = %v, want none", in)
	}
	if out := b.Outlinks("tables/subscriptions"); len(out) != 0 {
		t.Fatalf("Outlinks(subscriptions) = %v, want none", out)
	}
}

func TestBundle_Neighborhood(t *testing.T) {
	b := loadBundle(t)
	if got := b.Neighborhood("references/metrics/mrr", 0); got != nil {
		t.Fatalf("Neighborhood depth 0 = %v, want nil", got)
	}
	got := b.Neighborhood("references/metrics/mrr", 1)
	if len(got) != 1 || got[0] != "tables/subscriptions" {
		t.Fatalf("Neighborhood depth 1 = %v, want [tables/subscriptions]", got)
	}
	// The table has no further outlinks, so depth 2 adds nothing.
	if got := b.Neighborhood("references/metrics/mrr", 2); len(got) != 1 {
		t.Fatalf("Neighborhood depth 2 = %v, want 1 concept", got)
	}
}

// expanderStore builds an okf.Store over the bundle's concepts.
func expanderStore(t *testing.T, b *Bundle) *Store {
	t.Helper()
	s, err := NewStore(context.Background(), ChunkConcepts(b.Concepts))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestGraphExpander_AppendsForwardNeighbors(t *testing.T) {
	ctx := context.Background()
	b := loadBundle(t)
	exp := &GraphExpander{Inner: expanderStore(t, b), Bundle: b}

	// K=1: the base ranking returns only mrr; the expander then appends its
	// single outlink, tables/subscriptions.
	res, err := exp.Retrieve(ctx, memory.Query{Text: "monthly recurring revenue", K: 1})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("len(res) = %d, want 2 (1 base + 1 expanded): %v", len(res), conceptIDs(res))
	}
	if res[0].Chunk.Metadata[MetaConceptID] != "references/metrics/mrr" {
		t.Fatalf("base hit = %q, want mrr", res[0].Chunk.Metadata[MetaConceptID])
	}
	if res[1].Chunk.Metadata[MetaConceptID] != "tables/subscriptions" {
		t.Fatalf("expanded hit = %q, want tables/subscriptions", res[1].Chunk.Metadata[MetaConceptID])
	}
	// The expanded neighbor must rank below its seed (decayed score).
	if !(res[1].Score < res[0].Score) {
		t.Fatalf("expanded score %v should be < base score %v", res[1].Score, res[0].Score)
	}
}

func TestGraphExpander_NoExpansionWithoutOutlinks(t *testing.T) {
	ctx := context.Background()
	b := loadBundle(t)
	exp := &GraphExpander{Inner: expanderStore(t, b), Bundle: b}

	// subscriptions has no outlinks, so a forward-only expander adds nothing.
	res, err := exp.Retrieve(ctx, memory.Query{Text: "past_due lifecycle state", K: 1})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("len(res) = %d, want 1: %v", len(res), conceptIDs(res))
	}
	if res[0].Chunk.Metadata[MetaConceptID] != "tables/subscriptions" {
		t.Fatalf("hit = %q, want tables/subscriptions", res[0].Chunk.Metadata[MetaConceptID])
	}
}

func TestGraphExpander_IncludeInlinks(t *testing.T) {
	ctx := context.Background()
	b := loadBundle(t)
	exp := &GraphExpander{Inner: expanderStore(t, b), Bundle: b, IncludeInlinks: true}

	// Now a subscriptions hit expands backward to the metric that links it.
	res, err := exp.Retrieve(ctx, memory.Query{Text: "past_due lifecycle state", K: 1})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("len(res) = %d, want 2: %v", len(res), conceptIDs(res))
	}
	if res[1].Chunk.Metadata[MetaConceptID] != "references/metrics/mrr" {
		t.Fatalf("inlink-expanded hit = %q, want mrr", res[1].Chunk.Metadata[MetaConceptID])
	}
}

func TestGraphExpander_NoDoubleCount(t *testing.T) {
	ctx := context.Background()
	b := loadBundle(t)
	exp := &GraphExpander{Inner: expanderStore(t, b), Bundle: b}

	// A wide query returns both mrr and subscriptions in the base set; the
	// expander must not append subscriptions a second time.
	res, err := exp.Retrieve(ctx, memory.Query{Text: "recurring revenue subscription", K: 5})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	seen := map[string]int{}
	for _, r := range res {
		seen[r.Chunk.Metadata[MetaConceptID]]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Fatalf("concept %q appears %d times, want 1", id, n)
		}
	}
}

// TestBundle_ConcurrentLazyBuild hammers the lazily-built indices (graph,
// concept lookup, directory tree) and the expander's chunk index from many
// goroutines at once, so `go test -race` proves the sync.Once guards hold.
func TestBundle_ConcurrentLazyBuild(t *testing.T) {
	b := loadBundle(t)
	exp := &GraphExpander{Inner: expanderStore(t, b), Bundle: b, IncludeInlinks: true}
	ctx := context.Background()

	var wg sync.WaitGroup
	for range 24 {
		wg.Go(func() {
			b.Outlinks("references/metrics/mrr")
			b.Inlinks("tables/subscriptions")
			b.Neighborhood("references/metrics/mrr", 2)
			b.Children("references/metrics")
			b.Dirs("")
			b.Concept("tables/subscriptions")
			_, _ = exp.Retrieve(ctx, memory.Query{Text: "recurring revenue", K: 2})
		})
	}
	wg.Wait()
}

func conceptIDs(res []memory.Result) []string {
	out := make([]string, len(res))
	for i, r := range res {
		out[i] = r.Chunk.Metadata[MetaConceptID]
	}
	return out
}
