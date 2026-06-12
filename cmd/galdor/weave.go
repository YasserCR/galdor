package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/YasserCR/galdor/internal/store"
	"github.com/YasserCR/galdor/pkg/graph"
)

// weave validates or visualizes the graph topology recorded for a run.
// The topology is captured at run time (the same spec the dashboard
// renders), so `weave <run-id>` works on any traced run without re-running
// the graph.
func weave(ctx context.Context, args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("weave", flag.ContinueOnError)
	fs.SetOutput(errW)
	db := fs.String("db", "", "path to the span store (defaults to $GALDOR_DB or ~/.galdor/traces.db)")
	outFile := fs.String("o", "", "write the SVG to this file instead of stdout")
	check := fs.Bool("check", false, "validate the topology instead of rendering (exit 1 on problems)")
	format := fs.String("format", "svg", "output format: svg or json")
	runID, err := parseRunIDArg(fs, args)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "weave: %v\n\n%s\n", err, weaveUsage)
		return 64
	}

	path, err := resolveDBPath(*db)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "weave: %v\n", err)
		return 70
	}
	s, err := store.OpenExisting(ctx, path)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "weave: open %s: %v\n", path, err)
		return 70
	}
	defer func() { _ = s.Close() }()

	specJSON, err := s.GetGraphSpec(ctx, runID)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "weave: %v\n", err)
		return 70
	}
	if specJSON == "" {
		_, _ = fmt.Fprintf(errW, "weave: no graph topology recorded for run %q\n", runID)
		_, _ = fmt.Fprintln(errW, "  (only runs executed through a graph.Runnable capture a topology)")
		return 1
	}
	var spec graph.Spec
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		_, _ = fmt.Fprintf(errW, "weave: decode topology: %v\n", err)
		return 70
	}

	if *check {
		return weaveCheck(spec, runID, w, errW)
	}

	switch *format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return encodeJSONAs("weave", errW, enc, spec)
	case "svg":
		return weaveRenderSVG(spec, *outFile, w, errW)
	default:
		_, _ = fmt.Fprintf(errW, "weave: unknown format %q (want svg or json)\n", *format)
		return 64
	}
}

const weaveUsage = `galdor weave — validate or visualize a run's graph topology.

Usage:
  galdor weave <run-id> [--db PATH] [-o FILE] [--format svg|json]
  galdor weave <run-id> --check [--db PATH]

  --db      Path to the SQLite span store. Falls back to $GALDOR_DB and
            then to ~/.galdor/traces.db.
  -o FILE   Write the SVG to FILE instead of stdout.
  --format  svg (default) or json (the raw topology).
  --check   Validate edges/entry instead of rendering; exits 1 on problems.`

func weaveRenderSVG(spec graph.Spec, outFile string, w io.Writer, errW io.Writer) int {
	out := w
	if outFile != "" {
		f, err := os.Create(outFile) // #nosec G304 -- path is supplied by the CLI user
		if err != nil {
			_, _ = fmt.Fprintf(errW, "weave: create %s: %v\n", outFile, err)
			return 70
		}
		defer func() { _ = f.Close() }()
		out = f
	}
	if err := spec.RenderSVG(out); err != nil {
		_, _ = fmt.Fprintf(errW, "weave: render: %v\n", err)
		return 70
	}
	if outFile != "" {
		_, _ = fmt.Fprintf(errW, "weave: wrote %s\n", outFile)
	}
	return 0
}

// weaveCheck validates the topology and reports problems. A graph that
// recorded successfully is already well-formed, but a hand-edited or
// externally-produced spec can dangle; this catches edges to unknown
// nodes, a missing/unknown entry, and unreachable nodes.
func weaveCheck(spec graph.Spec, runID string, w io.Writer, errW io.Writer) int {
	known := map[string]bool{graph.START: true, graph.END: true}
	for _, n := range spec.Nodes {
		known[n.Name] = true
	}

	var problems []string
	if spec.Entry == "" {
		problems = append(problems, "entry node is empty")
	} else if !known[spec.Entry] {
		problems = append(problems, fmt.Sprintf("entry node %q is not a registered node", spec.Entry))
	}
	for _, e := range spec.StaticEdges {
		if !known[e.From] {
			problems = append(problems, fmt.Sprintf("static edge from unknown node %q", e.From))
		}
		if e.To != "" && !known[e.To] {
			problems = append(problems, fmt.Sprintf("static edge %q → unknown node %q", e.From, e.To))
		}
	}
	for _, e := range spec.ConditionalEdges {
		if !known[e.From] {
			problems = append(problems, fmt.Sprintf("conditional edge from unknown node %q", e.From))
		}
		for _, b := range e.Branches {
			if !known[b.To] {
				problems = append(problems, fmt.Sprintf("conditional branch %q:%q → unknown node %q", e.From, b.Label, b.To))
			}
		}
	}

	// Reachability: a node with no inbound edge (and that isn't the entry)
	// can never run. Reported as a warning, not a hard failure.
	hasInbound := map[string]bool{}
	for _, e := range spec.StaticEdges {
		if e.To != "" {
			hasInbound[e.To] = true
		}
	}
	for _, e := range spec.ConditionalEdges {
		for _, b := range e.Branches {
			hasInbound[b.To] = true
		}
	}
	var orphans []string
	for _, n := range spec.Nodes {
		if n.Name != spec.Entry && !hasInbound[n.Name] {
			orphans = append(orphans, n.Name)
		}
	}
	sort.Strings(orphans)

	if len(problems) == 0 {
		_, _ = fmt.Fprintf(w, "run %s — topology OK: %d node(s), %d static + %d conditional edge(s)\n",
			runID, len(spec.Nodes), len(spec.StaticEdges), len(spec.ConditionalEdges))
		if len(orphans) > 0 {
			_, _ = fmt.Fprintf(w, "  note: node(s) with no inbound edge (unreachable unless a router targets them dynamically): %s\n", strings.Join(orphans, ", "))
		}
		return 0
	}
	_, _ = fmt.Fprintf(errW, "run %s — %d problem(s):\n", runID, len(problems))
	for _, p := range problems {
		_, _ = fmt.Fprintf(errW, "  ✗ %s\n", p)
	}
	return 1
}
