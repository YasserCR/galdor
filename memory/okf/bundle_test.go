package okf

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/memory"
)

func bundlePath() string { return filepath.Join("testdata", "bundle") }

func TestLoad(t *testing.T) {
	docs, warnings, err := Load(bundlePath())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// index.md is reserved and must be skipped: 3 concepts remain.
	if len(docs) != 3 {
		t.Fatalf("len(docs) = %d, want 3; ids=%v", len(docs), docIDs(docs))
	}
	// No broken links or missing types in the fixture bundle.
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	mrr := findDoc(t, docs, "references/metrics/mrr")
	if mrr.Metadata[MetaType] != "Metric" {
		t.Fatalf("mrr type = %q", mrr.Metadata[MetaType])
	}
	if mrr.Metadata[MetaTags] != "metric,revenue,recurring,mrr" {
		t.Fatalf("mrr tags = %q", mrr.Metadata[MetaTags])
	}
	// The markdown link to the subscriptions table must resolve to its id.
	if !strings.Contains(mrr.Metadata[MetaOutlinks], "tables/subscriptions") {
		t.Fatalf("mrr outlinks = %q, want to contain tables/subscriptions", mrr.Metadata[MetaOutlinks])
	}
}

func TestChunkConcepts_ShortBodyOneChunk(t *testing.T) {
	docs, _, err := Load(bundlePath())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	chunks := ChunkConcepts(docs)
	// Every fixture body is short → exactly one chunk per concept.
	if len(chunks) != len(docs) {
		t.Fatalf("len(chunks) = %d, want %d", len(chunks), len(docs))
	}
	mrr := findChunk(t, chunks, "references/metrics/mrr")
	// Header fold: title, description and tags must be in the indexed text.
	if !strings.HasPrefix(mrr.Text, "MRR (Monthly Recurring Revenue). ") {
		t.Fatalf("chunk text should start with the folded title, got %q", first80(mrr.Text))
	}
	if !strings.Contains(mrr.Text, "tags: metric, revenue, recurring, mrr") {
		t.Fatalf("chunk text should fold tags, got %q", first80(mrr.Text))
	}
}

func TestChunkConcepts_LongBodySplitsByHeading(t *testing.T) {
	long := "para\n\n# First\n" + strings.Repeat("a ", 400) + "\n# Second\n" + strings.Repeat("b ", 400)
	doc := memory.Document{
		ID:   "big",
		Text: long,
		Metadata: map[string]string{
			MetaTitle: "Big", MetaDesc: "d", MetaTags: "x", MetaConceptID: "big",
		},
	}
	chunks := ChunkConcepts([]memory.Document{doc})
	if len(chunks) < 2 {
		t.Fatalf("expected the long body to split into >=2 chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if c.DocumentID != "big" {
			t.Fatalf("chunk DocumentID = %q, want big", c.DocumentID)
		}
	}
}

func TestResolveLink(t *testing.T) {
	cases := []struct {
		target, docDir, want string
	}{
		{"../../tables/subscriptions.md", "references/metrics", "tables/subscriptions"},
		{"/tables/x.md", "references/metrics", "tables/x"},
		{"sibling.md", "tables", "tables/sibling"},
		{"https://example.com", "tables", ""},
		{"mailto:a@b.c", "tables", ""},
		{"other.md#anchor", "tables", "tables/other"},
	}
	for _, c := range cases {
		if got := resolveLink(c.target, c.docDir); got != c.want {
			t.Errorf("resolveLink(%q, %q) = %q, want %q", c.target, c.docDir, got, c.want)
		}
	}
}

func docIDs(docs []memory.Document) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.ID
	}
	return out
}

func findDoc(t *testing.T, docs []memory.Document, id string) memory.Document {
	t.Helper()
	for _, d := range docs {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("doc %q not found in %v", id, docIDs(docs))
	return memory.Document{}
}

func findChunk(t *testing.T, chunks []memory.Chunk, docID string) memory.Chunk {
	t.Helper()
	for _, c := range chunks {
		if c.DocumentID == docID {
			return c
		}
	}
	t.Fatalf("chunk for %q not found", docID)
	return memory.Chunk{}
}

func first80(s string) string {
	if len(s) > 80 {
		return s[:80]
	}
	return s
}
