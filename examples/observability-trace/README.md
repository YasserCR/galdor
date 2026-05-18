# examples/observability-trace

Runs a tiny ReAct agent with full OpenTelemetry instrumentation —
provider calls, tool executions and graph node hops all emit spans
— and pipes the spans to stdout in pretty JSON. No network, no API
key.

## Run

```bash
go run ./examples/observability-trace
```

You'll see two messages on stderr (the agent's progress) and a
sequence of JSON span objects on stdout, one per nested operation:

```
galdor.graph.run                 (root)
├── galdor.graph.node            (node: model)
│   └── galdor.provider.generate (LLM call)
├── galdor.graph.node            (node: tools)
│   └── galdor.tool.execute      (math)
└── galdor.graph.node            (node: model)
    └── galdor.provider.generate (final LLM call)
```

Span attributes follow the [OpenTelemetry GenAI semantic
conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/):
`gen_ai.system`, `gen_ai.request.model`, `gen_ai.response.model`,
`gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`,
`gen_ai.response.finish_reasons`, plus galdor-specific keys
(`galdor.run.id`, `galdor.node.name`, `galdor.step`, etc.) listed
in `pkg/observability/attrs.go`.

## What it shows

- **`observability.InstrumentProvider(p, tracer)`** returns a
  provider.Provider that's drop-in compatible with the rest of
  galdor and emits a `galdor.provider.generate` (or
  `galdor.provider.stream`) span per call.
- **`observability.InstrumentRegistry(reg, tracer)`** wraps every
  tool in a registry so each tool execution emits a
  `galdor.tool.execute` span.
- **`observability.TraceHooks[State](tracer)`** returns
  `graph.Hooks[State]` you drop straight into
  `graph.RunOptions.Hooks`. The hooks open a root span on
  `BeforeRun` and one child span per node on `BeforeNode`.
- Spans nest correctly: an instrumented provider call made from
  inside a node body ends up as a child of that node's span,
  because `BeforeNode` returns a ctx carrying the active node
  span.

## Swap in a real exporter

The example pipes to stdout because it has no extra dependencies.
For production, replace the exporter:

```go
import (
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

exp, _ := otlptracegrpc.New(ctx)
tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))
```

The rest of the program is unchanged — galdor only depends on the
OTel API, not on any specific exporter.

## Once Phase 4 session B lands

A galdor-embedded SQLite exporter will let you skip the external
backend entirely for local development. The CLI `galdor scry` will
read from that same store. The instrumentation layer in this
example is the producer side of that flow; the storage backend is
the consumer side.
