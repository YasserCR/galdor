# replay

`pkg/replay` reproduces a past agent run from its recorded trace. A `replay.Provider` is a `provider.Provider` backed by a list of `RecordedCall` values — prompts paired with the responses the real LLM produced. Plug it into `agent.Config` or anywhere else that accepts a `provider.Provider` and the system behaves exactly as before, without spending tokens, without network calls, and (when the model was deterministic at the wire level) without flakiness.

What this unlocks: regression tests that exercise the full graph + tool + memory chain on real-world inputs, cheap debugging (load a production run, step through it locally), deterministic CI (one paid-API run becomes a permanent fixture).

## Core types

```go
type Mode int
const (
    ModeStrict Mode = iota // Nth call must match Nth recording; ErrPromptMismatch on drift
    ModeLenient            // match by fingerprint; order doesn't matter
)

type RecordedCall struct {
    SpanID   string             // informational
    Model    string
    Prompt   []schema.Message
    Response *provider.Response
}

func (r RecordedCall) Fingerprint() string // SHA-256 of canonical JSON

type Recording struct {
    Version int            // fixture schema version
    RunID   string
    Note    string
    Calls   []RecordedCall
}

const CurrentFixtureVersion = 1

type Provider struct{ /* ... */ }
func NewProvider(calls []RecordedCall, mode Mode) *Provider

var (
    ErrPromptMismatch = errors.New("replay: prompt does not match next recorded call")
    ErrUnknownPrompt  = errors.New("replay: no recorded call matches this prompt")
    ErrExhausted      = errors.New("replay: recording exhausted")
    ErrNoContent      = errors.New("replay: span has no captured content (...)")
)
```

`Provider` is safe for concurrent `Generate` calls. `Stream` is not supported — fold streaming consumers back to non-streaming at the call site for replay. `Capabilities` reports `ToolCalling: true` so recorded tool-calling responses replay correctly.

## Fingerprinting

`Fingerprint()` returns `sha256(canonical-json(prompt))`. `encoding/json` sorts map keys, so identical message slices with identically-keyed metadata produce identical fingerprints regardless of order in the underlying maps. Two practical consequences: small reorderings inside metadata maps don't break a match, but reordering messages does.

`ModeStrict` compares the next recorded fingerprint to the incoming one and errors on mismatch. `ModeLenient` looks up the incoming fingerprint in a `map[string]int` keyed by fingerprint at construction time — order-insensitive, works across graph restructurings as long as the same prompts surface eventually.

## Recording a run

Replay requires the original run to have been recorded with prompt + completion capture turned on:

```go
import (
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    "github.com/YasserCR/galdor/pkg/observability"
)

exporter, _ := observability.NewSQLiteExporter("./traces.db")
tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
tracer := tp.Tracer("my-agent")

p = observability.InstrumentProvider(p, tracer,
    observability.WithCaptureContent(true))
```

Without `WithCaptureContent(true)`, the spans don't carry `gen_ai.prompt` and `gen_ai.completion`, and `LoadFromStore` returns `ErrNoContent`-wrapped errors. The CLI surfaces this with a remediation hint:

```
$ galdor scry replay <run-id>
scry replay: replay: span has no captured content (run with observability.WithCaptureContent(true)): span <id>

This run was recorded without prompt/completion capture, so it cannot be replayed.
Re-run with observability.WithCaptureContent(true) to enable replay.
```

## Exporting a fixture from a stored run

```
galdor scry replay <run-id> --db ./traces.db -o fixture.json --note "v2.1 of the assistant prompt"
```

The CLI calls `replay.LoadFromStore(ctx, dbPath, runID)`, prints a summary (call count, model per call, fingerprint preview, reply preview), and when `-o` is set writes a portable JSON fixture via `SaveToFile`. The fixture is the `Recording` struct verbatim — hand-editable, diff-friendly.

Programmatic equivalent:

```go
rec, _ := replay.LoadFromStore(ctx, "./traces.db", runID)
rec.Note = "v2.1 of the assistant prompt"
_ = replay.SaveToFile(rec, "fixture.json")
```

## Replaying

```go
import "github.com/YasserCR/galdor/pkg/replay"

rec, _ := replay.LoadFromFile("fixture.json")
mock := replay.NewProvider(rec.Calls, replay.ModeStrict)

r, _ := agent.NewReAct(agent.Config{
    Provider: mock, Model: "claude-haiku-4-5", Tools: reg,
})
final, err := r.Invoke(ctx, state)
if errors.Is(err, replay.ErrPromptMismatch) {
    // A prompt drifted — the error message identifies which call.
}

if remaining := mock.Remaining(); remaining > 0 {
    t.Fatalf("replay didn't exhaust the recording: %d calls left", remaining)
}
```

`Remaining()` (strict mode) and `Reset()` are the test ergonomics — assert the recording was fully consumed, or rewind to drive several sequential replays from one fixture.

## Deep-copy semantics

`Generate` returns a deep-copied `*provider.Response` so a caller that mutates the result (appends to `Message.Content`, modifies `ToolCalls`, edits `ProviderRaw`) doesn't poison subsequent replays. The slice and byte-slice fields are cloned; the response struct is copied by value. Test mutations are safe.

## Lenient mode

```go
mock := replay.NewProvider(rec.Calls, replay.ModeLenient)
```

Order-insensitive lookup by fingerprint. The fingerprint map is built once at construction. Use this when the graph has been restructured between recording and replay (e.g., a node was split into two) but the surfaced prompts haven't changed. The trade-off: silent prompt drift won't trip an error the way strict mode does — you get `ErrUnknownPrompt` only when no recorded prompt matches at all.

## Gotchas

- Fixture files are versioned (`Recording.Version`). `LoadFromFile` rejects any version other than `CurrentFixtureVersion` so old fixtures fail loudly instead of decoding into a misshapen struct.
- `ErrPromptMismatch` is wrapped (`fmt.Errorf("%w: call %d expected ... got ...", ErrPromptMismatch, ...)`); use `errors.Is` to detect it.
- "Content was not captured" applies only to runs recorded *without* `WithCaptureContent(true)`. Fix the recording side, not the replay side.
- `LoadFromStore` filters to `observability.SpanProviderGenerate` spans only; `galdor.provider.stream` spans aren't replayable today.
- `provider.Stream` returns `provider.ErrUnsupported` on a replay provider. If your agent uses streaming in production, switch to non-streaming for the replay (or wrap a streaming consumer to fold to a single `Generate`).
- Fingerprints depend on `schema.Message` JSON. Field changes in `pkg/schema` are rare, but any of them invalidate existing fixtures — re-record after upgrading across one.

## See also

- [observability](observability.md) — the source of truth for captured prompts.
- [provider](provider.md) — the interface `replay.Provider` satisfies.
- [eval](eval.md) — drive a regression suite from a recorded fixture.
- [`examples/integration-cost-tracked`](../../examples/integration-cost-tracked/) — the complementary budget-enforcement pattern.
