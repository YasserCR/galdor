package okf

import (
	"strings"
	"testing"
)

func TestValidate_CleanFixture(t *testing.T) {
	b := loadBundle(t)
	// The fixture is fully conformant: every concept has type, description
	// and a valid timestamp; links resolve; okf_version is 0.1.
	if ps := b.Validate(); len(ps) != 0 {
		t.Fatalf("expected a clean bundle, got problems: %v", ps)
	}
}

func TestValidate_ReportsProblems(t *testing.T) {
	// A deliberately messy bundle: no root index (missing version), one
	// concept with no type / no description / a bad timestamp / a broken
	// link, one clean concept it links to is absent.
	b, err := LoadBundleFS(newMapFS(map[string]string{
		"bad.md": "---\n" +
			"timestamp: not-a-date\n" +
			"---\n" +
			"See [ghost](ghost.md).\n",
	}), ".")
	if err != nil {
		t.Fatalf("LoadBundleFS: %v", err)
	}
	ps := b.Validate()

	wantSubstrings := []string{
		"missing okf_version",
		"missing required field: type",
		"missing recommended field: description",
		"timestamp is not ISO-8601",
		"broken link -> ghost",
	}
	for _, want := range wantSubstrings {
		if !hasProblem(ps, want) {
			t.Fatalf("expected a problem containing %q, got: %v", want, ps)
		}
	}
	// The missing type is an error; the rest are warnings.
	if !HasErrors(ps) {
		t.Fatal("HasErrors = false, want true (missing type is an error)")
	}
}

func TestValidate_UnknownMajor(t *testing.T) {
	b, err := LoadBundleFS(newMapFS(map[string]string{
		"index.md": "---\nokf_version: \"9.2\"\n---\n",
		"a.md":     "---\ntype: Note\ndescription: d\ntimestamp: '2026-01-01'\n---\nbody\n",
	}), ".")
	if err != nil {
		t.Fatalf("LoadBundleFS: %v", err)
	}
	// The loader warns on an unfamiliar major...
	if !hasWarning(b.Warnings, "unsupported major") {
		t.Fatalf("loader warnings missing the version warning: %v", b.Warnings)
	}
	// ...and so does Validate, as a warning (not an error).
	ps := b.Validate()
	if !hasProblem(ps, "unsupported major") {
		t.Fatalf("Validate missing the version problem: %v", ps)
	}
	if HasErrors(ps) {
		t.Fatalf("an unknown major must not be an error: %v", ps)
	}
}

func hasProblem(ps []Problem, sub string) bool {
	for _, p := range ps {
		if strings.Contains(p.Message, sub) {
			return true
		}
	}
	return false
}
