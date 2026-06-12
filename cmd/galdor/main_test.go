package main

import (
	"context"
	"io"
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

func TestUsageListsAllVerbs(t *testing.T) {
	out := renderUsage(t)

	// Every shipped verb appears in the usage; there is no "planned" block.
	for _, v := range []string{"scry", "ui", "mcp", "weave", "trial", "cast", "council", "spellbook", "doctor", "version", "help"} {
		if !strings.Contains(out, v) {
			t.Errorf("help text missing verb %q", v)
		}
	}
	if strings.Contains(out, "not yet implemented") {
		t.Error("no verb should be labeled not-yet-implemented anymore")
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

// Asking for help is never a usage error: every verb and subcommand that
// parses flags must exit 0 on -h/--help (flag.ErrHelp), not 64. Pinned
// here through the in-process entry points.
func TestHelpFlagExitsZero(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		run  func() int
	}{
		{"ui", func() int { return runUI(ctx, []string{"--help"}, io.Discard, io.Discard) }},
		{"weave", func() int { return weave(ctx, []string{"--help"}, io.Discard, io.Discard) }},
		{"trial", func() int { return trial(ctx, []string{"--help"}, io.Discard, io.Discard) }},
		{"cast", func() int { return cast(ctx, []string{"--help"}, io.Discard, io.Discard) }},
		{"council", func() int { return councilCmd(ctx, []string{"--help"}, io.Discard, io.Discard) }},
		{"scry list", func() int { return scry(ctx, []string{"list", "--help"}, io.Discard, io.Discard) }},
		{"scry show", func() int { return scry(ctx, []string{"show", "--help"}, io.Discard, io.Discard) }},
		{"scry stats", func() int { return scry(ctx, []string{"stats", "--help"}, io.Discard, io.Discard) }},
		{"scry replay", func() int { return scry(ctx, []string{"replay", "--help"}, io.Discard, io.Discard) }},
		{"mcp serve", func() int { return mcpCmd(ctx, []string{"serve", "--help"}, io.Discard, io.Discard) }},
		{"mcp ls", func() int { return mcpCmd(ctx, []string{"ls", "--help"}, io.Discard, io.Discard) }},
		{"mcp call", func() int { return mcpCmd(ctx, []string{"call", "--help"}, io.Discard, io.Discard) }},
		{"spellbook list", func() int { return spellbookCmd(ctx, []string{"list", "--help"}, io.Discard, io.Discard) }},
		{"spellbook show", func() int { return spellbookCmd(ctx, []string{"show", "--help"}, io.Discard, io.Discard) }},
		{"spellbook render", func() int { return spellbookCmd(ctx, []string{"render", "--help"}, io.Discard, io.Discard) }},
		{"spellbook diff", func() int { return spellbookCmd(ctx, []string{"diff", "--help"}, io.Discard, io.Discard) }},
		{"doctor", func() int { return doctor(ctx, []string{"--help"}, io.Discard, io.Discard) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := tc.run(); code != 0 {
				t.Errorf("%s --help exited %d, want 0", tc.name, code)
			}
		})
	}
}
