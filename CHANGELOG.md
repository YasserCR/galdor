# Changelog

All notable changes to galdor are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Pre-1.0 conventions: minor versions (v0.x.0) may introduce breaking changes;
patch versions (v0.x.y) are reserved for backward-compatible fixes and release
hygiene (docs, build metadata).

## [Unreleased]

## [0.4.1] - 2026-05-30

Hygiene-only patch. No code change.

### Build
- Pin submodule `require` directives to root `v0.4.1` across the workspace
  (`providers/*`, `memory/*`, `providerset`, `examples`), bumping them from
  the stale `v0.3.1` floor left after the v0.4.0 release. Pure go.mod edit —
  no go.sum churn (the local `replace => ../..` directives keep workspace
  builds resolving from source) and no code change. Minimal version
  selection already resolves the root to the newest required version for any
  consumer that depends on it directly; this keeps the submodules' own floor
  consistent with the release.

## [0.4.0] - 2026-05-30

Pre-launch hardening + dashboard redesign. An adversarial audit of the
surface not covered by v0.3.0 (MCP, A2A, multi-agent council, eval,
replay, web UI/CLI) plus a full redesign of the embedded dashboard.
Every bug fix ships a regression test; the release is green under
`go test -race`, golangci-lint v2.12.2, govulncheck and gosec across all
nine modules.

### Added
- **Embedded dashboard redesign** (`internal/ui`): a light, professional
  "Studio" theme (neutral slate + a single indigo accent) replacing the
  dark/neon look; an **interactive execution graph** whose nodes link to
  their step and show duration/status on hover (`graph.NodeAnnotation` +
  `Spec.RenderSVGAnnotated`); the `/graph` viewer now auto-loads the
  selected run's recorded topology (run dropdown, `?run=<id>`); and a
  richer timeline with a top time axis, gridlines, per-bar durations and
  depth-indented span names.
- **`graph.NodeAnnotation` + `Spec.RenderSVGAnnotated`** (`pkg/graph`):
  additive; `RenderSVG` is unchanged.
- **`council.ErrMaxHopsExceeded`, `council.ErrUnknownHandoffTarget`**: a
  Supervisor/Swarm run that is capped or misrouted now returns a
  detectable sentinel instead of a silent empty result.
- **`replay.ErrNilResponse`**: replaying a recorded nil response returns
  a descriptive error instead of `(nil, nil)`.

### Changed
- ⚠️ **Replay fixture format bumped to v2** (`replay.CurrentFixtureVersion`):
  the request fingerprint now folds in `Tools` and `ToolChoice` (and the
  model), so changing a run's available tools correctly invalidates a
  recorded answer. v1 fixtures are rejected and must be re-recorded.
- ⚠️ **Swarm handoffs are enforced at runtime**: an agent can only hand
  off to a target it declared in `Handoffs`; an undeclared `handoff_to_*`
  is rejected (returned to the model as an error result) instead of
  silently transferring control.
- ⚠️ **A2A client defaults are stricter**: cross-host redirects are
  rejected on card discovery (SSRF) and response bodies are size-capped
  (OOM); override via `WithHTTPClient` if you need the old behavior.

### Fixed
- **MCP** (`pkg/mcp`): a panicking tool no longer crashes the server
  process (recovered into a JSON-RPC error); the StreamableHTTP transport
  correlates replies by JSON-RPC id (no more dropped/cross-talked replies
  under concurrent same-session requests); the client no longer leaks its
  dispatch goroutine on `Close`; inbound messages are size-capped; the
  initialize handshake echoes the client's protocol version.
- **A2A** (`pkg/a2a`): a client-controlled `*Task` is now guarded by a
  per-task mutex (fixes a remote-triggerable data race / crash).
- **Eval** (`pkg/eval`): the shared `*Regex` scorer is compiled under a
  `sync.Once` (fixes a `-race` failure); the LLM-judge score parser no
  longer mis-reads numbers embedded in prose (no false PASS/FAIL); the
  runner honors context cancellation and recovers panicking
  Subjects/Scorers (one bad case errors instead of aborting the batch);
  `ExactMatch` no longer passes on empty expected-vs-actual; duplicate
  scorer names are rejected at setup.
- **Replay** (`pkg/replay`): lenient mode serves same-prompt calls in
  recorded order instead of collapsing to the last; cloned responses are
  deep-copied (nested image bytes / tool-call arguments no longer alias
  the recording).
- **UI/CLI**: `/api/graph/svg` caps request body and node count; the SSE
  endpoint clamps a minimum poll interval; `galdor ui` warns when binding
  to a non-loopback address (no auth); the `scry replay` CLI no longer
  panics on a short fingerprint. Also fixes the graph SVG node-fill bug
  (`fill="%q"` rendered empty).

## [0.3.1] - 2026-05-30

Hygiene-only patch. No code change.

### Build
- Pin submodule `require` directives to root `v0.3.1` across the
  workspace (`providers/*`, `memory/*`, `providerset`, `examples`),
  bumping them from the stale `v0.1.0` floor. Pure go.mod edit — no
  go.sum churn (the local `replace => ../..` directives keep workspace
  builds resolving from source) and no code change. Minimal version
  selection already resolves the root to the newest required version for
  any consumer that depends on it directly; this keeps the submodules'
  own floor consistent with the release instead of trailing at v0.1.0.

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

[Unreleased]: https://github.com/YasserCR/galdor/compare/v0.4.1...HEAD
[0.4.1]: https://github.com/YasserCR/galdor/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/YasserCR/galdor/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/YasserCR/galdor/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/YasserCR/galdor/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/YasserCR/galdor/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/YasserCR/galdor/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/YasserCR/galdor/releases/tag/v0.1.0
