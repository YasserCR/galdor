package okf

import (
	"context"
	"reflect"
	"testing"
)

func TestBundle_Children(t *testing.T) {
	b := loadBundle(t)
	got := b.Children("references/metrics")
	want := []string{"references/metrics/churn_rate", "references/metrics/mrr"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Children(references/metrics) = %v, want %v", got, want)
	}
	if got := b.Children("tables"); !reflect.DeepEqual(got, []string{"tables/subscriptions"}) {
		t.Fatalf("Children(tables) = %v", got)
	}
	// The root holds no concepts directly, only subdirectories.
	if got := b.Children(""); len(got) != 0 {
		t.Fatalf("Children(root) = %v, want none", got)
	}
}

func TestBundle_Dirs(t *testing.T) {
	b := loadBundle(t)
	if got := b.Dirs(""); !reflect.DeepEqual(got, []string{"references", "tables"}) {
		t.Fatalf("Dirs(root) = %v, want [references tables]", got)
	}
	if got := b.Dirs("references"); !reflect.DeepEqual(got, []string{"references/metrics"}) {
		t.Fatalf("Dirs(references) = %v", got)
	}
	// A leaf directory (only concept files) has no subdirectories.
	if got := b.Dirs("references/metrics"); len(got) != 0 {
		t.Fatalf("Dirs(references/metrics) = %v, want none", got)
	}
	// "." normalizes to root.
	if got := b.Dirs("."); !reflect.DeepEqual(got, []string{"references", "tables"}) {
		t.Fatalf(`Dirs(".") = %v`, got)
	}
}

func TestBundle_ParentAndConcept(t *testing.T) {
	b := loadBundle(t)
	if got := b.Parent("references/metrics/mrr"); got != "references/metrics" {
		t.Fatalf("Parent(mrr) = %q", got)
	}
	if got := b.Parent("tables/subscriptions"); got != "tables" {
		t.Fatalf("Parent(subscriptions) = %q", got)
	}
	c, ok := b.Concept("references/metrics/mrr")
	if !ok {
		t.Fatal("Concept(mrr) not found")
	}
	if c.Metadata[MetaType] != "Metric" {
		t.Fatalf("Concept(mrr) type = %q", c.Metadata[MetaType])
	}
	if _, ok := b.Concept("does/not/exist"); ok {
		t.Fatal("Concept(missing) reported found")
	}
}

func TestNewBrowseTool_Root(t *testing.T) {
	b := loadBundle(t)
	tl, err := NewBrowseTool(b)
	if err != nil {
		t.Fatalf("NewBrowseTool: %v", err)
	}
	if tl.Name() != "okf_browse" {
		t.Fatalf("tool name = %q", tl.Name())
	}
	out, err := tl.Execute(context.Background(), BrowseInput{})
	if err != nil {
		t.Fatalf("Execute(root): %v", err)
	}
	if !reflect.DeepEqual(out.Subdirs, []string{"references", "tables"}) {
		t.Fatalf("root Subdirs = %v", out.Subdirs)
	}
	if len(out.Concepts) != 0 {
		t.Fatalf("root Concepts = %v, want none", out.Concepts)
	}
	// The root has a real index.md (carrying okf_version), so not synthesized.
	if out.Synthesized {
		t.Fatal("root browse marked synthesized despite a real index.md")
	}
}

func TestNewBrowseTool_RealIndexDir(t *testing.T) {
	b := loadBundle(t)
	tl, _ := NewBrowseTool(b)
	out, err := tl.Execute(context.Background(), BrowseInput{Dir: "references/metrics"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// A conformant non-root index has no frontmatter, so the title is the
	// directory name.
	if out.Title != "metrics" || out.Synthesized {
		t.Fatalf("expected real 'metrics' index, got title=%q synthesized=%v", out.Title, out.Synthesized)
	}
	if len(out.Concepts) != 2 {
		t.Fatalf("concepts = %d, want 2", len(out.Concepts))
	}
	for _, c := range out.Concepts {
		if c.Type != "Metric" {
			t.Fatalf("child %q type = %q, want Metric", c.ConceptID, c.Type)
		}
		if c.Title == "" || c.Description == "" {
			t.Fatalf("child %q missing title/description", c.ConceptID)
		}
	}
}

func TestNewBrowseTool_SynthesizedDir(t *testing.T) {
	b := loadBundle(t)
	tl, _ := NewBrowseTool(b)
	// "tables" has no index.md → the browse index is synthesized.
	out, err := tl.Execute(context.Background(), BrowseInput{Dir: "tables"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !out.Synthesized {
		t.Fatal("tables browse should be synthesized (no index.md)")
	}
	if len(out.Concepts) != 1 || out.Concepts[0].ConceptID != "tables/subscriptions" {
		t.Fatalf("tables concepts = %+v", out.Concepts)
	}
}
