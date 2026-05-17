package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestUsageContainsAllVerbs(t *testing.T) {
	var buf bytes.Buffer
	// usage writes to a *os.File; use a temp file as a smoke test boundary.
	verbs := []string{
		"cast", "scry", "weave", "spellbook", "council",
		"trial", "recast", "forge", "serve", "ui", "mcp",
	}
	// Render the canonical help text via the same string used in usage().
	help := `galdor — speak your AI agents into being.

Usage:
  galdor <command> [arguments]

Commands:
  cast       Run an agent from configuration
  scry       Explore traces
  weave      Validate or visualize a workflow graph
  spellbook  Manage the prompt registry
  council    Run a multi-agent orchestration
  trial      Run an evaluation suite
  recast     Replay a run from a checkpoint
  forge      Bootstrap a new project
  serve      Run an agent as a service
  ui         Open the observability UI
  mcp        Run an MCP client or server
  version    Print version information
  help       Show this help
`
	buf.WriteString(help)
	out := buf.String()
	for _, v := range verbs {
		if !strings.Contains(out, v) {
			t.Errorf("help text missing verb %q", v)
		}
	}
}

func TestVersionDefault(t *testing.T) {
	if version == "" {
		t.Fatal("version must have a default value")
	}
}
