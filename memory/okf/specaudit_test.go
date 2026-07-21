package okf

import (
	"strings"
	"testing"
)

// Tests for the behaviors pinned directly to SPEC.md sections during the
// full-conformance audit: §4.1 Extensions, §6 index structure, §8
// Citations, §9 conformance rules.

func TestExtensionKeys_PreservedAndRoundTripped(t *testing.T) {
	// §4.1: "Consumers SHOULD preserve unknown keys when round-tripping."
	src := "---\n" +
		"type: Note\n" +
		"description: d\n" +
		"timestamp: '2026-01-01'\n" +
		"env: prod\n" +
		"owners: [ana, luis]\n" +
		"---\n\nbody text\n"
	b, err := LoadBundleFS(newMapFS(map[string]string{
		"index.md": "---\nokf_version: \"0.1\"\n---\n",
		"a.md":     src,
	}), ".")
	if err != nil {
		t.Fatalf("LoadBundleFS: %v", err)
	}
	c, ok := b.Concept("a")
	if !ok {
		t.Fatal("concept a missing")
	}
	if got := c.Metadata[MetaExtraPrefix+"env"]; got != "prod" {
		t.Fatalf("fm.env = %q, want prod", got)
	}
	if got := c.Metadata[MetaExtraPrefix+"owners"]; got != "[ana, luis]" {
		t.Fatalf("fm.owners = %q, want inline list form", got)
	}

	// Round trip: Marshal must write the unknown keys back.
	out := string(Marshal(c))
	if !strings.Contains(out, "env: prod") {
		t.Fatalf("Marshal dropped the env key:\n%s", out)
	}
	if !strings.Contains(out, "owners: [ana, luis]") {
		t.Fatalf("Marshal dropped the owners list:\n%s", out)
	}
	// And a reload of the marshaled form yields identical extras.
	b2, err := LoadBundleFS(newMapFS(map[string]string{"a.md": out}), ".")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	c2, _ := b2.Concept("a")
	for _, key := range []string{MetaExtraPrefix + "env", MetaExtraPrefix + "owners"} {
		if c2.Metadata[key] != c.Metadata[key] {
			t.Fatalf("extra %q: got %q, want %q", key, c2.Metadata[key], c.Metadata[key])
		}
	}
}

func TestValidate_MissingFrontmatterBlock(t *testing.T) {
	// §9 rule 1: every non-reserved .md must contain a parseable
	// frontmatter block.
	b, err := LoadBundleFS(newMapFS(map[string]string{
		"index.md": "---\nokf_version: \"0.1\"\n---\n",
		"bare.md":  "just a body, no frontmatter at all\n",
	}), ".")
	if err != nil {
		t.Fatalf("LoadBundleFS: %v", err)
	}
	ps := b.Validate()
	if !hasProblem(ps, "missing frontmatter block") {
		t.Fatalf("expected a missing-frontmatter error, got %v", ps)
	}
	if !HasErrors(ps) {
		t.Fatal("missing frontmatter must be an error (§9.1)")
	}
}

func TestValidate_NonRootIndexFrontmatter(t *testing.T) {
	// §6/§11: frontmatter is permitted only in the bundle-root index.md.
	b, err := LoadBundleFS(newMapFS(map[string]string{
		"index.md":     "---\nokf_version: \"0.1\"\n---\n",
		"sub/index.md": "---\ntitle: Sub\n---\n\n# Sub\n\n* [A](a.md) - a concept\n",
		"sub/a.md":     "---\ntype: Note\ndescription: d\ntimestamp: '2026-01-01'\n---\nbody\n",
	}), ".")
	if err != nil {
		t.Fatalf("LoadBundleFS: %v", err)
	}
	ps := b.Validate()
	if !hasProblem(ps, "only permitted in the bundle-root") {
		t.Fatalf("expected a non-root-frontmatter error, got %v", ps)
	}
	if !HasErrors(ps) {
		t.Fatal("non-root index frontmatter must be an error (§9.3)")
	}
}

func TestValidate_IndexEntryWithoutLink(t *testing.T) {
	b, err := LoadBundleFS(newMapFS(map[string]string{
		"index.md": "---\nokf_version: \"0.1\"\n---\n\n# Contents\n\n* a bare entry with no link\n",
		"a.md":     "---\ntype: Note\ndescription: d\ntimestamp: '2026-01-01'\n---\nbody\n",
	}), ".")
	if err != nil {
		t.Fatalf("LoadBundleFS: %v", err)
	}
	ps := b.Validate()
	if !hasProblem(ps, "index entry without a markdown link") {
		t.Fatalf("expected an entry-without-link warning, got %v", ps)
	}
	if HasErrors(ps) {
		t.Fatalf("a linkless entry is a warning, not an error: %v", ps)
	}
}

func TestCitations_Parse(t *testing.T) {
	// §8: numbered citations under a # Citations heading.
	body := "# Overview\n\nprose with a claim.\n\n# Citations\n\n" +
		"[1] [BigQuery announcement](https://cloud.google.com/blog/x)\n" +
		"[2] [Internal runbook](/references/runbook.md)\n" +
		"not a citation line\n"
	b, err := LoadBundleFS(newMapFS(map[string]string{
		"index.md": "---\nokf_version: \"0.1\"\n---\n",
		"a.md":     "---\ntype: Note\ndescription: d\ntimestamp: '2026-01-01'\n---\n" + body,
	}), ".")
	if err != nil {
		t.Fatalf("LoadBundleFS: %v", err)
	}
	cs := b.Citations("a")
	if len(cs) != 2 {
		t.Fatalf("citations = %d, want 2: %+v", len(cs), cs)
	}
	if cs[0].Number != 1 || cs[0].Title != "BigQuery announcement" ||
		!strings.HasPrefix(cs[0].Target, "https://") {
		t.Fatalf("citation[0] = %+v", cs[0])
	}
	if cs[1].Number != 2 || cs[1].Target != "/references/runbook.md" {
		t.Fatalf("citation[1] = %+v", cs[1])
	}
	// A concept with no Citations section returns nil.
	if got := b.Citations("missing"); got != nil {
		t.Fatalf("Citations(missing) = %v, want nil", got)
	}
}

func TestMarshal_NonRootIndexHasNoFrontmatter(t *testing.T) {
	// WriteBundle must emit conformant reserved files: only the root
	// index.md may carry frontmatter (§11).
	src := loadBundle(t)
	dir := t.TempDir()
	if err := WriteBundle(dir, src); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	out, err := LoadBundle(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if ps := out.Validate(); len(ps) != 0 {
		t.Fatalf("written bundle should validate clean, got: %v", ps)
	}
}
