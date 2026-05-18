// Package observability is galdor's native instrumentation layer.
// It produces OpenTelemetry spans for every LLM call, tool
// invocation and graph node hop, using the OpenTelemetry GenAI
// semantic conventions where they apply.
//
// Three entry points cover the surface:
//
//   - InstrumentProvider wraps a provider.Provider so Generate and
//     Stream emit spans with model, token counts, stop reason and
//     finish/error status.
//   - InstrumentTool / InstrumentRegistry wraps a tool.AnyTool so
//     ExecuteJSON emits a span carrying the tool name, input size
//     and error (if any).
//   - TraceHooks[S] returns graph.Hooks[S] suitable for
//     graph.RunOptions[S].Hooks — every run is one root span;
//     every node hop is a child span.
//
// The package depends only on the OpenTelemetry API (trace,
// attribute, codes). Span export is the caller's responsibility:
// pick an SDK and an exporter (stdouttrace for local debugging,
// OTLP / Jaeger / Datadog / your-favorite-backend in production),
// or — once Phase 4 session B lands — let galdor's embedded SQLite
// store consume them.
package observability
