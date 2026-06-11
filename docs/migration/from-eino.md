# Migrating from Eino

This guide is for users coming from
[cloudwego/eino](https://github.com/cloudwego/eino). Both
projects are Go-native agent frameworks; the major differences
are the observability target (Eino ships to Langfuse;
galdor is OTel-native with an embedded dashboard), protocol
support (galdor has first-party MCP server + A2A; Eino has MCP
client only), and the multi-agent surface (Eino has DeepAgent;
galdor has Supervisor + Swarm).

## ChatModel / Provider

| In Eino | In galdor |
|---|---|
| `model.ChatModel` interface | `provider.Provider` interface |
| `openai.NewChatModel(ctx, config)` | `openai.New(openai.Config{APIKey, BaseURL, ...})` |
| `claude.NewChatModel(ctx, config)` | `anthropic.New(anthropic.Config{APIKey, ...})` |
| `gemini.NewChatModel(ctx, config)` | `google.New(google.Config{APIKey, ...})` |
| `cm.Generate(ctx, messages)` | `p.Generate(ctx, provider.Request{Model, Messages, ...})` |
| `cm.Stream(ctx, messages)` | `p.Stream(ctx, req) (StreamReader, error)` |
| `BindTools(tools)` | `provider.Request{Tools: defs, ToolChoice: ...}` |

Eino binds tools onto the ChatModel; galdor passes them per
request via `provider.Request.Tools`. The pattern lets the same
provider serve agents with different tool sets without
rebinding.

```go
resp, err := p.Generate(ctx, provider.Request{
    Model:      "claude-haiku-4-5",
    Messages:   msgs,
    Tools:      toolDefs,
    ToolChoice: provider.ToolChoiceAuto,
})
```

## Tools

| In Eino | In galdor |
|---|---|
| `tool.BaseTool` (Info + InvokableRun) | `tool.Tool[In, Out]` (generic) |
| `utils.NewTool` / hand-written schemas | `tool.MustNewTool` — schema derived from `In` |
| `compose.ToolsNode` | `agent.NewReAct` (or a custom graph node) |
| schema info via `Info()` returning `ToolInfo` | derived by reflection from struct tags |

```go
type lookupIn struct {
    InvoiceID string `json:"invoice_id" jsonschema:"required, invoice ID"`
}
type lookupOut struct {
    Customer string  `json:"customer"`
    Amount   float64 `json:"amount"`
}

lookup := tool.MustNewTool("lookup_invoice", "Look up an invoice by ID",
    func(ctx context.Context, in lookupIn) (lookupOut, error) {
        return lookupOut{Customer: "ACME", Amount: 42.50}, nil
    })

reg, _ := tool.NewRegistry(lookup)
```

## Compose graph → pkg/graph

| In Eino | In galdor |
|---|---|
| `compose.NewGraph[I, O]` | `graph.New[State]()` |
| `g.AddChatModelNode(...)` | `g.AddNode("model", func(ctx, s) (s, error){...})` |
| `g.AddToolsNode(...)` | a node that runs `tool.ExecuteCalls` |
| `g.AddEdge(a, b)` | `g.AddEdge("a", "b")` |
| `g.AddBranch(node, condition)` | `g.AddConditionalEdge("a", router)` |
| `g.Compile(ctx)` | `g.Compile()` |
| `r.Invoke(ctx, in)` | `r.Invoke(ctx, state)` / `r.InvokeWith(ctx, state, opts)` |
| `compose.START` / `compose.END` | `graph.START` / `graph.END` |

Eino's graph is typed on `(input, output)`; galdor's is typed
on a single `State`. Nodes are `func(ctx, S) (S, error)` and the
state flows through. The single-type model is simpler at the
cost of one extra "fan-in / fan-out lives in a state field"
convention for parallel work.

```go
g := graph.New[MyState]().
    AddNode("plan", plan).
    AddNode("act", act).
    AddEdge(graph.START, "plan").
    AddConditionalEdge("plan", router).
    AddEdge("act", "plan")

r, _ := g.Compile()
final, _ := r.Invoke(ctx, MyState{...})
```

## Callbacks → OTel + Hooks

| In Eino | In galdor |
|---|---|
| `callbacks.Handler` interface (OnStart, OnEnd, ...) | `graph.Hooks[S]` (OnRunStart, OnNodeStart, OnNodeEnd, OnRunEnd) |
| Langfuse via `callbacks.AppendGlobalHandlers(...)` | OTel: `observability.TraceHooks[S](tracer)` + `InstrumentProvider` |
| no embedded trace UI | `galdor ui --db ./traces.db` |

The translation has two parts:

1. **Per-call instrumentation.** Wrap the provider:

   ```go
   p = observability.InstrumentProvider(p, tracer,
       observability.WithCaptureContent(true))
   ```

   Every `Generate` becomes a span tagged with the GenAI semantic
   conventions (model, input tokens, output tokens, finish
   reason, optionally the prompt and completion bodies).

2. **Per-run / per-node instrumentation.** Pass hooks into
   `RunOptions`:

   ```go
   r.InvokeWith(ctx, state, graph.RunOptions[State]{
       Hooks: observability.TraceHooks[State](tracer),
   })
   ```

   You get one root span per run and one child span per node
   execution.

Eino's primary observability target is Langfuse (via the
`callbacks-langfuse` extension). galdor's spans go through OTel,
so the same instrumentation works against:

- the embedded SQLite + dashboard (single binary, no setup),
- Datadog / Honeycomb / Grafana Tempo / Jaeger via OTLP,
- Langfuse, by piping OTel spans into Langfuse's OTel ingest if
  that's what your org already uses.

## DeepAgent → Supervisor / Swarm

| In Eino | In galdor |
|---|---|
| `deepagent.DeepAgent` | `council.Supervisor` (hierarchical) or `council.Swarm` (peer) |
| sub-agent delegation | `Worker.Run` (Supervisor) or `Agent.Handoffs` (Swarm) |
| planning tools | a `Worker` running `agent.NewPlanAndExecute(...)` |

Supervisor sketch:

```go
supervisor, _ := council.NewSupervisor(council.SupervisorConfig{
    Provider: p,
    Model:    "claude-haiku-4-5",
    Workers: []council.Worker{
        {Name: "research", Description: "looks up facts", Run: researchAgent.Invoke},
        {Name: "write",    Description: "drafts copy",   Run: writeAgent.Invoke},
    },
})
final, _ := supervisor.Invoke(ctx, council.SupervisorState{Input: q})
```

See [multi-agent](../patterns/multi-agent.md) for the full
discussion of when to pick Supervisor vs Swarm.

## Replay and time-travel

Eino doesn't ship an offline fixture-replay layer. In galdor:

```bash
galdor scry replay <run-id> -o fixture.json
```

then

```go
rec, _ := replay.LoadFromFile("fixture.json")
p := replay.NewProvider(rec.Calls, replay.ModeStrict)
```

See [replay-tests](../patterns/replay-tests.md).

## MCP

Eino has MCP client support first-party; the server side is not
in the core repo. galdor has both client and server (stdio) in
`pkg/mcp` — so you can expose your `tool.Registry` to Claude
Desktop without any extra glue. See [mcp-server](../patterns/mcp-server.md).

## A2A

Eino does not ship A2A (Google's agent-to-agent protocol).
galdor has client + server in `pkg/a2a`.

## What Eino has that galdor doesn't

- **Broader provider component coverage** under `eino-ext`.
  galdor ships four providers; Eino's ecosystem is further along
  in raw adapter count.
- **A graph typed on `(input, output)` explicitly.** If you're
  used to thinking in input/output types per node, galdor's
  single-state model takes some adjustment.

## What galdor has that Eino doesn't

- OTel-native, no extension required.
- Embedded SQLite trace store + dashboard from one binary.
- First-party MCP **server** in addition to client.
- First-party A2A (Google spec) client + server.
- `pkg/replay` for deterministic fixture-based regression tests.
- Capability-aware boundary validation
  (`Provider.Capabilities()` is checked against
  `agent.Config.Tools` at construction).

## Tradeoffs to expect

- Eino's per-component module layout under `eino-ext` is
  similar in shape to galdor's per-provider + per-memory-backend
  layout. Migration tends to be 1:1 at the import level.
- Eino's callbacks fire at richer granularity (per-component);
  galdor's hooks + OTel auto-instrumentation cover provider,
  tool, and node — for finer-grained events, you instrument
  your node functions directly.
