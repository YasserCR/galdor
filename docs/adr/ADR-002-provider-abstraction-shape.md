# ADR-002 — Provider abstraction shape

- **Status:** Accepted
- **Date:** 2026-05-17
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

galdor must abstract a heterogeneous set of LLM providers (Anthropic,
OpenAI, Google, Bedrock, Azure, Ollama, vLLM, ...) behind a single Go
interface that:

- never leaks provider-specific details to user code (ADR-001 §D7),
- accepts `context.Context` on every blocking method (ADR-001 §D15),
- treats streaming as a first-class consumption mode, not an optional
  extension (PLAN §5.1),
- can be implemented in an independent Go module per provider (ADR-001 §D9)
  so the core remains dependency-light,
- normalizes errors so callers do not need a switch on provider name.

Several design questions had to be settled before Phase 1 adapters could
begin. This ADR records the answers.

## Decisions

### D1. A single `Provider` interface, not many small ones

`Provider` exposes `Name`, `Capabilities`, `Generate` and `Stream`. The
alternative — many granular interfaces (`Generator`, `Streamer`,
`ToolCaller`, ...) — was rejected because in practice every backend
implements the same four operations and the surface fits in <40 lines.
Granular interfaces would push runtime type assertions to call sites
(`if s, ok := p.(Streamer); ok { ... }`) which defeats the goal of a
uniform API.

Optional features are expressed through `Capabilities`, not through
interface narrowing. Calling an unsupported feature returns
`ErrUnsupported` so callers can fall back gracefully:

```go
if p.Capabilities().Streaming {
    s, err := p.Stream(ctx, req)
    // ...
}
```

### D2. Streaming is the load-bearing path; non-streaming is derivable

`Stream` returns a `StreamReader` whose `Recv(ctx) (Event, error)` returns
`io.EOF` when the run completes. `Generate` returns a fully assembled
`*Response`.

Adapters that can only stream are free to implement `Generate` by calling
their own `Stream` and passing the reader to `CollectStream`. The reverse
(non-streaming adapters faking a stream by emitting a single event) is
also permitted but discouraged — callers that depend on incremental
content deltas would silently see only one large delta.

The chosen iterator shape (`Recv` returning `io.EOF`) is preferred over
the channel-based `<-chan Event` because:

- It allows the consumer to control timeout-per-chunk via `context.Context`
  on every call.
- It avoids goroutine leaks if a consumer abandons the stream early — the
  consumer must call `Close`, which the helper takes care of.
- `iter.Seq` style (Go 1.23+) is not yet available given the Go 1.22 floor
  (ADR-001 §D2). When the floor moves, an `iter.Seq2`-style helper can be
  added without breaking existing callers.

### D3. Tool-call arguments stay as raw JSON

`schema.ToolCall.Arguments` is `json.RawMessage` rather than a typed
`map[string]any` or a generic. Reasons:

- The tool runtime (`pkg/tool`) owns the typed decoding step; teaching
  the provider abstraction about generics over tool inputs would couple
  the two prematurely.
- Raw JSON survives a round-trip into checkpoints and traces verbatim,
  which makes replay (Phase 9) deterministic.
- Providers emit arguments in fragments during streaming;
  `provider.ToolCallDelta.ArgumentsDelta` is appended as a string, then
  finalized as the raw payload — no intermediate parsing.

### D4. Errors are sentinels wrapped in a structured `APIError`

Adapters surface failures as `*APIError{Kind, Provider, StatusCode, Message,
RetryAfter}`. `Kind` is one of the package-level sentinels
(`ErrAuth`, `ErrRateLimited`, `ErrInvalidRequest`, `ErrServer`,
`ErrContextWindow`, `ErrUnsupported`) and is exposed via `Unwrap` so
`errors.Is(err, provider.ErrRateLimited)` works regardless of provider.

A typed error struct (rather than free-form `error`) was chosen so a
retry middleware can read `RetryAfter` and `StatusCode` without parsing
the message string.

### D5. Optional parameters are pointers to distinguish unset

`Request.Temperature`, `TopP` and `MaxTokens` are `*float64` / `*int`.
A zero value would conflict with "explicit zero" (a valid temperature)
versus "use provider default". Adapters MUST treat `nil` as "do not send".

This is uglier at call sites than struct-of-options builders, but a
pointer-based shape is the smallest possible change for callers who do
want the provider default and is consistent across all sampling
parameters.

### D6. Cache control is a hint, never a contract

`schema.CacheControl` is a per-`Message` hint. Providers that support
caching (Anthropic today, OpenAI and Google increasingly) honor it;
providers that do not, ignore it. The hint shape uses Anthropic's
vocabulary (`Type: "ephemeral"`) because they shipped the first widely
adopted spec, with adapter-side translation when other providers extend
the surface.

### D7. `pkg/schema` holds the lingua franca; `pkg/provider` holds the contract

`Message`, `Role`, `ContentPart`, `ToolCall`, `ToolDef`, `Usage`,
`StopReason` and `CacheControl` live in `pkg/schema`. They are reused by
`pkg/tool`, `pkg/graph`, `pkg/observability` (spans serialize them) and
the eventual checkpointer. `pkg/provider` only owns the call contract
(`Request`, `Response`, `Capabilities`, errors, streaming).

The split prevents an import cycle once `pkg/observability` instruments
provider calls: trace spans need to carry `Message` and `ToolCall`
without importing `pkg/provider`.

## Consequences

**Positive.** Adapters land with a minimal, well-typed surface and a
clear answer to streaming, errors, optional parameters and tool argument
handling. Phase 1 sessions 3 and 4 (Anthropic, OpenAI) can proceed
without renegotiating fundamentals. Coverage is high (`pkg/schema` 100%,
`pkg/provider` 96%) with only an in-process stub provider.

**Negative.** Pointer-based optional params are slightly verbose. The
`Event` struct is union-shaped (several fields, only some populated per
`Type`); consumers must switch on `Type`. A sealed-interface alternative
was rejected as more complex than the discriminator pattern justifies at
this stage.

## Out of scope (deferred)

- **ADR-003** — Retry and backoff policy (where it lives — in the
  adapter, in a wrapping middleware, in the agent runtime).
- **ADR-004** — Streaming event schema alignment with OTel GenAI
  semantic conventions.
- **ADR-006** — Cross-provider prompt caching policy in detail (how a
  caller signals "the entire system prompt is stable").
- **ADR-007** — Cost tracking model. `Usage` carries token counts; price
  per token is a separate concern.

## References

- `pkg/provider/provider.go`, `pkg/provider/request.go`,
  `pkg/provider/response.go`, `pkg/provider/stream.go`,
  `pkg/provider/errors.go`, `pkg/provider/capabilities.go`.
- `pkg/schema/*.go`.
- `examples/provider-interface/` — runnable smoke test of the contract.
- ADR-001 — foundational decisions (license, Go floor, OTel core, DCO,
  governance, etc.).
