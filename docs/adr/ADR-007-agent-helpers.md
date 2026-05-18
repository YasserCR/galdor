# ADR-007 — Agent helpers (`pkg/agent`)

- **Status:** Accepted
- **Date:** 2026-05-18
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

`pkg/agent` is the seam where the lower-level primitives — providers
(ADR-002), tools (ADR-004), graph runtime (ADR-005), checkpoints
(ADR-006) — compose into agents users actually write code against.
The plan calls out four helpers (ReAct, PlanAndExecute, Reflexion,
Supervisor); this session ships ReAct and leaves the rest for
follow-ups.

Several questions had to be settled now because they affect every
subsequent helper:

1. Is an "agent" a struct, a runnable, or both?
2. How does configuration enter a helper — value semantics or builder?
3. How are tools, models, and the conversation kept in sync across
   the ReAct cycle?
4. How does a caller drive a multi-turn or interruptible agent?
5. What's the relationship between `agent.Run` (one-shot) and the
   underlying `graph.Runnable[State]`?

## Decisions

### D1. An agent is a `graph.Runnable[State]`. There is no separate "Agent" struct

`agent.NewReAct(cfg)` returns `(*graph.Runnable[agent.State], error)`.
Callers drive it with the existing graph primitives — `Invoke`,
`InvokeWith`, `Stream`, `Resume` — and get checkpointing, streaming
and human-in-the-loop pauses for free. Wrapping the Runnable in a
new `Agent` struct would force every graph feature to be re-exposed
as a method, and would split the mental model into "agents" vs
"graphs" with no semantic difference.

This is the most important decision in the ADR: helpers are graph
*constructors*, not new top-level abstractions.

### D2. `agent.State` is a small value type

The ReAct state is just:

```go
type State struct {
    Messages   []schema.Message
    FinalText  string
    Iterations int
}
```

The provider, the registry, the model name and the sampling knobs
are closed over by the node functions at `NewReAct` time — they are
not part of the per-run state. State that doesn't change across the
loop has no business in `State`.

`FinalText` is a convenience mirror of the last assistant message's
`Text()`. `Iterations` lets the conditional router (and tests)
enforce the per-run iteration cap independently of the graph's
hard MaxSteps guard.

### D3. ReAct is a 3-edge graph

```
START -> model
model -> (tool calls?  tools  :  END)
tools -> model
```

When `Config.Tools` is nil, the `tools` node and its back-edge are
omitted; the router's only useful return is `END`, and the agent
becomes a single-shot LLM call. This makes "no tools, just an LLM"
the cheapest possible agent without a special code path.

### D4. `Config` is plain data; helpers validate at construction

`Config` is a struct with public fields. `NewReAct` validates
required fields (Provider, Model) and returns an error rather than
panicking, so callers can validate at startup. `MustNewReAct` would
mirror `tool.MustNewTool` (ADR-004); we'll add it the moment a real
use case asks for it, not before.

### D5. `agent.Run` is a one-shot convenience; for everything else, drive the Runnable

```go
func Run(ctx, cfg, input string, system ...string) (string, error)
```

Builds the seed `State` (variadic system prompts + the user input),
invokes a fresh ReAct runnable, and returns the final assistant
text. Multi-turn chats, streaming, mid-run interrupts, time-travel
debugging — none of those go through `Run`. Callers reach for
`NewReAct` directly and drive the Runnable. This keeps `Run`
trivial and `Run`'s contract obvious (one input, one output, no
state retained between calls).

### D6. Tool choice is `auto` by default; `ForceToolUse` switches to `required`

When `Config.Tools` is non-nil, the model request carries
`ToolChoice = auto` so the model can choose between calling a tool
and answering directly. `Config.ForceToolUse = true` switches to
`required`, useful for agents that must always answer through tools
(retrieval-grounded QA, tool-only DSLs). `none` is not exposed —
callers can achieve it by leaving `Config.Tools` nil.

### D7. MaxIterations caps the model-tool cycle; MaxSteps is the runtime backstop

`Config.MaxIterations` (default 10) bounds how many times the model
node runs. The router checks this counter and returns `END` once
exceeded. The Runnable's `MaxSteps` is set to `MaxIterations*3+4`
so the graph runtime's hard guard is never the first thing to fire
under normal operation; it's there for malformed Configs only.

The split lets callers reason about "how many LLM calls" (which is
what costs money) without having to mentally translate steps into
LLM calls.

### D8. PlanAndExecute / Reflexion / Supervisor wait

Each is non-trivial:

- **PlanAndExecute** is a planner LLM + executor LLM, often with
  different system prompts and different tool sets. It deserves
  its own Config shape rather than being shoehorned into ReAct.
- **Reflexion** layers a self-critique loop on top of either ReAct
  or PlanAndExecute. It's better as a wrapper than a constructor.
- **Supervisor** (multi-agent) is the gateway to `pkg/council`
  (Phase 7). Building it now would prejudge the multi-agent
  primitives.

Shipping ReAct alone keeps this ADR scoped and lets the plan stay
honest about phase boundaries.

## Consequences

**Positive.** `agent.NewReAct` is ~150 lines of straight-line Go,
mostly closures. The Runnable it returns is just a graph — every
existing pkg/graph feature (streaming, checkpointing, interrupts,
Resume with `OverrideState`) works without a second implementation.
Coverage is 88.6% on the first commit. The example wires a custom
tool + a builtin into the loop end-to-end and runs offline.

**Negative.** Agents that need more than `Config` can express must
either wrap `NewReAct` with their own builder (returning the same
`*graph.Runnable[State]`) or call into the graph primitives
directly. There is no "subclass" path; that's the price of D1. If a
real pattern requires extension (e.g. emitting custom OTel spans
from inside the model node), it lands as a new ADR proposing a
hook surface, not as a `MyReAct` type.

## Out of scope

- **PlanAndExecute / Reflexion** — separate ADRs when they land.
- **Multi-agent supervisor / council** — `pkg/council`, Phase 7.
- **Custom node hooks** (pre/post-model, pre/post-tool callbacks).
  Useful for observability and rate limiting; revisit once Phase 4
  trace plumbing exists, because hooks and spans should agree on
  what they emit.
- **Streaming intermediate tool outputs to the caller**. The
  graph's `Stream` already surfaces node transitions; a future
  extension can layer assistant-message deltas on top.

## References

- ADR-002 — provider abstraction.
- ADR-004 — tool system.
- ADR-005 — graph runtime.
- ADR-006 — checkpointing + interrupts.
- `pkg/agent/react.go`, `pkg/agent/react_test.go`.
- `examples/agent-react/` — runnable demo combining a custom
  weather tool and the built-in math tool.
