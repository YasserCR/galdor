// Command galdor is the single-binary CLI for the galdor framework.
//
// Verbs (themed, mapping to conceptual operations):
//
//	scry       explore traces (CLI / live tail)        — implemented
//	ui         open the embedded observability UI       — implemented
//	mcp        serve builtins / inspect an MCP server   — implemented
//	weave      validate or visualize a workflow graph   — implemented
//	trial      run an evaluation suite from YAML         — implemented
//	cast       run an agent from a YAML file             — implemented
//	council    run a multi-agent orchestration from YAML — implemented
//	spellbook  manage versioned prompt templates         — implemented
//	doctor     check the environment for setup problems   — implemented
//
// The verbs serve, recast and forge were removed from the surface —
// ADR-013 records why (serve and forge contradict explicit non-goals;
// recast is subsumed by `scry replay`).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
)

// helpRequested reports whether err is flag.ErrHelp: the user passed
// -h/--help and the flag package already printed the flag listing, so the
// command exits 0 — asking for help is not a usage error.
func helpRequested(err error) bool { return errors.Is(err, flag.ErrHelp) }

// version is the fallback when nothing better is available. It may be
// overridden at build time via `-ldflags "-X main.version=vX.Y.Z"`, but
// the common path is `go install …/cmd/galdor@vX.Y.Z`, which passes no
// ldflags — there, resolveVersion reads the version Go embeds in the
// binary's build info instead.
var version = "0.0.0-dev"

// resolvedVersion is what the CLI reports (the `version` command, the MCP
// server/client info). Computed once at startup.
var resolvedVersion = resolveVersion()

// resolveVersion picks the most accurate version available:
//  1. an explicit -ldflags injection, if present;
//  2. otherwise the module version Go embeds in the binary — "vX.Y.Z" for
//     `go install …@vX.Y.Z`;
//  3. for a local build from the repo (Main.Version == "(devel)"), the
//     short VCS revision, so dev binaries are still identifiable;
//  4. failing all that, the plain dev fallback.
func resolveVersion() string {
	if version != "0.0.0-dev" {
		return version // ldflags injection wins.
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return "0.0.0-dev+" + s.Value[:7]
		}
	}
	return version
}

func main() {
	if len(os.Args) < 2 {
		usage(os.Stdout)
		return
	}

	switch os.Args[1] {
	case "version", "--version", "-v":
		_, _ = fmt.Fprintf(os.Stdout, "galdor %s\n", resolvedVersion)
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
	case "trial":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := trial(ctx, os.Args[2:], os.Stdout, os.Stderr)
		stop()
		os.Exit(code)
	case "cast":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := cast(ctx, os.Args[2:], os.Stdout, os.Stderr)
		stop()
		os.Exit(code)
	case "council":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := councilCmd(ctx, os.Args[2:], os.Stdout, os.Stderr)
		stop()
		os.Exit(code)
	case "spellbook":
		os.Exit(spellbookCmd(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
	case "doctor":
		os.Exit(doctor(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
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
  trial      Run an evaluation suite from a YAML file
  cast       Run an agent from a YAML file
  council    Run a multi-agent orchestration from a YAML file
  spellbook  Manage versioned prompt templates
  doctor     Check your environment for common setup problems
  version    Print version information
  help       Show this help
`)
}
