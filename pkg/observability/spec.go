package observability

import (
	"context"
	"encoding/json"

	"github.com/YasserCR/galdor/pkg/graph"
)

// RecordGraphSpec returns hooks that persist a Runnable's topology
// (the graph.Spec) into the exporter's store on every BeforeRun.
// The dashboard reads that record to render the per-run DAG on the
// run-detail page; without it the /graph viewer would only know
// about graphs you manually paste in.
//
// Compose with TraceHooks via graph.MergeHooks when you want both
// span emission and spec recording:
//
//	hooks := graph.MergeHooks(
//	    observability.TraceHooks[State](tracer),
//	    observability.RecordGraphSpec[State](exporter, r),
//	)
//
// Spec serialization happens once at call time (the topology of a
// compiled Runnable does not change). BeforeRun only writes the
// pre-serialized blob.
//
// Writing the spec is best-effort: a store error is dropped silently
// because failing the run because the per-run DAG could not be
// recorded would be worse than the run completing without it.
func RecordGraphSpec[S any](exporter *SQLiteExporter, r *graph.Runnable[S]) graph.Hooks[S] {
	if exporter == nil {
		panic("observability: nil exporter")
	}
	if r == nil {
		panic("observability: nil runnable")
	}
	spec := r.Inspect()
	specJSON, err := json.Marshal(spec)
	if err != nil {
		// Spec marshaling should never fail (all fields are
		// trivially marshalable); a panic here would mean a galdor
		// bug, not a caller bug.
		panic("observability: marshal spec: " + err.Error())
	}
	store := exporter.store
	return graph.Hooks[S]{
		BeforeRun: func(ctx context.Context, runID string, _ S) context.Context {
			_ = store.SetGraphSpec(ctx, runID, specJSON)
			return ctx
		},
	}
}
