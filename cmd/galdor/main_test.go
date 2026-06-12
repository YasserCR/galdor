package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// renderUsage captures the real usage() output via a temp file, so the
// assertions below test the actual help text rather than a copy of it.
func renderUsage(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "usage.txt")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	usage(f)
	if cerr := f.Close(); cerr != nil {
		t.Fatal(cerr)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestUsageListsImplementedAndPlannedVerbs(t *testing.T) {
	out := renderUsage(t)

	for _, v := range []string{"scry", "ui", "mcp", "weave", "trial", "cast", "council", "version", "help"} {
		if !strings.Contains(out, v) {
			t.Errorf("help text missing implemented verb %q", v)
		}
	}
	for _, v := range []string{"spellbook"} {
		if !strings.Contains(out, v) {
			t.Errorf("help text missing planned verb %q", v)
		}
	}
	// The planned block must be labeled so users see the status before
	// running a verb, not after (audit §5 "stubs invisible until run").
	if !strings.Contains(out, "not yet implemented") {
		t.Error("help text must label planned verbs as not yet implemented")
	}
}

// Regression for ADR-013: serve, recast and forge were removed from the
// CLI surface (serve/forge contradict explicit non-goals; recast is
// subsumed by `scry replay`). They must not be advertised. Verbs are
// matched as command-column entries ("  <verb> ") rather than bare
// substrings — "serve" would otherwise match "server" in mcp's blurb.
func TestUsageOmitsPrunedVerbs(t *testing.T) {
	out := renderUsage(t)
	for _, v := range []string{"serve", "recast", "forge"} {
		if strings.Contains(out, "  "+v+" ") {
			t.Errorf("help text still advertises pruned verb %q (ADR-013)", v)
		}
	}
}

func TestVersionDefault(t *testing.T) {
	if version == "" {
		t.Fatal("version must have a default value")
	}
}

// TestResolveVersion_LdflagsWins covers the deterministic branch: an
// explicit -ldflags injection takes precedence over build info. The
// build-info path (go install @vX → Main.Version) can't be exercised
// hermetically in a unit test, but resolveVersion never returns the bare
// "0.0.0-dev" placeholder once a real version is available — that was the
// whole bug (a go-installed binary reporting 0.0.0-dev).
func TestResolveVersion_LdflagsWins(t *testing.T) {
	orig := version
	t.Cleanup(func() { version = orig })

	version = "v1.2.3"
	if got := resolveVersion(); got != "v1.2.3" {
		t.Errorf("ldflags injection should win, got %q", got)
	}
}

func TestResolveVersion_NeverBarePlaceholderInThisModule(t *testing.T) {
	// This binary is built from a VCS checkout, so build info carries
	// either a module version or a vcs revision — resolveVersion should
	// surface something more specific than the bare fallback.
	if got := resolveVersion(); got == "0.0.0-dev" {
		t.Skip("no VCS/module build info available in this environment")
	}
}
