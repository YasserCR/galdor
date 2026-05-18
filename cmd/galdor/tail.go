package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/YasserCR/galdor/internal/store"
)

// scryTail implements `galdor scry tail`. It polls the spans table
// at --interval and prints new arrivals as they show up. Exits
// cleanly on ctx cancellation (the binary wires SIGINT into ctx).
func scryTail(ctx context.Context, args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("scry tail", flag.ContinueOnError)
	fs.SetOutput(errW)
	db := fs.String("db", "", "path to the span store")
	interval := fs.Duration("interval", 1*time.Second, "poll interval")
	format := fs.String("format", "text", "text or json")
	maxIter := fs.Int("_max-iterations", 0, "internal: cap on number of poll iterations (0 = unbounded; used by tests)")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	if *interval < 10*time.Millisecond {
		_, _ = fmt.Fprintf(errW, "scry tail: interval too small (%s); minimum 10ms\n", *interval)
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

	cursor, err := s.MaxSpanStart(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "scry tail: %v\n", err)
		return 70
	}

	useJSON := *format == "json"
	if !useJSON {
		_, _ = fmt.Fprintf(w, "tailing %s (interval %s). Ctrl-C to stop.\n", path, *interval)
	}

	tick := time.NewTicker(*interval)
	defer tick.Stop()
	enc := json.NewEncoder(w)

	for iter := 0; ; iter++ {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return 0
			}
			return 0
		case <-tick.C:
		}

		newSpans, err := s.SpansSince(ctx, cursor, 200)
		if err != nil {
			// Don't bail on transient query errors — print and keep
			// polling. The user gets a stream of warnings if the DB
			// is gone, which is probably what they want.
			_, _ = fmt.Fprintf(errW, "scry tail: %v\n", err)
			continue
		}
		for _, sp := range newSpans {
			if sp.StartTimeUnixNano > cursor {
				cursor = sp.StartTimeUnixNano
			}
			if useJSON {
				if err := enc.Encode(sp); err != nil {
					_, _ = fmt.Fprintf(errW, "scry tail: encode: %v\n", err)
				}
			} else {
				printTailSpan(w, sp)
			}
		}

		if *maxIter > 0 && iter+1 >= *maxIter {
			return 0
		}
	}
}

// printTailSpan emits a one-line summary suitable for terminal
// tailing. Tightly matches the format used by `scry show` so the
// vocabulary stays consistent.
func printTailSpan(w io.Writer, sp store.Span) {
	status := sp.StatusCode
	if status == "unset" {
		status = "·"
	}
	when := time.Unix(0, sp.StartTimeUnixNano).Format("15:04:05.000")
	run := sp.RunID
	if run == "" {
		run = "-"
	}
	_, _ = fmt.Fprintf(w, "%s  run=%-20s  %-32s  %s  %s%s\n",
		when, truncate(run, 20),
		truncate(sp.Name, 32),
		formatDuration(sp.Duration()),
		status,
		formatExtras(sp),
	)
}
