# Graph

`pkg/graph` is galdor's generic graph runtime. A `Graph[S]` is a directed graph of named nodes connected by edges, parameterized over a state type you choose. It's the substrate every higher-level agent runs on — `pkg/agent` compiles ReAct and Plan-and-Execute into graphs, `pkg/council` composes multiple sub-graphs, the observability hooks emit OTel spans against this runtime — and it's also the right tool when you need a workflow more bespoke than a stock pattern can express.

## The shape

```go
type NodeFunc[S any] func(ctx context.Context, state S) (S, error)
type Router[S any]   func(state S) string

type Graph[S any] struct { /* opaque */ }

func New[S any]() *Graph[S]
func (g *Graph[S]) AddNode(name string, fn NodeFunc[S]) *Graph[S]
func (g *Graph[S]) AddEdge(from, to string) *Graph[S]
func (g *Graph[S]) AddConditionalEdge(from string, router Router[S]) *Graph[S]
func (g *Graph[S]) AddConditionalEdges(from string, router Router[S], branchMap map[string]string) *Graph[S]
func (g *Graph[S]) InterruptBefore(names ...string) *Graph[S]
func (g *Graph[S]) Compile() (*Runnable[S], error)
```

The compiled `Runnable[S]` is what you actually run; it's immutable and safe for concurrent use.

```go
type Runnable[S any] struct { MaxSteps int /* ... */ }

func (r *Runnable[S]) Invoke(ctx context.Context, initial S) (S, error)
func (r *Runnable[S]) InvokeWith(ctx context.Context, initial S, opts RunOptions[S]) (S, error)
func (r *Runnable[S]) Stream(ctx context.Context, initial S) <-chan Event[S]
func (r *Runnable[S]) Resume(ctx context.Context, opts RunOptions[S]) (S, error)
func (r *Runnable[S]) Inspect() Spec
```

`RunOptions` is where you wire in everything optional:

```go
type RunOptions[S any] struct {
    Checkpointer  Checkpointer[S]
    RunID         string
    MaxSteps      int
    OverrideState *S
    Hooks         Hooks[S]
    Timeout       time.Duration
    NodeTimeout   time.Duration
    Logger        *slog.Logger
}
```

Two special node names are reserved: `graph.START` (the implicit entry; install one outgoing edge from it) and `graph.END` (the terminal sink).

## Things you do with it

### 1. Build a graph

Each node is `func(ctx, state) (state, error)`. State is treated as a value: nodes return a new `S` rather than mutating the receiver.

```go
import "github.com/YasserCR/galdor/pkg/graph"

type state struct{ N, Limit int }

r, err := graph.New[state]().
    AddNode("inc", func(_ context.Context, s state) (state, error) {
        s.N++
        return s, nil
    }).
    AddEdge(graph.START, "inc").
    AddConditionalEdge("inc", func(s state) string {
        if s.N >= s.Limit {
            return graph.END
        }
        return "inc"
    }).
    Compile()
if err != nil {
    log.Fatal(err)
}
final, _ := r.Invoke(ctx, state{Limit: 5})
```

`Compile` validates the topology and returns an `*Runnable[S]` or a `*CompileError` whose `Problems` field aggregates every issue the builder found. Errors are accumulated by the chainable builder and surfaced once at `Compile` so the call sites stay readable. The full runnable example: [`examples/graph-counter`](../../examples/graph-counter/).

When the router's decision domain should be decoupled from node names — semantic labels like `"approve"` / `"reject"` / `"needs_human"` rather than the names of the nodes that handle each outcome — use `AddConditionalEdges` (plural) and pass a branch map. Matches LangGraph's `add_conditional_edges` shape.

```go
r, err := graph.New[review]().
    AddNode("draft",   draft).
    AddNode("publish", publish).
    AddNode("revise",  revise).
    AddNode("escalate", escalate).
    AddEdge(graph.START, "draft").
    AddEdge("revise",    "draft").
    AddEdge("publish",   graph.END).
    AddEdge("escalate",  graph.END).
    AddConditionalEdges("draft",
        func(s review) string {
            switch {
            case s.Score >= 0.9:           return "approve"
            case s.Score >= 0.5:           return "revise"
            default:                       return "escalate"
            }
        },
        map[string]string{
            "approve":  "publish",
            "revise":   "revise",
            "escalate": "escalate",
        }).
    Compile()
```

Compile validates that every branch-map target is a registered node (or `graph.END`); a typo fails at build time, not on the first run that happens to hit that branch. At runtime, a router returning a label that isn't in the map surfaces as `graph.ErrUnknownBranchLabel`.

### 2. Stream events instead of waiting for the final state

`Stream` returns a buffered channel that emits `EventRunStart`, `EventNodeStart`, `EventNodeEnd`, `EventEdgeTraversed`, `EventRunEnd` and `EventError`. The channel closes when the run terminates; consumers must drain it.

```go
for ev := range r.Stream(ctx, initial) {
    switch ev.Type {
    case graph.EventNodeStart:
        fmt.Printf("enter %s (step=%d)\n", ev.Node, ev.Step)
    case graph.EventError:
        return ev.Err
    }
}
```

This is what `galdor ui` consumes for the live SSE feed; the same channel can drive a CLI progress bar or your own dashboard.

### 3. Pause and resume (human-in-the-loop)

`InterruptBefore("name")` marks a node as gated. When the runtime reaches it, it saves a checkpoint and returns `ErrInterrupted` instead of running the node. The caller inspects (or edits) the saved state and calls `Resume` to continue.

```go
g := graph.New[post]().
    AddNode("write",   write).
    AddNode("review",  review).
    AddNode("publish", publish).
    AddEdge(graph.START, "write").
    AddEdge("write",   "review").
    AddEdge("review",  "publish").
    AddEdge("publish", graph.END).
    InterruptBefore("review")

r, _   := g.Compile()
cp     := graph.NewMemoryCheckpointer[post]()
runID  := "post-1"

_, err := r.InvokeWith(ctx, post{}, graph.RunOptions[post]{
    Checkpointer: cp, RunID: runID,
})
if errors.Is(err, graph.ErrInterrupted) {
    ck, _, _ := cp.Load(ctx, runID)
    edited   := ck.State
    edited.Approved = true   // your UI / Slack bot / etc.

    final, _ := r.Resume(ctx, graph.RunOptions[post]{
        Checkpointer:  cp, RunID: runID,
        OverrideState: &edited,
    })
    _ = final
}
```

Resume bypasses the interrupt on the very next hop (otherwise it would pause again immediately). Subsequent gates on later nodes still fire. The full runnable example: [`examples/graph-interrupt`](../../examples/graph-interrupt/).

`MemoryCheckpointer` keeps the full per-step history in memory; `History(runID)` returns the ordered slice for time-travel UIs and tests. The `Checkpointer` interface is narrow on purpose — drop in your own SQLite/Postgres/Redis implementation when you need cross-process durability.

### 4. Apply timeouts and capture panics

Each `RunOptions` setting is independent; combine them as needed.

```go
final, err := r.InvokeWith(ctx, initial, graph.RunOptions[State]{
    Timeout:     2 * time.Minute,    // whole-run deadline
    NodeTimeout: 30 * time.Second,   // per-node deadline
    Logger:      slog.New(slog.NewJSONHandler(os.Stdout, nil)),
})
```

A panic inside a node is recovered, logged at warn level on `Logger`, and surfaced as a `*graph.PanicError` (matchable with `errors.Is(err, graph.ErrPanic)`). The host process keeps running. Hooks that panic are also recovered and logged — broken instrumentation cannot break the agent.

### 5. Install hooks for observability

`Hooks[S]` is the seam `pkg/observability` uses to emit OTel spans, but any caller can install custom callbacks for metrics, audit trails, or structured logging.

```go
hooks := graph.Hooks[State]{
    BeforeRun: func(ctx context.Context, runID string, initial State) context.Context {
        return ctx // attach a span here, etc.
    },
    BeforeNode: func(ctx context.Context, runID, node string, step int, state State) context.Context {
        return ctx
    },
    AfterNode: func(ctx context.Context, runID, node string, step int, state State, err error) {},
    AfterRun:  func(ctx context.Context, runID string, final State, err error) {},
}
```

`BeforeRun` and `BeforeNode` return an updated `context.Context` that the runtime uses for the wrapped scope, which is how observability hooks attach a span to the context every nested call inherits.

### 6. Introspect the graph

`Runnable.Inspect()` returns a JSON-serializable `Spec` describing the topology — node names, edges, interrupt-gated nodes. `Spec.RenderSVG(w)` produces a self-contained SVG visualization (static edges solid, conditional edges dashed, interrupt-gated nodes highlighted) suitable for embedding in HTML or piping through `rsvg-convert`.

```go
spec := r.Inspect()
_    = spec.RenderSVG(os.Stdout)
```

The embedded web UI uses the same mechanism to render every graph it sees.

### 7. Inspect a recorded graph from the CLI (`galdor weave`)

The topology is captured at run time alongside the trace, so you don't need the Go program to visualize or validate it after the fact:

```bash
galdor weave <run-id> -o graph.svg        # render the topology to an SVG file
galdor weave <run-id> --format json       # dump the raw Spec
galdor weave <run-id> --check             # validate edges/entry; exit 1 on problems
```

`weave` reads the same `Spec` `Inspect()` produces (persisted per run in the trace store), so `--check` catches dangling edges, an unknown entry, and unreachable nodes without re-running the graph. Only runs executed through a `graph.Runnable` record a topology.

## Gotchas

- **State is value-typed.** Nodes receive a copy; they should return a new `S` rather than mutating it in place. The runtime does not deep-copy substructures (maps, slices, pointers) — if you mutate one of those, you'll see the mutation across hops.
- **One outgoing edge per node.** A node can have either a static edge (`AddEdge`), a conditional edge (`AddConditionalEdge`), or a labeled conditional edge set (`AddConditionalEdges`) — never more than one flavor per source. Compile rejects double installs.
- **Two router shapes.** `AddConditionalEdge` expects the router to return a node name directly (or `graph.END`). `AddConditionalEdges` expects the router to return a semantic label that the branch map resolves to a node name — matching LangGraph's `add_conditional_edges(from, router, {label: node})` form.
- **Routers must return a real name or END.** For `AddConditionalEdge`: an empty string is `ErrEmptyRouterResult`; an unknown name is `ErrUnknownNode`. For `AddConditionalEdges`: an empty string is `ErrEmptyRouterResult`; a label not in the branch map is `ErrUnknownBranchLabel`. Intentional dead-ends resolve to `graph.END` (directly or via a branch-map entry).
- **`MaxSteps` is a safety net, not a budget.** The runtime aborts with `ErrMaxSteps` when the step counter exceeds the ceiling. Default is 100; override via `Runnable.MaxSteps` or `RunOptions.MaxSteps`. The `agent` package sizes this generously around its iteration cap so the soft cap (in the router) is what users actually feel.
- **`Checkpointer` requires a `RunID`.** Setting `RunOptions.Checkpointer` without `RunID` returns `ErrCheckpointerMissingRunID` — without an ID you cannot find the saved state again.
- **`Stream` is one-shot.** The channel closes after the first terminal event. Consumers must drain it; the runtime's writer goroutine blocks on backpressure until the consumer reads.

## See also

- [Agent](agent.md) — `NewReAct` and `NewPlanAndExecute` return `*Runnable[State]` and `*Runnable[PlanExecuteState]`; everything in this guide applies to them.
- [Observability](observability.md) — wires `Hooks[S]` to emit OTel spans automatically.
- [Human-in-the-loop pattern](../patterns/human-in-the-loop.md) — the full interrupt/resume workflow.
- Examples: [`graph-counter`](../../examples/graph-counter/), [`graph-interrupt`](../../examples/graph-interrupt/).
