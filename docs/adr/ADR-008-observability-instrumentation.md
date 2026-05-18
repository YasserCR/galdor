# ADR-008 — Observability instrumentation (Phase 4 session A)

- **Status:** Accepted
- **Date:** 2026-05-18
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

The plan's elevator pitch (PLAN §1 / §6.2.6) is "Go framework with
native observability". ADR-001 §D6 locked OpenTelemetry in as a
core dependency, intentionally a one-way door. Phase 4 session A is
the first time we actually wire OTel through the lower-level
primitives.

The work splits cleanly into three layers, and the questions this
ADR settles are the same in each: what triggers a span, where do
the attributes come from, and how do callers opt in?

## Decisions

### D1. Instrumentation is opt-in wrapping, not a fork in the lower-level packages

Three exported helpers cover the surface:

```go
observability.InstrumentProvider(p, tracer) provider.Provider
observability.InstrumentTool(t, tracer)     tool.AnyTool
observability.InstrumentRegistry(r, tracer) (*tool.Registry, error)
observability.TraceHooks[S](tracer)         graph.Hooks[S]
```

Each one returns a value of the *same* public type the caller
already has. The instrumented provider IS a `provider.Provider`,
the instrumented tool IS a `tool.AnyTool`, the hooks ARE
`graph.Hooks[S]`. Nothing in `pkg/provider`, `pkg/tool`,
`pkg/graph` or `pkg/agent` imports OpenTelemetry — only callers who
opt in pay the dependency.

That said, the *API* of `pkg/graph` had to grow a thin extension
point: `graph.Hooks[S]` lives in pkg/graph itself, with no OTel
import. Observability supplies an instance of `Hooks[S]`; callers
plug it in via `RunOptions.Hooks`. This keeps the OTel weight in
pkg/observability while letting any consumer (logger, metric
reporter, custom audit trail) reuse the same hook surface.

### D2. Span layout mirrors the call graph; attributes follow the OTel GenAI conventions

Every run produces a single root span (`galdor.graph.run`), one
child span per node hop (`galdor.graph.node`), and — when the node
calls into an instrumented provider or tool — additional grandchild
spans (`galdor.provider.{generate,stream}`,
`galdor.tool.execute`).

Attribute keys follow the OpenTelemetry GenAI semantic conventions
verbatim where one exists (`gen_ai.system`,
`gen_ai.request.model`, `gen_ai.response.model`,
`gen_ai.response.finish_reasons`, `gen_ai.usage.input_tokens`,
`gen_ai.usage.output_tokens`, `gen_ai.tool.name`,
`gen_ai.tool.input_size_bytes`, `gen_ai.tool.output_size_bytes`).
galdor-specific dimensions are namespaced under `galdor.*`
(`galdor.run.id`, `galdor.node.name`, `galdor.step`,
`galdor.provider.name`, `galdor.provider.streaming`,
`galdor.state.type`).

Keeping these in a single `pkg/observability/attrs.go` makes the
dashboard (Phase 5) and external trace pipelines (Phase 4 session
B's SQLite store, OTLP exporters, Datadog / Jaeger / Tempo) all
recognize galdor spans without ad-hoc matching.

### D3. Streaming spans end at terminal events, not at Stream() return

A streaming provider call returns to the caller as soon as the
HTTP request is opened; the actual response arrives event by
event. The instrumented wrapper keeps the provider span open until
either `EventMessageStop` is observed, `io.EOF` is returned by
`Recv`, or the caller calls `Close()`. Final usage and stop reason
are attached at that point.

This is the only place in the package where a span's lifetime is
longer than the function call that started it. It is necessary
because tying the span to `Stream()`'s return would record zero
output tokens and an empty stop reason every time — useless.

### D4. Hooks expose context handoff, not data payloads

`graph.Hooks[S]` deliberately accepts the same state value the
runtime is passing around, not a serialized form. Hooks are
expected to summarize: span attributes carry token counts and node
names, not the entire S. Callers who want the full payload on the
span can read it from the State they're already given and add a
custom attribute — but the default surface is small.

`BeforeRun` and `BeforeNode` both return a `context.Context`. The
returned ctx is what the runtime uses for the wrapped scope
(BeforeRun → the rest of the loop; BeforeNode → the node call and
AfterNode). This is how the node-level OTel span ends up as the
parent of any provider / tool span dispatched from inside the
node. Without it, those spans would be siblings instead of
children, and the trace tree would lie about the call structure.

### D5. Errors are recorded with both RecordError and SetStatus(Error)

OpenTelemetry consumers split between using the span's status code
and reading recorded events. The instrumentation calls both so
either consumption pattern works. `RecordError(err)` captures the
err message and stack-ish hint; `SetStatus(codes.Error,
err.Error())` makes the span show up red in any dashboard that
groups by status.

### D6. The package depends only on the OTel API

`pkg/observability` imports `go.opentelemetry.io/otel/{attribute,
codes, trace}` and nothing else from the OTel stable. The SDK
(`go.opentelemetry.io/otel/sdk/...`), exporters and resource
constructors are imported only by tests (using the in-memory
recorder) and by the example (using `stdouttrace`). Library users
who write their own setup pay only the API weight (~few kB,
zero-allocation in steady state when no tracer is registered).

### D7. No `Instrument(...)` mega-helper; users compose three calls

A "wrap a whole Config in one call" helper was considered and
rejected for the v1. The three concerns — provider, tools, graph
hooks — have distinct surfaces and distinct types, and the
"composed setup" varies from project to project (some users only
want provider tracing, others only graph tracing, plenty want
both). Forcing a single helper would either hide what's happening
or accumulate enough options to be confusing.

The example (`examples/observability-trace`) shows the full setup
in ~10 lines; that's the canonical pattern.

## Consequences

**Positive.** OpenTelemetry instrumentation works against every
existing primitive (provider, tool, graph, agent) without touching
their public APIs. Spans nest correctly through context handoff,
GenAI semantic conventions are honored, and errors show up as red
in any compliant dashboard. Coverage on the new package is 87%.
The example exports spans to stdout with no external dependencies
so anyone can see the shape immediately.

**Negative.** Wrapping introduces an indirection layer. For
hot-path code (a tight tool-call loop with cheap tools), the span
allocation and attribute construction add measurable overhead.
ADR-001 §D6 anticipated this trade-off and accepted it; if a
production workload turns out to be sensitive we can revisit with
a sampling-aware variant. The pattern of wrapping at construction
also means callers who forget to wrap get *no* spans for that
component — there's no fallback to a global tracer. That's a
deliberate "explicit is better than implicit" call.

## Out of scope

- **Embedded SQLite trace store + CLI explorer** (`galdor scry`).
  Phase 4 session B will land both: the storage backend acts as
  an OTel exporter (just another sink), and the CLI reads from
  the same store. The instrumentation surface in this ADR is the
  producer side; the consumer side comes next.
- **Metrics (latency p50/p95/p99, cost).** Spans already carry
  duration; turning them into Prometheus / OTLP metrics is a thin
  layer atop the existing instrumentation and lives in the same
  follow-up session as the storage backend.
- **Sampling policy.** OTel SDK sampling configurations (parent-
  based, ratio-based) work as-is against galdor spans; no
  framework-specific decision is needed.
- **Replay engine (Phase 9).** Replay reads spans + checkpoints
  (ADR-006) and reconstructs runs. Both the span layout and the
  checkpoint contract are stable in this ADR, so Phase 9 can
  build on them without further changes here.

## References

- ADR-001 §D6 — OTel as a core dependency (rationale and
  one-way-door).
- ADR-005 — graph runtime; this ADR adds `Hooks[S]` and threads
  them through `runLoop`.
- ADR-006 — checkpointer; checkpoints and spans are independent
  and parallel — Phase 9 reads both.
- `pkg/observability/attrs.go`, `pkg/observability/provider.go`,
  `pkg/observability/tool.go`, `pkg/observability/graph.go`.
- `examples/observability-trace/` — end-to-end demo, stdouttrace
  exporter.
- OpenTelemetry GenAI semantic conventions:
  https://opentelemetry.io/docs/specs/semconv/gen-ai/
