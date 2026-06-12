package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/YasserCR/galdor/pkg/replay"
)

// scryReplay implements `galdor scry replay <run-id>`. It loads the
// provider.generate spans for a run, validates that they carry the
// captured prompt + completion bodies, and either prints a summary
// of the recording or exports it to a JSON fixture file.
//
// Replay itself happens client-side: a test or a script reloads the
// fixture (or calls replay.LoadFromStore directly) and plugs the
// resulting replay.Provider into agent.Config. This subcommand is
// the "operator" surface — it tells you whether a given run is
// replayable and saves a portable fixture for later runs.
func scryReplay(ctx context.Context, args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("scry replay", flag.ContinueOnError)
	fs.SetOutput(errW)
	db := fs.String("db", "", "path to the span store")
	out := fs.String("o", "", "write the recording to this JSON fixture file (default: print summary only)")
	note := fs.String("note", "", "free-form note saved on the fixture (e.g., dataset version)")
	runID, err := parseRunIDArg(fs, args)
	if helpRequested(err) {
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintf(errW, "usage: galdor scry replay <run-id> [--db PATH] [-o FILE] [--note TEXT]\n  %v\n", err)
		return 64
	}

	path, err := resolveDBPath(*db)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "scry: %v\n", err)
		return 70
	}

	rec, err := replay.LoadFromStore(ctx, path, runID)
	if err != nil {
		if errors.Is(err, replay.ErrNoContent) {
			_, _ = fmt.Fprintf(errW,
				"scry replay: %v\n\nThis run was recorded without prompt/completion capture, so it cannot be replayed.\nRe-run with observability.WithCaptureContent(true) to enable replay.\n",
				err)
			return 65
		}
		_, _ = fmt.Fprintf(errW, "scry replay: %v\n", err)
		return 70
	}

	if *note != "" {
		rec.Note = *note
	}
	if *out != "" {
		if err := replay.SaveToFile(rec, *out); err != nil {
			_, _ = fmt.Fprintf(errW, "scry replay: write %s: %v\n", *out, err)
			return 70
		}
		_, _ = fmt.Fprintf(w, "Saved %d recorded calls to %s.\n", len(rec.Calls), *out)
	}
	printReplaySummary(w, rec)
	return 0
}

func printReplaySummary(w io.Writer, rec replay.Recording) {
	_, _ = fmt.Fprintf(w, "Recording for run %q\n", rec.RunID)
	_, _ = fmt.Fprintf(w, "  fixture version: %d\n", rec.Version)
	if rec.Note != "" {
		_, _ = fmt.Fprintf(w, "  note: %s\n", rec.Note)
	}
	_, _ = fmt.Fprintf(w, "  calls: %d\n\n", len(rec.Calls))
	for i, c := range rec.Calls {
		var preview string
		if c.Response != nil {
			preview = c.Response.Message.Text()
		}
		// Fingerprint can fail (and yield "") when the matching surface
		// won't JSON-encode; truncate is length-guarded so a short or
		// empty fingerprint never panics on a raw [:12] slice.
		fp, err := c.Fingerprint()
		if err != nil {
			fp = "(unavailable)"
		}
		_, _ = fmt.Fprintf(w, "  %2d. model=%-24s fp=%s\n      reply: %s\n",
			i+1, c.Model, truncate(fp, 12), truncate(preview, 72))
	}
}
