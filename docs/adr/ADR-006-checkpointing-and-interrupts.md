# ADR-006 — Checkpointing and interrupt/resume

- **Status:** Accepted
- **Date:** 2026-05-18
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

ADR-005 settled the synchronous core of `pkg/graph`. The plan also
calls for two related capabilities that the graph runtime must
support before agent helpers (Phase 3 session C) and time-travel
(Phase 9) can be written:

- **Checkpointing**: persisting the state observed between node
  hops so a run can be inspected, audited, or replayed.
- **Interrupt / resume**: pausing a run before a designated node,
  surfacing the state to a caller, and continuing later — possibly
  with the state edited (human-in-the-loop).

The questions this ADR answers:

1. What goes into a Checkpoint, and what is the contract of the
   Checkpointer interface?
2. When does the runtime save — before each node, after, both?
3. How does a node trigger a pause? Declaratively at build time, or
   imperatively at run time?
4. How does a caller drive the pause/resume cycle, and how does it
   pass an edited state back into the run?
5. What does Resume do about the interrupt that paused it the first
   time?

## Decisions

### D1. `Checkpoint[S]` is a small typed record

A `Checkpoint[S]` carries `RunID`, `Step`, `Node`, `State` (the
typed graph state), a `Reason` enum (`step` / `interrupt` / `end`),
and `CreatedAt`. The state field is the value the named node will
receive on the next hop — checkpoints are "what to do next",
not "what just happened". This makes Resume a one-line operation
(load → run from `Node` with `State`).

### D2. `Checkpointer[S]` interface is just Save + Load

```go
type Checkpointer[S any] interface {
    Save(ctx, ck Checkpoint[S]) error
    Load(ctx, runID string) (Checkpoint[S], bool, error)
}
```

`Save` is fire-and-forget — implementations may keep history or
discard old entries. `Load` returns the most recent checkpoint
plus a `bool` so a missing run is distinguishable from a fetch
error. Extra surface (history listing, time-travel, deletion) lives
on concrete implementations (`MemoryCheckpointer.History`), not in
the interface. Keeping the interface narrow makes pluggable backends
cheap to write.

### D3. The runtime saves *before* every node, plus interrupts and END

A run produces one checkpoint per node-about-to-execute (reason
`step`), one checkpoint when an interrupt pauses execution (reason
`interrupt`), and one terminal checkpoint at END (reason `end`).
Saving *before* each node means the post-node failure path leaves a
checkpoint pointing at the failed node, which is what an audit
trail or a Phase 9 time-travel tool wants.

### D4. Interrupts are declared at build time, not raised from within a node

`Graph[S].InterruptBefore(names...)` marks a set of nodes as
gated. The runtime checks the gate before dispatching, persists a
checkpoint, and returns `ErrInterrupted` — the gated node body never
runs until Resume.

A node-raised "interrupt error" sentinel was considered and
rejected for v1: declarative gates are simpler, easier to test, and
keep node bodies free of graph internals. A future ADR can add an
imperative variant if a real use case demands it (e.g. an LLM-driven
"escalate to human" gesture).

### D5. The caller drives the cycle via `RunOptions` and `Resume`

`Invoke` keeps its zero-options ergonomics. `InvokeWith(ctx, initial,
RunOptions[S])` opts into a Checkpointer (RunID required), per-call
MaxSteps overrides, and (for Resume) `OverrideState`. `Resume(ctx,
RunOptions[S])` loads the saved checkpoint, applies any override,
and continues. Both `Invoke` and `Resume` share the same loop core
so behaviors stay aligned.

`Runnable[S]` itself stays immutable per ADR-005 §D5 — checkpointer
and run ID are per-call options, not Runnable fields.

### D6. RunID is required as soon as a Checkpointer is set

Auto-generating IDs would create a "what was my ID?" problem at
resume time. Callers always supply their own — usually a request
ID, a job ID, or a UUID they generate before calling `InvokeWith`.
A missing RunID under a non-nil Checkpointer returns
`ErrCheckpointerMissingRunID` immediately.

### D7. Resume bypasses the interrupt that caused the pause — exactly once

If Resume re-entered through the same interrupt gate that paused
the run, no progress would be made — the runtime would interrupt
again before the gated node body executed. Resume therefore passes
a `bypassInterrupt` flag through the loop core that is consumed
after the first hop. Subsequent passes through the same gate (e.g.
in a loop) re-engage normally.

### D8. `OverrideState` is how human-in-the-loop edits enter the run

When a reviewer changes the state during a pause, they pass the
edited value through `RunOptions.OverrideState`. Resume uses the
override instead of the checkpoint's state for the resumed node.
The override is intentionally a pointer (`*S`) so distinguishing
"no override" from "override with the zero value" is unambiguous.

### D9. `MemoryCheckpointer[S]` is the canonical reference implementation

It is safe for concurrent use, retains every saved checkpoint (per
RunID) so tests and Phase 9 replay can reconstruct timelines, and
exposes `History(runID)` and `Reset(runID)` helpers outside the
interface. Production users plug in persistent implementations
(Postgres, SQLite, Redis); the interface stays small enough that
those impls are ~50 lines each.

## Consequences

**Positive.** Interrupt/resume is a single declarative gesture
(`InterruptBefore`) and a single API call (`Resume`). The same
Runnable runs straight-through, pauses, or resumes — there is no
forked "interruptible" version of the runtime. Human-in-the-loop is
expressed through `OverrideState`, which doubles as the seam where
external systems (approval queues, manual review tools) plug in.
Coverage on the new code is 86.4%, every public behavior is tested.

**Negative.** Every Invoke under a Checkpointer pays one extra
Save per step. For in-memory backends this is microseconds; for
persistent backends it can dominate the run if the state struct is
large. Phase 4 observability can amortize that by batching, but
callers should be aware. The "save before, never after" rule also
means a node body that mutates external systems and then errors
leaves no checkpoint *after* the side effect — the saved checkpoint
points at the failed node, which is correct for replay but does not
encode "the side effect already happened". That's an inherent
limit of fast-replay semantics, not a bug.

## Out of scope

- **Node-raised interrupts** (imperative pause). Declarative gates
  cover the v1 use cases; revisit when a real workflow needs
  data-driven pausing.
- **Time-travel from arbitrary historical checkpoints.** Replaying
  from `history[i]` instead of the latest entry is a one-line
  addition once Phase 9 lands the surrounding UX.
- **Distributed Checkpointers.** Postgres / Redis / Bedrock-state
  backends are not in this session; the interface lets them land
  whenever needed without further ADRs.
- **Cancellation policy at interrupt time.** ctx.Cancel mid-pause
  is currently a no-op — the runtime has already returned. If
  resumes need to be cancelable mid-resume, the existing ctx in
  Resume already does that.

## References

- ADR-005 — graph runtime shape (synchronous core).
- `pkg/graph/checkpoint.go`, `pkg/graph/runnable.go`,
  `pkg/graph/errors.go`, `pkg/graph/graph.go`.
- `examples/graph-interrupt/` — runnable HITL demo.
