package okf

import (
	"context"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/memory"
)

func TestChunkHeader_FoldsResource(t *testing.T) {
	docs, _, err := Load(bundlePath())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	chunks := ChunkConcepts(docs)

	subs := findChunk(t, chunks, "tables/subscriptions")
	if !strings.Contains(subs.Text, "resource: warehouse://acme-prod/analytics/subscriptions") {
		t.Fatalf("subscriptions chunk should fold the resource URI, got: %q", first80(subs.Text))
	}
	// A concept without a resource gets no trailing "resource:" noise.
	mrr := findChunk(t, chunks, "references/metrics/mrr")
	if strings.Contains(mrr.Text, "resource:") {
		t.Fatalf("mrr has no resource; header should not mention one: %q", first80(mrr.Text))
	}
}

func TestStore_RetrieveByResource(t *testing.T) {
	// A query naming the physical resource path resolves to the concept
	// that declares it, thanks to the folded resource URI.
	s := openStore(t)
	res, err := s.Retrieve(context.Background(), memory.Query{Text: "acme-prod analytics warehouse", K: 3})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got := topConcept(t, res); got != "tables/subscriptions" {
		t.Fatalf("resource query top concept = %q, want tables/subscriptions", got)
	}
}

func TestChunkConcepts_TagsConventionalSections(t *testing.T) {
	// A body big enough to split, with a conventional "# Schema" section.
	body := "# Overview\n" + strings.Repeat("overview prose ", 120) +
		"\n# Schema\n" + strings.Repeat("column detail ", 120)
	doc := memory.Document{
		ID:   "tables/x",
		Text: body,
		Metadata: map[string]string{
			MetaTitle: "X", MetaDesc: "d", MetaTags: "t", MetaConceptID: "tables/x",
		},
	}
	chunks := ChunkConcepts([]memory.Document{doc})

	var schema, overview *memory.Chunk
	for i := range chunks {
		switch chunks[i].ID {
		case "tables/x#schema":
			schema = &chunks[i]
		case "tables/x#overview":
			overview = &chunks[i]
		}
	}
	if schema == nil || overview == nil {
		t.Fatalf("expected #overview and #schema chunks, got ids %v", chunkIDs(chunks))
	}
	if schema.Metadata[MetaSection] != "schema" {
		t.Fatalf("schema chunk section = %q, want schema", schema.Metadata[MetaSection])
	}
	// A non-conventional section carries no section label...
	if overview.Metadata[MetaSection] != "" {
		t.Fatalf("overview chunk section = %q, want empty", overview.Metadata[MetaSection])
	}
	// ...and the document's shared metadata map was never polluted.
	if doc.Metadata[MetaSection] != "" {
		t.Fatal("ChunkConcepts mutated the document's shared metadata map")
	}
}

func TestStore_SectionFilter(t *testing.T) {
	ctx := context.Background()
	body := "# Overview\n" + strings.Repeat("overview prose ", 120) +
		"\n# Schema\n" + strings.Repeat("column detail ", 120)
	doc := memory.Document{
		ID:   "tables/x",
		Text: body,
		Metadata: map[string]string{
			MetaTitle: "X", MetaDesc: "d", MetaTags: "t", MetaConceptID: "tables/x",
		},
	}
	s, err := NewStore(ctx, ChunkConcepts([]memory.Document{doc}))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// The section filter is pushed down as an exact metadata match: a term
	// that only appears in the Overview section returns nothing under it.
	res, err := s.Retrieve(ctx, memory.Query{
		Text:   "overview prose",
		K:      5,
		Filter: map[string]string{MetaSection: "schema"},
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("section=schema on an overview-only term returned %d hits, want 0", len(res))
	}
	// A schema term under the same filter hits, and only schema chunks.
	res, err = s.Retrieve(ctx, memory.Query{
		Text:   "column detail",
		K:      5,
		Filter: map[string]string{MetaSection: "schema"},
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected a schema-section hit")
	}
	for _, r := range res {
		if r.Chunk.Metadata[MetaSection] != "schema" {
			t.Fatalf("section filter leaked a %q chunk", r.Chunk.Metadata[MetaSection])
		}
	}
}

func TestStore_TimestampFilters(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	// Fixture timestamps: mrr 2026-06-04, churn 2026-06-04, subscriptions
	// 2026-06-02. All three match "recurring".
	const cut = "2026-06-03"

	since, err := s.Retrieve(ctx, memory.Query{
		Text: "recurring", K: 5, Filter: map[string]string{FilterSince: cut},
	})
	if err != nil {
		t.Fatalf("Retrieve since: %v", err)
	}
	if len(since) == 0 {
		t.Fatal("since filter returned nothing")
	}
	for _, r := range since {
		if r.Chunk.Metadata[MetaConceptID] == "tables/subscriptions" {
			t.Fatal("since=2026-06-03 leaked the older subscriptions concept")
		}
	}

	until, err := s.Retrieve(ctx, memory.Query{
		Text: "recurring", K: 5, Filter: map[string]string{FilterUntil: cut},
	})
	if err != nil {
		t.Fatalf("Retrieve until: %v", err)
	}
	if len(until) == 0 {
		t.Fatal("until filter returned nothing")
	}
	for _, r := range until {
		if got := r.Chunk.Metadata[MetaConceptID]; got != "tables/subscriptions" {
			t.Fatalf("until=2026-06-03 kept %q, want only subscriptions", got)
		}
	}
}

func TestSearchTool_SinceFilter(t *testing.T) {
	s := openStore(t)
	tl, err := NewSearchTool(s)
	if err != nil {
		t.Fatalf("NewSearchTool: %v", err)
	}
	out, err := tl.Execute(context.Background(), SearchInput{
		Query: "recurring", Since: "2026-06-03", K: 5,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, h := range out.Hits {
		if h.ConceptID == "tables/subscriptions" {
			t.Fatal("since filter via tool leaked the older concept")
		}
	}
}

func chunkIDs(chunks []memory.Chunk) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.ID
	}
	return out
}
