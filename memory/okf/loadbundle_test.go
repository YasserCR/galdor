package okf

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// newMapFS builds an in-memory fs.FS from a path→contents map, for bundles
// that would be awkward to keep as testdata files.
func newMapFS(files map[string]string) fs.FS {
	m := make(fstest.MapFS, len(files))
	for p, content := range files {
		m[p] = &fstest.MapFile{Data: []byte(content)}
	}
	return m
}

func loadBundle(t *testing.T) *Bundle {
	t.Helper()
	b, err := LoadBundle(bundlePath())
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	return b
}

func TestLoadBundle_ConceptsAndVersion(t *testing.T) {
	b := loadBundle(t)
	if len(b.Concepts) != 3 {
		t.Fatalf("len(Concepts) = %d, want 3", len(b.Concepts))
	}
	if b.Version != "0.1" {
		t.Fatalf("Version = %q, want 0.1", b.Version)
	}
	// The fixture is clean: no missing types, broken links or version miss.
	if len(b.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", b.Warnings)
	}
}

func TestLoadBundle_Indexes(t *testing.T) {
	b := loadBundle(t)
	root, ok := b.Indexes[""]
	if !ok {
		t.Fatal("missing root index")
	}
	if root.Version != "0.1" {
		t.Fatalf("root index Version = %q, want 0.1", root.Version)
	}
	nested, ok := b.Indexes["references/metrics"]
	if !ok {
		t.Fatalf("missing nested index; keys=%v", indexKeys(b))
	}
	// Non-root indexes carry no frontmatter (§6); the title derives from
	// the directory name.
	if nested.Title != "metrics" {
		t.Fatalf("nested index Title = %q, want metrics", nested.Title)
	}
	if nested.Synthesized {
		t.Fatal("a real index.md must not be marked Synthesized")
	}
	if !strings.Contains(nested.Body, "MRR") {
		t.Fatalf("nested index Body missing content: %q", nested.Body)
	}
}

func TestLoadBundle_Logs(t *testing.T) {
	b := loadBundle(t)
	lg, ok := b.Logs[""]
	if !ok {
		t.Fatal("missing root log")
	}
	// §7 format: entries grouped under ## YYYY-MM-DD headings, with the
	// conventional bold kind marker extracted.
	if len(lg.Entries) != 3 {
		t.Fatalf("log entries = %d, want 3: %+v", len(lg.Entries), lg.Entries)
	}
	if lg.Entries[0].Timestamp != "2026-06-04" || lg.Entries[0].Kind != "Update" {
		t.Fatalf("entry[0] = %+v, want 2026-06-04 / Update", lg.Entries[0])
	}
	if !strings.HasPrefix(lg.Entries[0].Text, "Added the MRR metric") {
		t.Fatalf("entry[0] text = %q", lg.Entries[0].Text)
	}
	if lg.Entries[1].Timestamp != "2026-06-04" || lg.Entries[1].Kind != "Creation" {
		t.Fatalf("entry[1] = %+v, want 2026-06-04 / Creation", lg.Entries[1])
	}
	if lg.Entries[2].Timestamp != "2026-06-02" || lg.Entries[2].Kind != "Initialization" {
		t.Fatalf("entry[2] = %+v, want 2026-06-02 / Initialization", lg.Entries[2])
	}
}

func TestParseLog_FlatAndMalformedTolerated(t *testing.T) {
	// A non-conformant log: a flat item with its own timestamp, an undated
	// item, and a non-ISO date heading. The permissive parser keeps every
	// entry; Validate (not the loader) reports the structure problems.
	b, err := LoadBundleFS(newMapFS(map[string]string{
		"a.md": "---\ntype: Note\n---\nbody",
		"log.md": "# Log\n\n" +
			"- 2026-06-04T10:00:00Z flat entry with time\n" +
			"- undated entry\n" +
			"## June 5th\n" +
			"* under a bad heading\n",
	}), ".")
	if err != nil {
		t.Fatalf("LoadBundleFS: %v", err)
	}
	lg := b.Logs[""]
	if len(lg.Entries) != 3 {
		t.Fatalf("entries = %d, want 3: %+v", len(lg.Entries), lg.Entries)
	}
	if lg.Entries[0].Timestamp != "2026-06-04T10:00:00Z" {
		t.Fatalf("flat entry timestamp = %q", lg.Entries[0].Timestamp)
	}
	if lg.Entries[1].Timestamp != "" || lg.Entries[2].Timestamp != "" {
		t.Fatalf("undated entries should have empty timestamps: %+v", lg.Entries)
	}
	ps := b.Validate()
	if !hasProblem(ps, "not ISO 8601") {
		t.Fatalf("expected a bad-date-heading problem, got %v", ps)
	}
	if !hasProblem(ps, "outside any date heading") {
		t.Fatalf("expected an undated-entries problem, got %v", ps)
	}
}

func TestBundle_SynthesizeIndex(t *testing.T) {
	b := loadBundle(t)
	// "tables" has no index.md, so IndexFor must synthesize one from the
	// single concept it holds, in §6's entry format:
	// `* [Title](relative-url) - description`.
	idx := b.IndexFor("tables")
	if !idx.Synthesized {
		t.Fatal("IndexFor(tables) should be synthesized")
	}
	if !strings.Contains(idx.Body, "* [Subscriptions](subscriptions.md) - One row per subscription") {
		t.Fatalf("synthesized body not in §6 entry form: %q", idx.Body)
	}
	// The root synthesized view lists subdirectories (§6 / Appendix A).
	rootIdx := b.SynthesizeIndex("")
	if !strings.Contains(rootIdx.Body, "* [references](references/)") ||
		!strings.Contains(rootIdx.Body, "* [tables](tables/)") {
		t.Fatalf("root synthesized body missing subdir entries: %q", rootIdx.Body)
	}
	// A directory that DOES have an index.md returns the real one.
	if got := b.IndexFor("references/metrics"); got.Synthesized {
		t.Fatal("IndexFor(references/metrics) should return the real index")
	}
	// Root spelled as "." must normalize to the root index.
	if got := b.IndexFor("."); got.Synthesized {
		t.Fatal(`IndexFor(".") should resolve to the real root index`)
	}
}

func TestLoadBundle_MissingRootIndexWarns(t *testing.T) {
	// A bundle whose root has no index.md: okf_version is unknown, which is
	// a best-effort warning, never an error (§11).
	b, err := LoadBundleFS(newMapFS(map[string]string{
		"a.md": "---\ntype: Note\n---\nbody",
	}), ".")
	if err != nil {
		t.Fatalf("LoadBundleFS: %v", err)
	}
	if b.Version != "" {
		t.Fatalf("Version = %q, want empty", b.Version)
	}
	if !hasWarning(b.Warnings, "okf_version unknown") {
		t.Fatalf("expected an okf_version warning, got %v", b.Warnings)
	}
}

func TestLoad_StillSkipsReserved(t *testing.T) {
	// Backward compatibility: Load ignores index.md / log.md entirely and
	// emits no version warnings, even now that the fixture has both.
	docs, warnings, err := Load(bundlePath())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("len(docs) = %d, want 3", len(docs))
	}
	if len(warnings) != 0 {
		t.Fatalf("Load warnings = %v, want none", warnings)
	}
}

func indexKeys(b *Bundle) []string {
	out := make([]string, 0, len(b.Indexes))
	for k := range b.Indexes {
		out = append(out, k)
	}
	return out
}

func hasWarning(warnings []string, sub string) bool {
	for _, w := range warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
