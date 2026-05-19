# integration-approval-gate

Human-in-the-loop pattern: the agent runs autonomously up to the
boundary of an irreversible action (here: a money transfer),
**pauses**, persists its state to a checkpoint, and waits for a
human's decision before continuing.

```
START
  │
  ▼
┌──────────┐    ┌────────┐    ╔══════════════╗
│ validate │ →  │ review │ →  ║ execute  ║   (pauses HERE)
└──────────┘    └────────┘    ╚══════════════╝
                                     │
                              human approves/rejects
                                     │
                                     ▼
                                   END
```

The double-bordered "execute" node is `InterruptBefore`-gated. The
runtime saves a checkpoint, returns `ErrInterrupted`, and waits.
The caller (your approval UI, your Slack bot, the bank's
compliance workflow) loads the checkpoint, decides, and either
abandons the run or calls `Resume(ctx, RunOptions{OverrideState: ...})`
to inject the decision and continue.

## Why this is useful

* **Auditable**: every paused run leaves a checkpoint. You can
  show a regulator exactly what the agent decided, what the human
  decided, and when each decision happened.
* **Safe by construction**: the irreversible node is statically
  reachable only after the gate. There's no code path where the
  transfer happens without a human signoff.
* **State edits are explicit**: `OverrideState` makes the human's
  decision a first-class input to the resumed run, not a magic
  field someone might forget to set.

## Running it

```bash
go run ./examples/integration-approval-gate
```

You'll see three scenarios:

* **$75**: low risk → standard approval → executed.
* **$6,500**: HIGH risk → second signer required and authorized → executed.
* **$12,000**: above the $10k cap → human rejects → NOT executed,
  run remains in checkpoint state.

The "human" is just a Go function (`promptHuman`). In a real
deployment that's the function your approval UI calls when an
operator clicks Approve/Reject.

## Inspecting the trace

```bash
galdor ui --db ./traces.db
# open http://127.0.0.1:7777
```

The trace shows the pause as a checkpoint-step span, and the
resume as a fresh root span linked by `RunID`. The steps view
walks you through validate → review → (pause) → execute.

## Adapting it to a real LLM

Replace the deterministic nodes with `agent.NewReAct` sub-runnables
(one per node) and have the LLM-driven validator decide the risk
level. The interrupt/resume scaffolding stays exactly the same.

## Files

* `main.go` — graph + interrupt gate + scripted human decision
