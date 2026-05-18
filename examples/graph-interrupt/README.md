# examples/graph-interrupt

Human-in-the-loop demo of `pkg/graph`'s checkpointer + interrupt
machinery. A tiny editorial pipeline writes a draft, **pauses for
review** (the interrupt), then publishes once a "reviewer" has
approved.

## Run

```bash
go run ./examples/graph-interrupt
```

Expected output:

```
paused at review: "Galdor: speak your AI agents into being."
human reviewer: approving the draft
published=true draft="Galdor: speak your AI agents into being."

checkpoint history:
  step=1 node=write   reason=step
  step=2 node=review  reason=interrupt
  step=2 node=review  reason=step
  step=3 node=publish reason=step
  step=3 node=__end__ reason=end
```

## What it shows

- **`InterruptBefore("review")`** marks a node as a pause point.
  When execution reaches it, the runtime saves a checkpoint with
  reason `interrupt` and returns `ErrInterrupted` *without running
  the node*.
- **`MemoryCheckpointer`** is the canonical in-process
  `Checkpointer`. Production users plug in a Postgres / SQLite /
  Redis-backed implementation — the interface (`Save` + `Load`) is
  intentionally narrow.
- **`Resume` with `OverrideState`** is the human-in-the-loop hook.
  The caller inspects the paused state, edits it (here: setting
  `Approved = true`), and passes it back so the resumed run sees
  the edits instead of the original snapshot.
- **`History(runID)`** on `MemoryCheckpointer` returns every save
  the run produced. Useful for trace UIs and the Phase 9 time-
  travel feature; production checkpointers do not need to retain
  history.

## Composing with the rest of galdor

The same `Runnable[S]` can be:

- driven synchronously with `Invoke` / `InvokeWith`,
- consumed event-by-event with `Stream` (see `examples/graph-counter`),
- paused and resumed with `InterruptBefore` + `Resume` (this example).

Future agent helpers (`agent.ReAct`, Phase 3 session C) layer ReAct
loops on top of this same primitive — the runtime stays one thing.
