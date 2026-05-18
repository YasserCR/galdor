package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/YasserCR/galdor/internal/store"
)

// scryStats implements `galdor scry stats`.
func scryStats(ctx context.Context, args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("scry stats", flag.ContinueOnError)
	fs.SetOutput(errW)
	db := fs.String("db", "", "path to the span store")
	by := fs.String("by", "overall", "grouping: overall, provider, or model")
	format := fs.String("format", "text", "text or json")
	if err := fs.Parse(args); err != nil {
		return 64
	}

	path, err := resolveDBPath(*db)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "scry: %v\n", err)
		return 70
	}
	s, err := store.Open(ctx, path)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "scry: open %s: %v\n", path, err)
		return 70
	}
	defer func() { _ = s.Close() }()

	var rows []store.Stats
	switch *by {
	case "overall":
		single, sErr := s.OverallStats(ctx)
		if sErr != nil {
			_, _ = fmt.Fprintf(errW, "scry: %v\n", sErr)
			return 70
		}
		rows = []store.Stats{single}
	case "provider":
		rows, err = s.StatsByProvider(ctx)
		if err != nil {
			_, _ = fmt.Fprintf(errW, "scry: %v\n", err)
			return 70
		}
	case "model":
		rows, err = s.StatsByModel(ctx)
		if err != nil {
			_, _ = fmt.Fprintf(errW, "scry: %v\n", err)
			return 70
		}
	default:
		_, _ = fmt.Fprintf(errW, "scry: unknown --by value %q (want overall|provider|model)\n", *by)
		return 64
	}

	switch *format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return encodeJSON(errW, enc, rows)
	default:
		renderStatsTable(w, *by, rows)
		return 0
	}
}

// renderStatsTable writes a compact table with one row per grouping
// key. The "overall" path uses a single row labeled "ALL".
func renderStatsTable(w io.Writer, by string, rows []store.Stats) {
	header := "GROUP"
	switch by {
	case "provider":
		header = "PROVIDER"
	case "model":
		header = "MODEL"
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintf(w, "(no spans recorded — run an agent with the SQLite exporter first)\n")
		return
	}
	_, _ = fmt.Fprintf(w, "%-28s  %6s  %6s  %10s  %10s  %10s  %8s  %8s\n",
		header, "SPANS", "ERRS", "P50", "P95", "P99", "TOK IN", "TOK OUT")
	for _, r := range rows {
		key := r.Key
		if key == "" {
			key = "ALL"
		}
		_, _ = fmt.Fprintf(w, "%-28s  %6d  %6d  %10s  %10s  %10s  %8d  %8d\n",
			truncate(key, 28),
			r.SpanCount,
			r.ErrorCount,
			formatDuration(r.LatencyP50Ns),
			formatDuration(r.LatencyP95Ns),
			formatDuration(r.LatencyP99Ns),
			r.InputTokens,
			r.OutputTokens,
		)
	}
}
