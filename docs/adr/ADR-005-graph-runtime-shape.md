# ADR-005 — Graph runtime shape

- **Status:** Accepted
- **Date:** 2026-05-18
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

`pkg/graph` is the engine `pkg/agent` (Phase 3 helpers), `pkg/council`
(Phase 7 multi-agent), and the replay engine (Phase 9) all build on.
Several questions had to be settled before any of those phases can be
written:

1. How is state represented and how does it flow between nodes — in
   place, by value, as partial updates?
2. What is a node's signature, and how does cancellation reach it?
3. How are edges expressed — adjacency list, function returns, both?
4. When is a graph validated, and how are builder mistakes surfaced?
5. How do streaming consumers see what the runtime is doing, without
   bolting on a custom logging interface to every node?
6. What's the termination story — only END, also a step ceiling,
   recovery from node errors?

This ADR captures the choices baked into the Phase 3 session A code
that landed `pkg/graph`.

## Decisions

### D1. State is a typed Go value passed by value

`Graph[S]` is parameterized over the state type `S`. Nodes take and
return a full `S`, not a partial update:

```go
type NodeFunc[S any] func(ctx context.Context, state S) (S, error)
```

Compared to LangGraph's Python approach (TypedDict + per-field
reducers), value-typed state is simpler, statically checked, and
removes the runtime cost of merging dicts on every hop. The downside —
that users have to think about copy semantics for mutable
substructures of `S` — is documented in the package docstring and is
the price of staying inside Go's normal value rules.

### D2. Node signature is the smallest thing that's useful

`func(ctx context.Context, state S) (S, error)` is everything a node
sees. Streaming events come from node transitions (decided by the
runtime), not from inside nodes. If a node wants to surface
intermediate progress to a UI, it does it through observability
hooks (Phase 4) rather than a node-local emitter. This keeps the
node-author cognitive surface tiny — write a function, return the
next state — and avoids two competing event channels.

### D3. Edges live alongside nodes in the builder; routers replace static edges

`AddEdge(from, to)` installs an unconditional transition;
`AddConditionalEdge(from, router)` installs a data-driven one. A
single node can have **either** a static edge **or** a conditional
edge, never both — the disjunction is a hard invariant in the
runtime, not a runtime check.

This keeps the resolution rule trivial (one lookup table per kind)
and avoids the surprising "static edge wins / loses against the
router" question entirely.

### D4. START and END are string sentinels

```go
const (
    START = "__start__"
    END   = "__end__"
)
```

START is the entry sentinel — `AddEdge(START, "first")` names the
first real node. END is the terminal sentinel — `AddEdge("last", END)`
(or a router that returns `END`) ends the run.

Sentinel strings, rather than `nil` or a separate API, mean the
builder's mental model is uniform ("everything is a name") and that
serialization (Phase 9 replay, Phase 5 UI) is just storing strings.

### D5. Validation is at Compile() time; builder methods are forgiving

`AddNode`, `AddEdge`, `AddConditionalEdge` always return `*Graph[S]`
so they can be chained. Mistakes (nil functions, duplicate names,
reserved names, conflicting edges) are accumulated on the builder
and surfaced in a single `*CompileError` at `Compile()`. The compiled
`*Runnable[S]` is immutable and safe for concurrent use across many
`Invoke` / `Stream` calls — the graph build phase is over once
`Compile()` succeeds.

### D6. `context.Context` everywhere; the runtime checks at every step

Every node receives a `context.Context`. The runtime checks
`ctx.Err()` between every step of `Invoke` and `Stream`. On `Stream`,
a canceled context causes the runtime to surface the cancellation as
an `EventError`, then close the channel — consumers only need to
range over the channel; they never need to also `select` on `ctx`.

### D7. Streaming events are buffered, typed, and self-closing

`Runnable.Stream(ctx, initial) <-chan Event[S]` returns a channel
buffered at 16 events. The channel is closed when the run terminates
(success or error), so `for ev := range ch { ... }` is the
canonical consumption pattern.

The event surface is intentionally narrow:

- `EventRunStart` — once, with the entry node and initial state.
- `EventNodeStart` / `EventNodeEnd` — paired around each node call.
- `EventEdgeTraversed` — after `EventNodeEnd`, naming the next hop.
- `EventRunEnd` — terminal success.
- `EventError` — terminal failure (channel closed after).

Each event carries the step counter and a state snapshot. That's the
minimum information a UI (Phase 5) or trace exporter (Phase 4) needs
to render a meaningful timeline; richer payloads (token counts,
tool-call timings) come from observability spans, not from the graph
itself.

### D8. Termination is END or a step ceiling

A run terminates one of three ways:

- An edge or router resolves to `END`.
- A node returns an error (`fmt.Errorf("node %q: %w", node, err)`
  wraps it).
- The step counter exceeds `Runnable.MaxSteps` (default 100;
  callers override per Runnable).

Step ceilings are the cheap insurance against misrouted conditionals.
The default of 100 is generous for most workflows and small enough
that an infinite loop fails fast in tests.

### D9. No automatic concurrency yet; nodes run sequentially

Each node runs to completion before the next one is dispatched. The
fan-out / fan-in patterns the README hints at (multi-agent council,
Phase 7) layer on top of this sequential core rather than replace it.
Sequential semantics keep the time-travel and replay stories (Phase
9) tractable: a run is a single ordered list of `(node, in, out)`
triples.

### D10. Routers return strings, including END; empty result is an error

A `Router[S]` returns the next node name. Returning `""` is treated
as `ErrEmptyRouterResult` at runtime — intentional dead-ends should
resolve to `END`, not silence. Returning an unknown node name is
`ErrUnknownNode`. Both are sentinels so middleware can match.

## Consequences

**Positive.** The runtime is ~250 lines of straight-line Go,
parameterized on state, with one builder and one immutable Runnable.
Coverage is 83.9% on the first commit; both `Invoke` and `Stream`
exercise the same `resolveNext` resolver so behavioral parity is
structural. Cancellation, error propagation, max-step guards and
sentinel errors compose with the rest of galdor without any custom
interfaces.

**Negative.** Value-typed `S` rules out the LangGraph-style
"reducer" model where multiple nodes contribute to disjoint fields in
parallel. Users with that need can either widen `S` and update fields
inside a single node, or wait for the Phase 7 council primitives.
The single-edge-per-node rule (D3) means "fan out to 2 branches and
join later" is not a graph primitive — it's a council primitive when
we get there.

## Out of scope

- **Checkpointing and interrupt/resume.** Tracked as the next
  Phase 3 session: a `Checkpointer` interface, an in-memory
  implementation, and `Runnable.Resume(ctx, checkpoint)`.
- **Agent helpers** (`agent.ReAct`, `agent.PlanAndExecute`,
  `agent.Reflexion`). Land once checkpointing is settled; they all
  rely on the ability to pause a loop and re-enter it.
- **Time-travel debugging.** Builds on checkpoints; tracked in
  Phase 9.
- **Multi-agent fan-out / fan-in.** Phase 7's `pkg/council` consumes
  the graph runtime to drive supervisor / swarm patterns. The graph
  itself stays single-threaded.

## References

- ADR-001 §D15 (`context.Context` universality), §D14 (no `panic`
  outside `init`).
- ADR-004 — tool system shape, which a node body typically calls into.
- `pkg/graph/graph.go`, `pkg/graph/runnable.go`,
  `pkg/graph/event.go`, `pkg/graph/errors.go`.
- `examples/graph-counter/` — runnable demo of the conditional-loop
  shape that exercises every event variant.
