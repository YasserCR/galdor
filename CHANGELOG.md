# Changelog

All notable changes to galdor are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Pre-1.0 conventions: minor versions (v0.x.0) may introduce breaking changes;
patch versions (v0.x.y) are reserved for backward-compatible fixes and release
hygiene (docs, build metadata).

## [Unreleased]

## [0.3.0] - 2026-05-30

Correctness & consistency release. Driven by an adversarial audit of the
provider adapters, graph runtime, and memory backends: it closes a class
of "capability lies" (adapters advertising features they didn't wire),
brings the streaming graph path to parity with the synchronous one, and
makes checkpoints, retries, and memory results behave consistently.

No exported function or type was removed or had its signature changed;
the only new public API is additive. Several bug fixes do change
observable behavior — see **Changed** for the ones integrators should
note.

### Added
- **`Runnable[S].StreamWith(ctx, initial, opts)`** (`pkg/graph`): the
  option-taking streaming variant, at parity with `InvokeWith`. It drives
  `Checkpointer`, `Hooks`, run/node `Timeout`, `MaxSteps`, and
  `InterruptBefore` gating, and recovers node panics into an `EventError`
  carrying a `*PanicError` instead of crashing the process. `Stream` now
  routes through it; interrupts surface as a terminal `EventError`
  wrapping `ErrInterrupted`.
- **`graph.Cloner[S]` interface** (`pkg/graph`): lets a state type provide
  a precise deep copy for checkpointing. `MemoryCheckpointer` uses it when
  the state implements it, falling back to a gob round-trip.
- **Bedrock `user_id` metadata**: the Bedrock adapter now forwards
  `Request.Metadata["user_id"]` via Converse `RequestMetadata`, matching
  the Anthropic and OpenAI adapters.

### Changed
- **Honest capabilities** ⚠️: Bedrock now reports `StructuredOutput: false`
  and `PromptCaching: false`, and Google reports `PromptCaching: false` —
  those adapters do not yet wire `Request.ResponseFormat` /
  `schema.CacheControl`, and reporting the features as available silently
  produced free-form text or uncached prompts. `Capabilities.ValidateRequest`
  now also enforces the `CacheControl` vs `PromptCaching` check its doc
  already promised.
- **Retry caps and determinism** ⚠️ (`pkg/provider`): `MaxDelay` is now a
  hard ceiling applied *after* jitter, and it also bounds a server's
  `Retry-After` (a hostile `Retry-After: 86400` no longer sleeps for a
  day). A *negative* `Jitter` disables jitter for deterministic backoff;
  the zero value still means the 0.25 default, so existing callers are
  unchanged.
- **Bedrock honors `Retry-After`** ⚠️: throttling / quota errors now
  surface the HTTP `Retry-After` header on `APIError.RetryAfter`, so the
  retry wrapper uses the server-suggested backoff (parity with the HTTP
  adapters).
- **Consistent vector results** ⚠️: the pgvector and qdrant backends now
  drop anti-correlated (negative-cosine) chunks, matching the sqlite and
  in-memory backends — `Retrieve` returns the same result *set* across all
  four. Verified against live Postgres + pgvector and Qdrant.
- **Immutable checkpoints** ⚠️ (`pkg/graph`): `MemoryCheckpointer.Save`
  now deep-copies state so a later node mutating shared slices or maps can
  no longer corrupt an already-saved checkpoint. The `Checkpointer`
  interface documents this immutability contract.
- **Anthropic stream truncation** ⚠️: the Anthropic adapter now synthesizes
  a terminal `EventMessageStop` (carrying the accumulated usage and model)
  when the connection drops before `message_stop`, matching the other
  three adapters.

### Fixed
- **`schema.ParseJSON[T]`** now tolerates trailing prose after the JSON
  value (e.g. `{"a":1}\n\nHope that helps!`), not only leading prose, as
  the doc already claimed.
- **FTS5 lexical search** (`memory/sqlite`) no longer raises a syntax
  error on queries containing `AND` / `OR` / `NOT` or operator characters;
  each token is quoted as an FTS5 string literal.
- **JSON Schema generation** (`internal/jsonschema`): embedded
  pointer-to-struct fields are now promoted like `encoding/json` does
  (previously they leaked as a spurious property and, under
  `additionalProperties:false`, made the schema reject the model's correct
  output); `map[string]T` now emits the value schema instead of discarding
  it.
- **InMemoryStore ranking** (`pkg/memory`): a vector query no longer mixes
  cosine and lexical scores in one ranked list — chunks without an
  embedding are skipped rather than scored on an incomparable scale.
- **`graph.MergeHooks`** no longer mutates the caller's backing array when
  spread from a slice; `runLoop`'s per-node timeout no longer accumulates
  timers across a long run.
- **Bedrock `ToolChoiceNone`** keeps the tool *definitions* declared (only
  the choice is omitted) instead of dropping the entire tool config, which
  could invalidate a follow-up turn carrying prior `tool_result` blocks.

## [0.2.0] - 2026-05-23

Phase 11 of the roadmap — direct-caller ergonomics, driven by the first
integrator report (Telegram interpreter migration from LangChain).
The non-agent `Provider.Generate` path is now as ergonomic as the
agent loop: typed errors, tolerant JSON parsing, importable test
provider, surfaced retry policy, and a first-class docs page.

### Added
- **Typed error wrappers** (`pkg/provider`): `*RateLimitError`,
  `*AuthError`, `*InvalidRequestError`, `*TransientError`,
  `*ContextLengthError`, `*UnsupportedError`. All embed `*APIError`
  and support `errors.As` for idiomatic Go classification. Adapters
  emit them via `provider.Classify`. Backward compatible with
  existing `errors.Is(err, ErrRateLimited)` and `errors.As(err,
  &apiErr)` patterns via the unwrap chain. See ADR-012.
- **`schema.ParseJSON[T any]`** (`pkg/schema`): tolerant LLM JSON
  parser. Strips Markdown code fences, extracts JSON from
  surrounding prose, returns `*schema.BadOutputError` with capped
  raw input on failure. Stdlib-only; no LLM-driven repair. See
  ADR-011.
- **`schema.BadOutputError`** (`pkg/schema`): non-transport content
  failures, shared by ParseJSON today and JSONOf[T] in Phase 12.
- **`pkg/testprovider`**: scripted in-process `provider.Provider`
  for unit tests. `New`, `Responses`, `JSONResponses`, `Errors`,
  `Name`, `Capabilities` options; `Requests()`, `Reset()`,
  `Remaining()` introspection; goroutine-safe; `ErrScriptExhausted`
  on overrun.
- **`provider.WithDefaultRetry(inner)`**: one-line constructor for
  the common "sensible 429/5xx retry" case.
- **`provider.RetryPolicy`**: Go type alias for `RetryConfig` so
  both names refer to the same struct.
- **`docs/patterns/direct-provider.md`**: end-to-end guide for the
  one-prompt-one-response case with copy-paste skeleton, full typed-
  error catalog, retry composition, observability wiring, and
  testing patterns. Linked from `docs/README.md` ahead of RAG.

### Changed
- All four adapters (`providers/{anthropic,openai,google,bedrock}`)
  now return typed error wrappers via `provider.Classify` at every
  failure boundary. No caller-facing breakage.
- `pkg/provider/doc.go` rewritten with Errors and Retry sections so
  the package's godoc.org / pkg.go.dev landing answers \"how do I
  classify errors\" and \"how do I retry on 429\" without scrolling.

### Acceptance principle hits
- Rejected: a `Retry` field on every adapter's Config struct.
  Composition via decorator stays the canonical pattern.
- Rejected: re-prompt-on-failure inside `ParseJSON[T]`. Failure is
  signal; recovery via LLM call belongs in caller code.

## [0.1.1] - 2026-05-23

### Documentation
- README refreshed: roadmap shipped, explicit call for integrators.
- ROADMAP extended with Post-v1.0 phases (11–14), driven by the first
  integrator report from a real LangChain → galdor migration.

### Build
- Pin `require` directives across submodules to root `v0.1.0` so
  downstream installations resolve cleanly without local `replace`
  directives. Affects `examples/`, `providerset/`, `providers/*`,
  `memory/*`.

## [0.1.0] - 2026-05-19

First tagged release. Delivers Phases 0–10 of the roadmap, including:

- Provider abstraction with Anthropic, OpenAI, Google, and AWS Bedrock
  adapters; streaming, tool-calling normalization.
- Generics-based tool system (`pkg/tool`) and JSON Schema generation
  from Go structs.
- Graph runtime (`pkg/graph`) with conditional edges, checkpointing,
  interrupt/resume, and streaming events.
- Agent helpers: ReAct and Plan-and-Execute (`pkg/agent`).
- OpenTelemetry-native observability with embedded SQLite span store
  and `galdor scry` CLI.
- Self-hosted web UI (`galdor ui`) with run list, span tree, timeline,
  SSE live updates, and graph viewer.
- Memory & RAG: in-memory, SQLite, pgvector, and Qdrant backends;
  short-term `Window` with summarization; provider-backed embedders.
- Multi-agent: Supervisor + Swarm patterns (`pkg/council`); MCP client
  and server; A2A protocol.
- Eval framework with LLM-as-judge, Go scorers, versioned datasets,
  and CI integration.
- Replay engine and time-travel UI for reproducible debugging.
- Production hardening: retry/backoff, timeouts, panic recovery,
  structured logging, goroutine-leak audit, capability validation.

See [ROADMAP.md](ROADMAP.md) for the full surface delivered.

[Unreleased]: https://github.com/YasserCR/galdor/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/YasserCR/galdor/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/YasserCR/galdor/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/YasserCR/galdor/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/YasserCR/galdor/releases/tag/v0.1.0
