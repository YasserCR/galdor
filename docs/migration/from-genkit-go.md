# Migrating from Genkit Go

This guide is for users coming from
[Firebase Genkit Go](https://github.com/firebase/genkit/tree/main/go).
The two projects share a Go-native philosophy and an OTel-first
observability story. The main differences are deployment story
(galdor is single-binary self-hostable; Genkit Monitoring is
Google-Cloud-only) and orchestration primitives (galdor has
Supervisor / Swarm built in; Genkit has Flows + tool-calling
agents but no first-class hierarchy).

## Flows → graphs

| In Genkit Go | In galdor |
|---|---|
| `genkit.DefineFlow(name, func(ctx, in) (out, error))` | a single `graph.NodeFunc[State]` |
| Multi-step flow chained via Go control flow | `graph.New[State]().AddNode(...).AddEdge(...)` |
| Conditional branching by `if` inside a flow | `g.AddConditionalEdge(node, router)` |
| `flow.Run(ctx, in)` | `r.Invoke(ctx, state)` |

A Genkit flow is a Go function with a registered name. galdor's
analogue is a graph node — also a Go function — composed into
a directed graph. The reason to compose is that you get
checkpoints, interrupts, hooks, timeouts, and panic recovery as
runtime features rather than ad-hoc per-flow concerns:

```go
g := graph.New[MyState]().
    AddNode("retrieve", retrieve).
    AddNode("answer",   answer).
    AddEdge(graph.START, "retrieve").
    AddEdge("retrieve",  "answer").
    AddEdge("answer",    graph.END)

r, _ := g.Compile()
final, err := r.InvokeWith(ctx, init, graph.RunOptions[MyState]{
    Timeout:     2 * time.Minute,
    NodeTimeout: 30 * time.Second,
    Logger:      logger,
})
```

For one-shot chat work, the equivalent of a Genkit "tool-calling
flow" is `agent.Run` or `agent.NewReAct`.

## Tools

| In Genkit Go | In galdor |
|---|---|
| `genkit.DefineTool(name, desc, func(ctx, in) (out, error))` | `tool.MustNewTool(name, desc, func(ctx, in) (out, error))` |
| JSON Schema from struct tags | JSON Schema from struct tags (`jsonschema:"..."`) |
| `ai.WithTools(tool1, tool2)` per Generate call | `tool.NewRegistry(tool1, tool2)` once, then `provider.Request.Tools` |
| Built-in tool helpers in `plugins/` | `pkg/tool/builtins` (math, time, file_read, http_get) |

```go
type lookupIn struct {
    Topic string `json:"topic" jsonschema:"the topic to look up"`
}
type lookupOut struct {
    Body string `json:"body"`
}

lookup := tool.MustNewTool("lookup_doc", "Look up an internal doc",
    func(ctx context.Context, in lookupIn) (lookupOut, error) {
        return lookupOut{Body: kb[in.Topic]}, nil
    })

reg, _ := tool.NewRegistry(lookup)
```

## Plugins → providers + memory modules

Genkit's `plugins/` directory is one of its biggest strengths:
each provider, vector store, and integration is a Go plugin you
import and register with `genkit.Init`. galdor's equivalents:

| Genkit Go plugin family | galdor module |
|---|---|
| `plugins/googlegenai` (Gemini) | `providers/google` |
| `plugins/anthropic` | `providers/anthropic` |
| `plugins/compat_oai/openai` and the OAI-compatible adapters | `providers/openai` (use `BaseURL` for MiniMax/Together/Groq/...) |
| `plugins/vertexai` | `providers/bedrock` for AWS; for Vertex, `providers/google` with a custom `HTTPClient` |
| `plugins/pinecone` / vector-store plugins | `memory/sqlite`, `memory/pgvector`, `memory/qdrant` |
| `plugins/mcp` (client + server) | `pkg/mcp` (client + server, stdio) |
| `plugins/evaluators` | `pkg/eval` |

Registration is by direct import, not a global plugin registry:

```go
import (
    anthropic "github.com/YasserCR/galdor/providers/anthropic"
    "github.com/YasserCR/galdor/memory/sqlite"
)

p, _ := anthropic.New(anthropic.Config{APIKey: ...})
store, _ := sqlite.Open("./rag.db")
```

## Monitoring → OTel + embedded dashboard

| In Genkit Go | In galdor |
|---|---|
| Genkit Monitoring (GCP) | `galdor ui --db ./traces.db` (embedded dashboard) |
| OTel via `googlecloud` plugin | OTel via standard exporters (Datadog, Honeycomb, Tempo, …) |
| Trace UI in Firebase console | Trace UI in the local web dashboard or piped to your existing OTel stack |

Genkit Monitoring is GCP-only. galdor's observability is
single-binary self-hostable: the same OTel spans go through a
SQLite exporter (read by the embedded dashboard) or any OTLP
exporter you point at your existing pipeline.

```go
import (
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    "github.com/YasserCR/galdor/pkg/observability"
)

exporter, _ := observability.NewSQLiteExporter("./traces.db")
tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
p = observability.InstrumentProvider(p, tp.Tracer("agent"),
    observability.WithCaptureContent(true))
```

Then `galdor ui --db ./traces.db` and the dashboard is live on
loopback. For Datadog / Honeycomb / Tempo, swap the SQLite
exporter for an OTLP exporter — the instrumentation code stays
the same.

## Multi-agent

Genkit Go has Flows + tool-calling agents; there's no first-
class supervisor / swarm primitive. In galdor, `pkg/council`
gives you both:

```go
supervisor, _ := council.NewSupervisor(council.SupervisorConfig{
    Provider: p, Model: "claude-haiku-4-5",
    Workers: []council.Worker{
        {Name: "research", Description: "looks up facts", Run: researchFn},
        {Name: "write",    Description: "drafts copy",   Run: writeFn},
    },
})
```

See [multi-agent](../patterns/multi-agent.md).

## MCP

Both projects have first-party MCP client + server support
(stdio in galdor; stdio / SSE / StreamableHTTP in Genkit). The
shapes are similar:

```go
srv := mcp.NewServer(reg, mcp.ServerInfo{Name: "my-tools", Version: "0.1"})
transport := mcp.NewStdioTransport(os.Stdin, os.Stdout)
_ = srv.Serve(ctx, transport)
```

See [mcp-server](../patterns/mcp-server.md) for the Claude
Desktop wiring.

## A2A

Genkit Go does not ship A2A. Even though Google authored A2A,
its Go support lives in the separate `a2aproject/a2a-go` SDK and
in ADK Go, not in Genkit. galdor ships A2A client + server in
`pkg/a2a`.

## What Genkit has that galdor doesn't

- **A richer plugin ecosystem** for niche vector stores and
  loaders. galdor's memory backends are three (SQLite/BM25,
  pgvector, qdrant); if you depend on a Genkit plugin galdor
  doesn't have an equivalent for, you'll write a `memory.Store`
  implementation (two methods).
- **Genkit DevUI** for interactive flow tracing. galdor's
  dashboard is intentionally lean: traces, spans, run history,
  live tail.
- **First-class deployment to Firebase / Cloud Functions.**
  galdor produces a static binary; you deploy it wherever you
  run binaries.

## What galdor has that Genkit doesn't

- **No GCP coupling for the polished dashboard.** Single binary,
  loopback by default, your data stays on disk.
- **First-party A2A.**
- **`pkg/council` Supervisor + Swarm** as first-class primitives.
- **`pkg/replay`** — record real runs, replay deterministically
  in CI. See [replay-tests](../patterns/replay-tests.md).
- **Embedded SQLite span store.** Trace data lives in a single
  file you can `scp`, back up, and `galdor scry` against from
  the CLI.

## Tradeoffs to expect

- Genkit's flow-centric mental model maps cleanly onto graph
  nodes; if your flows were short pipelines, the translation is
  almost line-for-line.
- The Genkit DevUI is more polished than `galdor ui` today.
  galdor's UI is intentionally minimal; if you want richer
  exploration, point your OTel pipeline at Honeycomb or
  Grafana Tempo instead.
- Genkit's plugin registration is more dynamic; galdor's is
  explicit Go imports. The result is a smaller dependency
  tree and faster build times, at the cost of less runtime
  pluggability.
