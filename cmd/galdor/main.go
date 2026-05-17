// Command galdor is the single-binary CLI for the galdor framework.
//
// Verbs (themed, mapping to conceptual operations):
//
//	cast       run an agent from configuration
//	scry       explore traces (CLI / live tail)
//	weave      validate or visualize a workflow graph
//	spellbook  manage the prompt registry
//	council    run a multi-agent orchestration
//	trial      run an evaluation suite
//	recast     replay a run from a checkpoint
//	forge      bootstrap a new project
//	serve      run an agent as an HTTP/gRPC service
//	ui         open the embedded observability UI
//	mcp        run an MCP client or server
//
// During Phase 0 every verb is a placeholder. Real implementations land in
// their respective phases (see ROADMAP.md).
package main

import (
	"fmt"
	"os"
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
		fmt.Fprintf(os.Stdout, "galdor %s\n", version)
	case "help", "--help", "-h":
		usage(os.Stdout)
	case
		"cast",
		"scry",
		"weave",
		"spellbook",
		"council",
		"trial",
		"recast",
		"forge",
		"serve",
		"ui",
		"mcp":
		fmt.Fprintf(os.Stderr, "galdor %s: not yet implemented (Phase 0)\n", os.Args[1])
		os.Exit(64)
	default:
		fmt.Fprintf(os.Stderr, "galdor: unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(64)
	}
}

func usage(w *os.File) {
	fmt.Fprint(w, `galdor — speak your AI agents into being.

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

This is a Phase 0 placeholder binary. See ROADMAP.md.
`)
}
