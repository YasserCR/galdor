package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// PrintSummary writes a human-readable summary of the report to w.
// Use this from a CI script or a `go test` benchmark to get a
// readable verdict without spelunking through JSON.
func (r *Report) PrintSummary(w io.Writer) {
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "Dataset:  %s @ %s\n", r.Dataset, r.Version)
	_, _ = fmt.Fprintf(w, "Cases:    %d  (pass %d, fail %d, error %d)\n",
		len(r.Cases), r.Passed, r.Failed, r.Errored)
	_, _ = fmt.Fprintf(w, "Pass rate: %.1f%%\n", r.PassRate()*100)
	_, _ = fmt.Fprintf(w, "Duration: %s\n", r.Duration)
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Per-scorer aggregates:")
	for _, name := range sortedAggregateNames(r) {
		a := r.Aggregates[name]
		_, _ = fmt.Fprintf(w, "  %-18s mean=%.2f  pass=%d  fail=%d\n",
			a.Scorer, a.Mean, a.Pass, a.Fail)
	}
	_, _ = fmt.Fprintln(w)

	// Failing / errored cases get a one-line preview each.
	if r.Failed+r.Errored > 0 {
		_, _ = fmt.Fprintln(w, "Cases needing attention:")
		for _, c := range r.Cases {
			switch {
			case c.Err != "":
				_, _ = fmt.Fprintf(w, "  [ERR ] %-24s %s\n", c.Case.ID, truncate(c.Err, 80))
			case !c.Pass:
				detail := failingScorers(c)
				_, _ = fmt.Fprintf(w, "  [FAIL] %-24s actual=%q  %s\n",
					c.Case.ID, truncate(c.Actual, 50), detail)
			}
		}
		_, _ = fmt.Fprintln(w)
	}
}

// WriteJSON writes the full report as indented JSON to w.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// RunAndExit is the CI-friendly wrapper. It calls Run, prints the
// summary to stderr, and exits with status 0 when the pass rate
// meets cfg.MinPass (default 1.0) or 1 otherwise. Setup errors (e.g.
// an empty dataset or duplicate scorer names) surface as exit 2. A
// panicking Subject or Scorer is recovered by the runner and recorded
// as an Errored case, so one bad case fails its run rather than
// aborting the whole batch.
func RunAndExit(ctx context.Context, cfg Config) {
	if cfg.MinPass == 0 {
		cfg.MinPass = 1.0
	}
	report, err := Run(ctx, cfg)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "eval: setup error: %v\n", err)
		os.Exit(2)
	}
	report.PrintSummary(os.Stderr)
	if !report.Meets(cfg.MinPass) {
		_, _ = fmt.Fprintf(os.Stderr, "eval: pass rate %.1f%% < threshold %.1f%%\n",
			report.PassRate()*100, cfg.MinPass*100)
		os.Exit(1)
	}
	os.Exit(0)
}

func sortedAggregateNames(r *Report) []string {
	names := make([]string, 0, len(r.Aggregates))
	for n := range r.Aggregates {
		names = append(names, n)
	}
	// Insertion-style stable sort that doesn't pull sort package
	// (this function is hit on every PrintSummary; keep it cheap).
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

func failingScorers(c CaseResult) string {
	var parts []string
	for name, s := range c.Scores {
		if !s.Pass {
			parts = append(parts, fmt.Sprintf("%s(%.2f)", name, s.Value))
		}
	}
	// Stable order so report diffs are clean.
	for i := 1; i < len(parts); i++ {
		for j := i; j > 0 && parts[j] < parts[j-1]; j-- {
			parts[j], parts[j-1] = parts[j-1], parts[j]
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "failed: " + strings.Join(parts, ", ")
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
