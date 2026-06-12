package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/YasserCR/galdor/internal/store"
)

// scry is the entry point for the `galdor scry` verb. It parses the
// sub-command and dispatches to listRuns / showRun. Output goes to
// w; the underlying *store.Store is opened against the database
// resolved from --db / GALDOR_DB / the default location.
func scry(ctx context.Context, args []string, w io.Writer, errW io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(errW, scryUsage)
		return 64
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return scryList(ctx, rest, w, errW)
	case "show":
		return scryShow(ctx, rest, w, errW)
	case "stats":
		return scryStats(ctx, rest, w, errW)
	case "tail":
		return scryTail(ctx, rest, w, errW)
	case "replay":
		return scryReplay(ctx, rest, w, errW)
	case "-h", "--help", "help":
		_, _ = fmt.Fprintln(w, scryUsage)
		return 0
	default:
		_, _ = fmt.Fprintf(errW, "galdor scry: unknown subcommand %q\n\n%s\n", sub, scryUsage)
		return 64
	}
}

const scryUsage = `galdor scry — explore stored traces.

Usage:
  galdor scry list  [--db PATH] [--limit N] [--format text|json]
  galdor scry show  <run-id> [--db PATH] [--format tree|json]
  galdor scry stats [--db PATH] [--by overall|provider|model] [--format text|json]
  galdor scry tail   [--db PATH] [--interval DURATION] [--format text|json]
  galdor scry replay <run-id> [--db PATH] [-o FILE] [--note TEXT]

  --db       Path to the SQLite span store. Falls back to $GALDOR_DB
             and then to ~/.galdor/traces.db.
  --limit    Maximum number of runs to list (default 20).
  --format   Output format. See per-command list above.
  --by       Stats grouping: overall, provider, or model.
  --interval Tail poll interval (default 1s).
  -o FILE    Write the recording to a JSON fixture file.
  --note     Free-form note saved on the fixture.`

// scryList implements `galdor scry list`.
func scryList(ctx context.Context, args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("scry list", flag.ContinueOnError)
	fs.SetOutput(errW)
	db := fs.String("db", "", "path to the span store")
	limit := fs.Int("limit", 20, "maximum number of runs to return")
	format := fs.String("format", "text", "text or json")
	if err := fs.Parse(args); err != nil {
		return 64
	}

	path, err := resolveDBPath(*db)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "scry: %v\n", err)
		return 70
	}
	s, err := store.OpenExisting(ctx, path)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "scry: open %s: %v\n", path, err)
		return 70
	}
	defer func() { _ = s.Close() }()

	runs, err := s.ListRuns(ctx, *limit)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "scry: list runs: %v\n", err)
		return 70
	}

	switch *format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return encodeJSON(errW, enc, runs)
	default:
		renderRunsTable(w, runs)
		return 0
	}
}

// scryShow implements `galdor scry show <run-id>`.
func scryShow(ctx context.Context, args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("scry show", flag.ContinueOnError)
	fs.SetOutput(errW)
	db := fs.String("db", "", "path to the span store")
	format := fs.String("format", "tree", "tree or json")
	runID, err := parseRunIDArg(fs, args)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "scry: %v\n", err)
		return 64
	}

	path, err := resolveDBPath(*db)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "scry: %v\n", err)
		return 70
	}
	s, err := store.OpenExisting(ctx, path)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "scry: open %s: %v\n", path, err)
		return 70
	}
	defer func() { _ = s.Close() }()

	spans, err := s.SpansForRun(ctx, runID)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "scry: %v\n", err)
		return 70
	}
	if len(spans) == 0 {
		_, _ = fmt.Fprintf(errW, "scry: no spans for run %q\n", runID)
		return 1
	}

	switch *format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return encodeJSON(errW, enc, spans)
	default:
		renderSpanTree(w, runID, spans)
		return 0
	}
}

// parseRunIDArg parses a subcommand that takes exactly one <run-id>
// positional plus flags. stdlib flag stops parsing at the first non-flag
// token, so flags placed AFTER the run-id — the shape the usage strings
// document, e.g. `scry show <run-id> --db PATH` — would otherwise be
// silently dropped, and the command would quietly read the WRONG
// database. We pull out the run-id, then re-parse the remainder so flags
// on either side of it are honored.
func parseRunIDArg(fs *flag.FlagSet, args []string) (string, error) {
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return "", fmt.Errorf("missing <run-id> argument")
	}
	runID := rest[0]
	if len(rest) > 1 {
		if err := fs.Parse(rest[1:]); err != nil {
			return "", err
		}
		if extra := fs.Args(); len(extra) > 0 {
			return "", fmt.Errorf("unexpected extra arguments: %v", extra)
		}
	}
	return runID, nil
}

// openLiveStore opens the span store for a live-watch command (ui, tail).
// Unlike the one-shot inspect commands (list/show/stats/replay), which use
// store.OpenExisting and fail on a missing DB, a live watcher may
// legitimately start before the writing process has created the database —
// so a missing file is created (and its parent dir, for the default
// ~/.galdor path) rather than treated as an error. A one-line notice on
// errW keeps a mistyped --db visible instead of silently watching an empty
// new store.
func openLiveStore(ctx context.Context, path string, errW io.Writer) (*store.Store, error) {
	if path != "" && !strings.HasPrefix(path, ":") {
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			if dir := filepath.Dir(path); dir != "" && dir != "." {
				_ = os.MkdirAll(dir, 0o700)
			}
			_, _ = fmt.Fprintf(errW, "galdor: %s does not exist yet — creating it and watching for spans\n", path)
		}
	}
	return store.Open(ctx, path)
}

// resolveDBPath picks the database path: explicit flag, then env
// var, then the canonical default ~/.galdor/traces.db.
func resolveDBPath(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if env := os.Getenv("GALDOR_DB"); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".galdor", "traces.db"), nil
}

// renderRunsTable writes a fixed-width table of runs to w.
func renderRunsTable(w io.Writer, runs []store.RunSummary) {
	if len(runs) == 0 {
		_, _ = fmt.Fprintln(w, "(no runs recorded)")
		return
	}
	_, _ = fmt.Fprintf(w, "%-32s  %-8s  %10s  %6s  %6s  %s\n",
		"RUN ID", "STATUS", "DURATION", "SPANS", "ERRORS", "STARTED")
	for _, r := range runs {
		_, _ = fmt.Fprintf(w, "%-32s  %-8s  %10s  %6d  %6d  %s\n",
			truncate(r.RunID, 32),
			r.Status(),
			formatDuration(r.Duration()),
			r.SpanCount,
			r.ErrorCount,
			time.Unix(0, r.StartTimeUnixNano).Format(time.RFC3339),
		)
	}
}

// renderSpanTree walks the parent/child relationships in spans and
// prints an indented tree to w. Spans without a parent in the
// returned set are treated as roots.
func renderSpanTree(w io.Writer, runID string, spans []store.Span) {
	byID := make(map[string]store.Span, len(spans))
	children := make(map[string][]string)
	var roots []string

	for _, sp := range spans {
		byID[sp.SpanID] = sp
	}
	for _, sp := range spans {
		if sp.ParentSpanID != "" && byID[sp.ParentSpanID].SpanID != "" {
			children[sp.ParentSpanID] = append(children[sp.ParentSpanID], sp.SpanID)
		} else {
			roots = append(roots, sp.SpanID)
		}
	}
	for _, ids := range children {
		sort.Slice(ids, func(i, j int) bool {
			return byID[ids[i]].StartTimeUnixNano < byID[ids[j]].StartTimeUnixNano
		})
	}
	sort.Slice(roots, func(i, j int) bool {
		return byID[roots[i]].StartTimeUnixNano < byID[roots[j]].StartTimeUnixNano
	})

	_, _ = fmt.Fprintf(w, "run %s — %d spans\n", runID, len(spans))
	for _, root := range roots {
		renderSpan(w, byID, children, root, "", true)
	}
}

func renderSpan(w io.Writer, byID map[string]store.Span, children map[string][]string, id, prefix string, last bool) {
	sp := byID[id]
	connector := "├── "
	childPrefix := prefix + "│   "
	if last {
		connector = "└── "
		childPrefix = prefix + "    "
	}
	status := sp.StatusCode
	if status == "unset" {
		status = "·"
	}
	_, _ = fmt.Fprintf(w, "%s%s%s  %s  %s%s\n",
		prefix, connector, sp.Name,
		formatDuration(sp.Duration()),
		status,
		formatExtras(sp),
	)
	kids := children[id]
	for i, k := range kids {
		renderSpan(w, byID, children, k, childPrefix, i == len(kids)-1)
	}
}

// formatExtras returns a compact " (k=v k=v)" suffix for the most
// useful attributes — node name, provider, token usage, tool name.
func formatExtras(sp store.Span) string {
	var parts []string
	if v, ok := sp.Attributes["galdor.span.label"].(string); ok && v != "" {
		parts = append(parts, "label="+v)
	}
	if v, ok := sp.Attributes["galdor.node.name"].(string); ok && v != "" {
		parts = append(parts, "node="+v)
	}
	if v, ok := sp.Attributes["galdor.provider.name"].(string); ok && v != "" {
		parts = append(parts, "provider="+v)
	}
	if v, ok := sp.Attributes["gen_ai.tool.name"].(string); ok && v != "" {
		parts = append(parts, "tool="+v)
	}
	if v, ok := sp.Attributes["gen_ai.usage.input_tokens"].(float64); ok && v > 0 {
		parts = append(parts, fmt.Sprintf("in=%d", int(v)))
	}
	if v, ok := sp.Attributes["gen_ai.usage.output_tokens"].(float64); ok && v > 0 {
		parts = append(parts, fmt.Sprintf("out=%d", int(v)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  (" + strings.Join(parts, " ") + ")"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func formatDuration(nanos int64) string {
	if nanos <= 0 {
		return "—"
	}
	d := time.Duration(nanos)
	switch {
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", nanos)
	case d < time.Millisecond:
		return fmt.Sprintf("%.1fµs", float64(d)/float64(time.Microsecond))
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	default:
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
}

func encodeJSON(errW io.Writer, enc *json.Encoder, v any) int {
	return encodeJSONAs("scry", errW, enc, v)
}

// encodeJSONAs is encodeJSON with the verb name used in the error prefix,
// so a failure inside `mcp ls --json` says "mcp", not "scry".
func encodeJSONAs(verb string, errW io.Writer, enc *json.Encoder, v any) int {
	if err := enc.Encode(v); err != nil {
		_, _ = fmt.Fprintf(errW, "%s: encode json: %v\n", verb, err)
		return 70
	}
	return 0
}
