// Command galdor is the single-binary CLI for the galdor framework.
//
// Verbs (themed, mapping to conceptual operations):
//
//	scry       explore traces (CLI / live tail)        — implemented
//	ui         open the embedded observability UI       — implemented
//	mcp        serve builtins / inspect an MCP server   — implemented
//	weave      validate or visualize a workflow graph   — implemented
//	trial      run an evaluation suite                  — planned
//	cast       run an agent from configuration          — planned
//	council    run a multi-agent orchestration          — planned
//	spellbook  manage the prompt registry               — planned
//
// Planned verbs print "not yet implemented" until their release lands
// (see ROADMAP.md). The verbs serve, recast and forge were removed from
// the surface — ADR-013 records why (serve and forge contradict explicit
// non-goals; recast is subsumed by `scry replay`).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// version is overridden at build time via -ldflags.
var version = "0.0.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage(os.Stdout)
		return
	}

	switch os.Args[1] {
	case "version", "--version", "-v":
		_, _ = fmt.Fprintf(os.Stdout, "galdor %s\n", version)
	case "help", "--help", "-h":
		usage(os.Stdout)
	case "scry":
		// Wire SIGINT/SIGTERM into ctx so `scry tail` stops cleanly on
		// Ctrl-C (its doc promises this). stop() is called before exit.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := scry(ctx, os.Args[2:], os.Stdout, os.Stderr)
		stop()
		os.Exit(code)
	case "ui":
		os.Exit(runUI(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
	case "mcp":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := mcpCmd(ctx, os.Args[2:], os.Stdout, os.Stderr)
		stop()
		os.Exit(code)
	case "weave":
		os.Exit(weave(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
	case
		"cast",
		"spellbook",
		"council",
		"trial":
		_, _ = fmt.Fprintf(os.Stderr, "galdor %s: not yet implemented — see ROADMAP.md\n", os.Args[1])
		os.Exit(64)
	default:
		_, _ = fmt.Fprintf(os.Stderr, "galdor: unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(64)
	}
}

func usage(w *os.File) {
	_, _ = fmt.Fprint(w, `galdor — speak your AI agents into being.

Usage:
  galdor <command> [arguments]

Commands:
  scry       Explore traces
  ui         Open the embedded observability dashboard (HTTP)
  mcp        Serve builtin tools over MCP, or inspect any MCP server
  weave      Validate or visualize a run's workflow graph
  version    Print version information
  help       Show this help

Planned (print "not yet implemented" when run — see ROADMAP.md):
  trial      Run an evaluation suite
  cast       Run an agent from configuration
  council    Run a multi-agent orchestration
  spellbook  Manage the prompt registry
`)
}
