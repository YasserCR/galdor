# Human-in-the-loop with InterruptBefore

## When to use this pattern

Some steps in your graph are irreversible: send a wire, file a
ticket, post a public message, drop a database table. You want
the agent to do everything *up to* that step automatically, then
pause, surface its proposed action to a human (UI, Slack,
compliance workflow), and only resume once the human signs off.

`graph.InterruptBefore` is the primitive that gates a node behind
exactly that pause. Combined with a `Checkpointer` and `Resume`,
it gives you three clean phases: run to gate, edit state
externally, resume.

## Minimal sketch

```go
g := graph.New[TransferState]().
    AddNode("validate", validate).
    AddNode("execute", execute).
    AddEdge(graph.START, "validate").
    AddEdge("validate", "execute").
    AddEdge("execute", graph.END).
    InterruptBefore("execute")

r, _ := g.Compile()
ckpt := graph.NewMemoryCheckpointer[TransferState]()

_, err := r.InvokeWith(ctx, init, graph.RunOptions[TransferState]{
    RunID:        runID,
    Checkpointer: ckpt,
})
if !errors.Is(err, graph.ErrInterrupted) {
    return err
}

ck, _, _ := ckpt.Load(ctx, runID)
decided := promptHuman(ck.State)
if !decided.Approved {
    return nil
}

final, _ := r.Resume(ctx, graph.RunOptions[TransferState]{
    RunID:         runID,
    Checkpointer:  ckpt,
    OverrideState: &decided,
})
```

## Phases

### 1. Run to the gate

Call `InvokeWith` with a `Checkpointer` and a `RunID`. The runtime
executes nodes normally until it's about to enter an interrupted
node. At that point it saves a checkpoint (`Reason =
CheckpointReasonInterrupt`) and returns `ErrInterrupted` wrapped
with the node name. The state in that checkpoint is *exactly*
what the gated node would have received.

Detect with `errors.Is(err, graph.ErrInterrupted)`. Any other
error is a real failure.

### 2. Edit state externally

Load the saved checkpoint with `ckpt.Load(ctx, runID)`. The
returned `Checkpoint[S]` has `.State` (the snapshot), `.Node`
(the gated node), `.Step`, and `.Reason`. Hand the state to your
approver — that can be a UI, a Slack interactive message, a
compliance system, an email-with-link, anything that lets a
human inspect and decide.

The checkpoint is the durable record. Between phase 2 and
phase 3, the process can crash and restart with no loss of
state: a fresh `Resume` will reload from `ckpt` and pick up where
the gate left off.

### 3. Resume with OverrideState

Call `Resume` with the same `Checkpointer` + `RunID`. Pass
`OverrideState: &decided` to inject the human's edits — that
value replaces the checkpoint's state for the first node after
the gate. The interrupt is bypassed exactly once (the resumed
node runs even though it's still gated for future calls).

If the human rejected the action, don't resume. The checkpoint
stays on disk as an audit record of "we proposed X, the human
said no, nothing irreversible happened."

## Defensive checks inside the gated node

`OverrideState` is trust-by-contract: the caller can pass any
value. The gated node should re-verify the policies the human is
supposed to honor:

```go
execute := func(_ context.Context, s TransferState) (TransferState, error) {
    if !s.Approved {
        return s, errors.New("approval-gate: execute called without Approved=true")
    }
    if s.Risk == "high" && !s.CounterApproved {
        return s, errors.New("approval-gate: HIGH risk requires CounterApproved=true")
    }
    s.TxID = doTransfer(s)
    return s, nil
}
```

The gate plus the in-node check is the belt-and-braces pattern:
the gate ensures a human had a chance to act; the check ensures
that even a buggy or compromised approver UI can't drive the
action past policy.

## Common variations

### Multiple gates

`InterruptBefore("review", "execute", "publish")` gates each
listed node. Each pause is independent; the run produces one
`ErrInterrupted` per gate. Stage your approvals as a sequence
when one human's decision depends on another's earlier action.

### Persistent checkpointers

`MemoryCheckpointer` is fine for tests. For production, swap in a
SQLite, Postgres, or Redis implementation of `graph.Checkpointer`
— the interface is two methods (`Save`, `Load`). The trace store
already persists run state separately; the checkpointer is what
makes the run *resumable*.

### Combined with observability

Pass `Hooks: observability.TraceHooks[S](tracer)` in `RunOptions`
and the pause + resume show up as one linked run in the trace
store. The dashboard renders the gap explicitly so you can see
how long a run sat waiting on a human.

## Gotchas

- **Don't mutate the checkpoint's state in place.** The
  checkpoint is the source of truth for the run. Build a new
  state value for `OverrideState` and pass it by pointer.
- **`RunID` must be stable.** A `Resume` call with a different
  `RunID` won't find the checkpoint. Generate the ID once,
  outside the agent loop, and persist it in your approval workflow
  alongside whatever the human will look at.
- **The interrupt bypass is one-shot.** A `Resume` skips the gate
  for the first node only. If the same node is reached again
  later in the same run (cycles), it gates again.
- **Checkpoints aren't free.** A `Checkpointer` runs on every
  node entry, not just gated ones. Use a fast in-process
  implementation for hot loops; reserve durable backends for runs
  that span request boundaries.

## Links

- Runnable example: [examples/integration-approval-gate](../../examples/integration-approval-gate/)
- Concept: [graph](../concepts/graph.md)
- Concept: [observability](../concepts/observability.md)
- Related: [cost-tracking](cost-tracking.md) — another way to
  draw a hard line in an agent run.
