# Replay tests: paid run → fixture → deterministic CI

## When to use this pattern

You have an agent or graph that works against a real provider,
and you want a regression test that catches prompt drift without
hitting the network and without burning tokens on every CI run.
Record one real run with content capture, export the recording
to a fixture file, swap a `replay.Provider` into your test, and
the same run replays for free.

## Minimal sketch

```go
import (
    "context"
    "testing"

    "github.com/YasserCR/galdor/pkg/agent"
    "github.com/YasserCR/galdor/pkg/replay"
    "github.com/YasserCR/galdor/pkg/schema"
)

func TestSupportBot_BillingFlow(t *testing.T) {
    rec, err := replay.LoadFromFile("testdata/billing-flow.json")
    if err != nil { t.Fatal(err) }

    p := replay.NewProvider(rec.Calls, replay.ModeStrict)

    r, err := agent.NewReAct(agent.Config{
        Provider: p,
        Model:    "claude-haiku-4-5",
        Tools:    buildBillingTools(),
    })
    if err != nil { t.Fatal(err) }

    final, err := r.Invoke(context.Background(), agent.State{
        Messages: []schema.Message{schema.UserMessage("refund INV-12345")},
    })
    if err != nil { t.Fatalf("replay: %v", err) }
    if got, want := final.FinalText, "Refund processed"; got != want {
        t.Errorf("final = %q, want %q", got, want)
    }
}
```

## Recording a run

Two requirements:

1. The original run must be **wrapped with content capture**:

   ```go
   tracer := tp.Tracer("my-agent")
   p = observability.InstrumentProvider(p, tracer,
       observability.WithCaptureContent(true))
   ```

   Without `WithCaptureContent(true)` the spans hold timing and
   token counts but no prompt or completion text, so there's
   nothing to replay. The CLI surfaces this case with
   `replay.ErrNoContent`.

2. The traces must land in a SQLite store you can read back from:

   ```go
   exporter, _ := observability.NewSQLiteExporter("./traces.db")
   tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
   ```

Run your agent once. Note the `RunID` (it shows up in
`galdor scry list` and in the dashboard). Export the recording:

```bash
galdor scry replay <run-id> --db ./traces.db -o testdata/billing-flow.json
```

Commit `billing-flow.json` to the repo. It's a portable JSON
file — diffable, reviewable, ~1-10 KB per turn.

## Strict vs lenient

`replay.NewProvider(calls, mode)` takes a `Mode`:

| Mode | Match rule | Use when |
|---|---|---|
| `replay.ModeStrict` | The Nth incoming call must match the Nth recorded call by canonical-JSON prompt fingerprint. | The graph structure is stable; you want every prompt change to fail the test loudly. |
| `replay.ModeLenient` | The framework looks up the recorded response by SHA-256 fingerprint of the prompt. Order doesn't matter. | The graph was refactored (different node order, but same prompts) and you want the fixture to survive. |

Default to `ModeStrict` — that's what catches prompt drift,
which is the whole point. Reach for `ModeLenient` when the test
suite explicitly tests prompt content, not call ordering.

## What ErrPromptMismatch means

In strict mode, if the incoming prompt's fingerprint doesn't
match the next recorded call's fingerprint, `Generate` returns
`replay.ErrPromptMismatch`:

```
replay: prompt does not match next recorded call: call 3 expected fingerprint a1b2c3d4e5f6, got 9f8e7d6c5b4a
```

Three causes, in order of likelihood:

1. **Your prompt changed.** Someone edited the system prompt,
   added a tool, renamed a tool, changed `MaxIterations` so the
   conversation history is shorter. Inspect the diff; either
   accept the new behaviour and re-record, or revert.
2. **A non-deterministic field leaked in.** Timestamps, UUIDs,
   `time.Now()` in a tool description. Make them deterministic
   (inject a clock) and re-record.
3. **The fixture is stale.** Your code is right, the fixture
   is from a different agent version. Re-record.

`ErrUnknownPrompt` (lenient mode) means the same thing: this
prompt was never seen during recording.

## Walkthrough

1. **Record.** Run the agent against a real provider with
   content capture on. The provider's `Generate` spans now carry
   the prompt + completion in their attributes.
2. **Export.** `galdor scry replay <run-id> -o fixture.json`
   reads those spans, sorts by start time, and writes a
   `replay.Recording{Version, RunID, Note, Calls}` JSON file.
3. **Swap.** In the test, `replay.LoadFromFile("fixture.json")`
   parses the recording. `replay.NewProvider(rec.Calls, mode)`
   builds a `provider.Provider` that fakes every `Generate`
   call by serving the matching recorded response.
4. **Replay.** Run the agent identically — same `Tools`, same
   `Model`, same initial state. The provider serves canned
   responses; tools execute for real (they were never mocked).
   The test asserts on the final output.

## Common variations

### Loading directly from a store

You don't have to go through a file. `replay.LoadFromStore(ctx,
dbPath, runID)` returns a `Recording` in-memory:

```go
rec, _ := replay.LoadFromStore(ctx, "./traces.db", "run-abc-123")
p := replay.NewProvider(rec.Calls, replay.ModeStrict)
```

Useful for "rerun this exact prod incident locally" debugging.

### Reset between replays

`(*replay.Provider).Reset()` rewinds a strict-mode replay's
index counter so the same fixture drives multiple sequential
runs. No-op in lenient mode.

### Assert every call was used

`(*replay.Provider).Remaining()` reports how many recorded calls
haven't been served. In a strict test, assert
`p.Remaining() == 0` at the end — that catches "the agent
short-circuited and didn't make the calls you thought it would".

### Recording offline

The MCP / observability spans aren't the only way to build a
`Recording`. You can hand-write `RecordedCall` values and feed
them to `NewProvider` directly. The JSON tags make this clean
when you want a tiny inline fixture in a test instead of a
file.

## Gotchas

- **Tools run for real.** Replay only fakes the provider. If
  your tool calls a real API, the test still hits the network.
  Wrap tools in their own fakes if isolation matters.
- **`Stream` is not implemented.** Replay providers return
  `provider.ErrUnsupported` from `Stream`. Tests that consume a
  stream should fold the stream consumer back into a
  non-streaming `Generate` at the call site.
- **Fixtures version-pin.** `Recording.Version` is checked at
  load. A breaking change to the fixture format will surface as
  "version X unsupported (want Y)"; the fix is to re-export.
- **The fingerprint sees the whole prompt slice.** Adding a
  single message in the seed conversation changes every
  downstream fingerprint. Don't be clever with conditional
  system messages between record and replay.
- **`schema.Message` ordering inside the slice matters.**
  Maps inside JSON are sorted; slices are not. Two prompts that
  differ only in message order do not match.

## Links

- Runnable example: closest is [examples/integration-cost-tracked](../../examples/integration-cost-tracked/)
  for the complementary "don't burn money" angle. The replay
  package is exercised heavily in `pkg/replay/replay_test.go`.
- Concept: [replay](../concepts/replay.md)
- Concept: [observability](../concepts/observability.md)
- CLI: `galdor scry replay <run-id>` — see `galdor scry --help`.
