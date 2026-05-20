# observability

`pkg/observability` is galdor's instrumentation layer. It produces OpenTelemetry spans for every LLM call, tool invocation, and graph node hop, using the GenAI semantic conventions (`gen_ai.*`) where they apply and a small `galdor.*` namespace for framework-specific dimensions (run id, node name, step counter). The package depends only on the OTel API — export is the caller's choice. galdor ships its own `SQLiteExporter` that feeds the embedded dashboard, but spans drop into Datadog / Honeycomb / Grafana / Tempo just as cleanly.

## Three entry points

```go
func InstrumentProvider(p provider.Provider, tracer trace.Tracer, opts ...InstrumentOption) provider.Provider
func InstrumentTool(t tool.AnyTool, tracer trace.Tracer) tool.AnyTool
func InstrumentRegistry(r *tool.Registry, tracer trace.Tracer) (*tool.Registry, error)
func TraceHooks[S any](tracer trace.Tracer) graph.Hooks[S]

func WithCaptureContent(enabled bool) InstrumentOption

func WithRunID(ctx context.Context, runID string) context.Context
func RunIDFromContext(ctx context.Context) string
```

`InstrumentProvider` wraps `Generate` and `Stream`. `InstrumentTool` / `InstrumentRegistry` wrap `ExecuteJSON`. `TraceHooks[S]` returns hooks you drop into `graph.RunOptions[S].Hooks` — one root span per run, one child span per node hop. Provider and tool spans nest under the active node span automatically because hooks plumb the span into `ctx`.

## The `galdor.run.id` contract

The dashboard, `scry` CLI and SQLite store all key on the `galdor.run.id` span attribute to group spans into runs. galdor stamps it for you in three places, in order of precedence:

1. **`WithRunID(ctx, runID)`** — explicit caller-supplied id. Use this when you have a meaningful external id (a BullMQ job id, an A2A task id, a request id).
2. **`TraceHooks[S]` via `graph.RunOptions[S].RunID`** — set automatically on the run / node spans. `BeforeRun` also calls `WithRunID` so any provider or tool span nested inside picks up the same id.
3. **Trace-id fallback** — if neither of the above is set, `InstrumentProvider` and `InstrumentTool` stamp the active trace id. That guarantees instrumented code always lands in *some* run bucket; raw spans produced outside any galdor instrumentation are the only way to end up with an empty run id, and the dashboard banners those as "orphan spans" so the silent-fail mode of older versions is no longer possible.

## Wiring it up

```go
import (
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    "github.com/YasserCR/galdor/pkg/observability"
)

exporter, _ := observability.NewSQLiteExporter("./traces.db")
tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
defer tp.Shutdown(ctx)
tracer := tp.Tracer("my-agent")

p = observability.InstrumentProvider(p, tracer,
    observability.WithCaptureContent(true))
reg, _ = observability.InstrumentRegistry(reg, tracer)

final, err := r.InvokeWith(ctx, state, graph.RunOptions[State]{
    RunID: runID,
    Hooks: observability.TraceHooks[State](tracer),
})
```

`WithCaptureContent(true)` records the request messages as `gen_ai.prompt` and the response as `gen_ai.completion`, both JSON-encoded. **Off by default** — prompts routinely contain PII, customer data, or proprietary instructions. Turn it on for local debugging, eval runs, or when you intend to drive [replay](replay.md) later.

## Attribute conventions

The keys are constants in `attrs.go`:

```
gen_ai.system                  "anthropic", "openai", "google", ...
gen_ai.request.model           model ID the request asked for
gen_ai.response.model          model that actually served
gen_ai.response.finish_reasons stop reason
gen_ai.usage.input_tokens      int
gen_ai.usage.output_tokens     int
gen_ai.tool.name               tool invoked
gen_ai.tool.input_size_bytes   int
gen_ai.tool.output_size_bytes  int
gen_ai.prompt                  JSON []schema.Message (capture-content only)
gen_ai.completion              JSON schema.Message  (capture-content only)

galdor.run.id                  user-supplied run ID
galdor.node.name               graph node name
galdor.step                    step counter inside the run
galdor.provider.name           same as gen_ai.system, for filtering
galdor.provider.streaming      bool, true for Stream() calls
galdor.state.type              Go type name of the graph state
```

Span names: `galdor.provider.generate`, `galdor.provider.stream`, `galdor.tool.execute`, `galdor.graph.run`, `galdor.graph.node`.

## The embedded SQLite store

`NewSQLiteExporter(path)` opens (or creates) a galdor-managed SQLite database via `internal/store` and registers as an `sdktrace.SpanExporter`. The schema is intentionally small — one denormalized spans table with run id and JSON-encoded attribute blobs — because everything richer is a SQL query, not a schema problem. Users don't touch `internal/store` directly; the `galdor scry` CLI and the embedded dashboard both read from it. `Exporter.Store()` exposes the underlying store if you need to query it from your own code.

The store opens SQLite in `journal_mode=WAL` for write concurrency. The exporter runs `PRAGMA wal_checkpoint(PASSIVE)` every 3 s in a background goroutine so the `.db-wal` sidecar folds back into the main file without waiting for the autocheckpoint threshold. `Shutdown` runs a final `wal_checkpoint(TRUNCATE)` so deploy artifacts ship a 0-byte WAL. Pass `WithCheckpointInterval(d)` to tune the cadence, `WithCheckpointInterval(0)` to disable when you run an external checkpointer.

## The `galdor scry` CLI

`scry` (Old English: *to perceive*) is the introspection family. Every command honors `--db`, `$GALDOR_DB`, and `~/.galdor/traces.db` in that order.

```
galdor scry list   [--db PATH] [--limit N] [--format text|json]
galdor scry show   <run-id> [--db PATH] [--format tree|json]
galdor scry stats  [--db PATH] [--by overall|provider|model] [--format text|json]
galdor scry tail   [--db PATH] [--interval DURATION] [--format text|json]
galdor scry replay <run-id> [--db PATH] [-o FILE] [--note TEXT]
```

`list` prints a runs table (id, status, duration, span count, errors). `show` renders the per-run span tree with node names, provider, token usage, and tool names extracted from attributes. `stats` aggregates token counts and durations. `tail` polls for new runs. `replay` is covered in [replay](replay.md).

## The dashboard

`galdor ui --db ./traces.db` starts the embedded server. The default bind is `127.0.0.1:7777` — use `--addr 0.0.0.0:7777` to opt into LAN exposure deliberately. The handler lives in `internal/ui` and serves:

```
GET  /                           run list
GET  /runs/{runID}               span tree
GET  /runs/{runID}/steps         step-by-step time-travel view
GET  /runs/{runID}/spans/{id}    single span detail
GET  /api/runs                   JSON run list
GET  /api/runs/{runID}/spans     JSON span list
GET  /api/stream/runs            SSE live feed of new runs
GET  /graph                      graph viewer page
POST /api/graph/svg              render a graph.Graph as SVG
```

Templates and CSS are compiled into the binary via `embed.FS`, so the framework stays a single artefact with the UI enabled. The SSE endpoint pushes one event per new run as it lands in the store — open `/` in two tabs and run an example; the second tab updates without reload.

## Gotchas

- `WithCaptureContent` is the gate for replay. If you record a run without it, `galdor scry replay <run-id>` returns `replay.ErrNoContent` and the user has to re-run.
- Stream spans end when the consumer drains the stream or calls `Close()` — not when `Stream()` returns. Token usage is recorded from the terminal `EventMessageStop`.
- `InstrumentTool` keeps the original `Name`/`Description`/`Schema` so registries can hold wrapped or unwrapped tools interchangeably.
- `TraceHooks[S]` uses a fresh ctx-key per invocation. Composing multiple hook sets (e.g., logging hooks and trace hooks) is safe — they don't collide.
- The SQLite exporter caps the connection pool to 1 writer; concurrent `ExportSpans` calls serialize but never `SQLITE_BUSY`.

## See also

- [provider](provider.md), [tool](tool.md), [graph](graph.md) — the surfaces being instrumented.
- [replay](replay.md) — consumes captured prompt/completion spans.
- [`ADR-008`](../adr/ADR-008-observability-instrumentation.md), [`ADR-009`](../adr/ADR-009-sqlite-span-store-and-scry-cli.md), [`ADR-010`](../adr/ADR-010-web-ui-architecture.md).
- [`examples/observability-trace`](../../examples/observability-trace/), [`examples/scry-store`](../../examples/scry-store/).
