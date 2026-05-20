package observability

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

// The dashboard, scry CLI and SQLiteExporter all key on the
// `galdor.run.id` attribute to group spans into runs. Setting that
// attribute is the *caller's* responsibility, but in practice the
// vast majority of users want it derived automatically:
//
//   - graph.RunOptions[S].RunID + TraceHooks already covers the
//     `r.InvokeWith(...)` path: BeforeRun stamps galdor.run.id on
//     the root span and calls WithRunID so descendant spans pick
//     it up.
//
//   - Code paths that invoke a provider or tool directly (a
//     standalone Generate call, a queue-worker handler, an A2A
//     task handler) used to fall through with an empty run.id —
//     the dashboard then silently hides their data. Two affordances
//     are provided:
//
//     1. WithRunID(ctx, "...") lets the caller set the run id
//        explicitly when they have a meaningful id to use (a
//        BullMQ job id, an A2A task id, a request id).
//
//     2. When the caller doesn't set one, InstrumentProvider /
//        InstrumentTool fall back to the active trace id (taken
//        from the current span in ctx). That guarantees every
//        span produced under instrumentation lands in *some* run
//        bucket the UI can render, instead of vanishing into the
//        no-run-id void.
//
// Retro feedback #4.

// runIDCtxKey is the context-key type. Unexported so callers must
// go through WithRunID / RunIDFromContext.
type runIDCtxKey struct{}

// WithRunID returns a ctx carrying runID. InstrumentProvider and
// InstrumentTool stamp this value onto each span as the
// `galdor.run.id` attribute, which is what the dashboard, scry CLI
// and SQLiteExporter promote into their run grouping.
//
// Empty runID returns ctx unchanged.
func WithRunID(ctx context.Context, runID string) context.Context {
	if runID == "" {
		return ctx
	}
	return context.WithValue(ctx, runIDCtxKey{}, runID)
}

// RunIDFromContext returns the run id carried by ctx, or "" if none
// is set. When empty, instrumentation falls back to the active
// trace id (see resolveRunID).
func RunIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(runIDCtxKey{}).(string)
	return v
}

// resolveRunID returns the run id instrumentation should stamp on a
// span. Precedence:
//
//  1. RunIDFromContext(ctx) — explicit caller-supplied id wins.
//  2. The trace id of the active span (if any). This guarantees
//     spans produced under a tracer always land in some run bucket
//     the dashboard can render.
//  3. "" — instrumentation skips the attribute and the SpanProcessor
//     in convertSpan will store an empty run_id.
func resolveRunID(ctx context.Context) string {
	if id := RunIDFromContext(ctx); id != "" {
		return id
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		return sc.TraceID().String()
	}
	return ""
}
