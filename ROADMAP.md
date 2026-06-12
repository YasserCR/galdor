# galdor Roadmap

Phases are designed so that each one delivers value on its own. If the project pauses at any phase, what shipped is still useful.

Each item below is tracked against the ADRs in [`docs/adr/`](docs/adr/) for context and rationale.

## Phase 0 — Foundations

- [x] Public repo, Apache 2.0 LICENSE
- [x] OSS metadata (README, CONTRIBUTING, GOVERNANCE, CODE_OF_CONDUCT, NOTICE, DCO.txt)
- [x] Module structure scaffold (`cmd/`, `pkg/`, `internal/`, `docs/`)
- [x] CI: `go test -race`, `go vet`, `golangci-lint`
- [x] DCO enforcement workflow
- [x] ADR-001 with foundational decisions
- [x] Landing page at `galdor.org` (deployed via Cloudflare Pages)
- [ ] Branch protection on `main` (deferred — single maintainer for now)

**Outcome:** `go get` works, the module is importable, the repo passes CI clean.

## Phase 1 — Provider Layer

- [x] `Provider` interface and `Request` / `Response` / shared `schema.Message` types
- [x] Streaming primitives (`StreamReader`, `Event`, `CollectStream`)
- [x] Anthropic adapter (reference) — `providers/anthropic`
- [x] OpenAI adapter — `providers/openai` (also targets Groq, Together, MiniMax, Mistral, ... via `BaseURL`)
- [x] Google Gemini adapter — `providers/google` (AI Studio surface; Vertex AI via `BaseURL` + custom `HTTPClient`)
- [x] AWS Bedrock adapter — `providers/bedrock` (Converse API; uses AWS SDK Go v2 for SigV4 and Event Stream framing; **compatibility validation against a live AWS account pending**, see README status)
- [x] Tool-calling normalization across providers (`provider.ValidateToolCalls`, documented contract in `pkg/provider/toolcalls.go`)
- [x] Unit tests with recorded HTTP fixtures (httptest)
- [x] Opt-in integration tests against the real API (gated by `integration` tag + per-provider env var)

**Outcome:** Drop-in SDK that already supersedes most ad-hoc wrappers.

## Phase 2 — Tools + Schema

- [x] Generics-based tool system (`pkg/tool` — `Tool[In, Out]`, `AnyTool`, `NewTool`, `MustNewTool`)
- [x] JSON Schema generation from Go structs (`internal/jsonschema`)
- [x] Concurrent tool-call execution (`tool.ExecuteCalls`, preserves order, propagates cancellation)
- [x] Tool registry + `schema.ToolDef` conversion (`tool.Registry`)
- [x] End-to-end LLM ↔ tools loop example (`examples/tools-loop`)
- [x] Built-in tools (`pkg/tool/builtins`): `time` (now/parse/format), `math` (add/sub/mul/div/mod/pow/sqrt/abs/ln/log10/exp), `http_get` (allowlist + size cap + timeout), `file_read` (BaseDir confinement + size cap + symlink gate). Shell deferred to ADR-008 sandboxing policy.

**Outcome:** Basic LLM ↔ tools loop working.

## Phase 3 — Graph Runtime

- [x] `Graph[S]`, `NodeFunc[S]`, `Edge`, `Runnable[S]` — `pkg/graph`
- [x] Conditional edges via `Router[S]`
- [x] Streaming event channel (`Runnable.Stream`, typed `Event[S]`)
- [x] `START` / `END` sentinels, validation at `Compile()`, max-step guard
- [x] Checkpointer interface + `MemoryCheckpointer` (history-preserving in-process impl)
- [x] Interrupt / resume via `InterruptBefore` + `Resume` with `OverrideState` for human-in-the-loop edits
- [x] ReAct helper (`pkg/agent.NewReAct`, `agent.Run` one-shot wrapper) — composes provider + tools + graph
- [x] CLI: `galdor cast <agent.yaml> "<input>"` — run a ReAct agent from YAML, with `--trace` recording provider/tool/node spans to the store (v0.12.0)
- [x] Plan-and-Execute helper (`pkg/agent.NewPlanAndExecute` — planner → execute → replan loop with JSON-mode prompts, fence-tolerant parser, max-iter cap)

**Outcome:** First real agent. Feature parity with basic LangGraph.

## Phase 4 — Observability Core

- [x] OTel instrumentation in provider, tool, graph (`pkg/observability`: `InstrumentProvider` / `InstrumentTool` / `InstrumentRegistry` / `TraceHooks[S]`); GenAI semantic conventions honored
- [x] `graph.Hooks[S]` lifecycle extension point in `pkg/graph` (BeforeRun / AfterRun / BeforeNode / AfterNode)
- [x] Embedded storage (SQLite via `modernc.org/sqlite`) — `internal/store` + `observability.SQLiteExporter`
- [x] CLI `galdor scry list` / `galdor scry show <run-id>` over the SQLite store (text + JSON output)
- [x] Latency (p50/p95/p99) and token metrics — `galdor scry stats [--by overall|provider|model]` (cost tracking needs a per-model price table; tracked for future session)
- [x] `galdor scry tail` live-follow mode (text + json output, configurable poll interval)
- [x] Prompt registry — `pkg/spellbook` (versioned file-backed templates, `Render` via text/template) + CLI `galdor spellbook list|show|diff|render` + agent-block `system_spell` integration (v0.13.0)

**Outcome:** First-class agent debugging from the CLI.

## Phase 5 — Web UI

- [x] Embedded HTTP server (`galdor ui`, loopback by default)
- [x] Run list page + per-run span tree (server-rendered HTML)
- [x] Per-span detail page with attribute table, events, and prompt/completion view
- [x] Clickable span rows in the tree
- [x] Opt-in content capture (`observability.WithCaptureContent`)
- [x] JSON API mirror (`/api/runs`, `/api/runs/{id}/spans`, `/api/runs/{id}/spans/{spanID}`)
- [x] CSS + templates embedded via `embed.FS` — no external assets
- [x] SVG timeline (Gantt-style) on the run detail page
- [x] Live updates via SSE (`/api/stream/runs`) + vanilla JS row upsert
- [x] Workflow graph (static DAG of `Graph[S]`): `Runnable.Inspect()` returns a serializable `graph.Spec`; `Spec.RenderSVG` produces a self-contained layered SVG; UI exposes `/graph` viewer + `POST /api/graph/svg`; CLI `galdor weave <run-id>` renders/validates a recorded run's topology (v0.10.0)

**Outcome:** Self-hosted "Langsmith-local" working.

## Phase 6 — Memory + RAG

- [x] Memory interfaces (short-term and long-term) — `pkg/memory.Store`, `Embedder`, `Retriever`, `Document`, `Chunk`, `Query`, `Result`
- [x] Short-term `Window` with message-count and token-budget caps, optional `Summarizer` for evicted turns
- [x] In-memory `Store` (vector cosine + lexical substring, metadata filtering) for tests and getting-started
- [x] Chunking helpers (`pkg/memory/chunk`: `FixedSize`, `Recursive`, `Sentence`)
- [x] Embedded backend (SQLite + BM25) — `memory/sqlite` module, FTS5 lexical + brute-force cosine vector, persistent across reopen
- [x] `HashingEmbedder` (offline, deterministic) + `EmbedderFunc` adapter for wiring real provider embedders
- [x] End-to-end RAG example (`examples/memory-rag`): chunk → embed → SQLite store → retrieve → scripted answer
- [x] `memory/pgvector` adapter (pgx/v5, cosine distance via `<=>`, JSONB metadata filtering, opt-in integration tests gated by `GALDOR_PGVECTOR_URL`)
- [x] `memory/qdrant` adapter (HTTP REST client, idempotent collection bootstrap, opt-in integration tests gated by `GALDOR_QDRANT_URL`)
- [x] Provider-backed embedder adapters: OpenAI (`providers/openai.NewEmbedder` — also covers Mistral, MiniMax, Together, Groq via `BaseURL`) and Google (`providers/google.NewEmbedder` — text-embedding-004, Gemini embedding models; Vertex AI via custom `HTTPClient`). Cohere/Voyage need a new provider module first; tracked separately.

**Outcome:** End-to-end RAG without external services.

## Phase 7 — Multi-Agent + MCP

- [x] Supervisor pattern — `council.NewSupervisor` (router LLM + worker fns + graph-based routing loop)
- [x] Swarm pattern — `council.NewSwarm` (per-agent ReAct + synthetic `handoff_to_<name>` tools + shared conversation)
- [x] CLI: `galdor council <topology.yaml>` — supervisor/swarm from YAML (workers = agent blocks) (v0.12.0)
- [x] MCP client — `pkg/mcp.Client` (initialize + tools/list + tools/call, stdio transport, `Client.AsRegistry` adapts remote tools to galdor `tool.AnyTool`)
- [x] MCP server — `pkg/mcp.Server` (wraps any `tool.Registry`, exposes it over MCP for Claude Desktop / IDE plugins, optional Strict mode)
- [x] MCP client transport over Streamable HTTP — `pkg/mcp.NewStreamableHTTPClientTransport` (request/response dialer; stdio + Streamable HTTP cover the spec's forward path) (v0.10.0)
- [x] CLI: `galdor mcp serve` (builtins over stdio/Streamable HTTP, guard-gated file_read/http_get) + `galdor mcp ls`/`call` (debugging client over a URL or `-- <command>`) (v0.10.0)
- [x] A2A protocol — `pkg/a2a` (Agent Card publishing + discovery, `tasks/send`, `tasks/get`, multi-turn input-required state, server is an `http.Handler` wrapping any `Handler` function)

**Outcome:** Multi-agent systems and ecosystem interop.

## Phase 8 — Eval Framework

- [x] LLM-as-judge primitives — `eval.LLMJudge` (provider-agnostic, configurable rubric, tolerant integer parsing, threshold-based pass)
- [x] Custom Go scorers — `eval.ScorerFunc` adapter + built-ins (`ExactMatch`, `Contains`, `Regex`)
- [x] Versioned datasets — `eval.Dataset{Name, Version, Cases}` with JSON loader/saver, dup-ID validation
- [x] CI integration with exit codes — `eval.RunAndExit`, `Report.Meets(threshold)`, parallel runner with worker pool
- [x] `examples/eval-suite` — end-to-end offline demo using a scripted Subject + scripted LLM judge
- [x] CLI: `galdor trial <suite.yaml>` — declarative eval suites in YAML (subject = agent block, builtin scorers), CI exit codes; config format + module split in ADR-014, `examples/trial-suite` (v0.11.0)

**Outcome:** Inline regression testing for prompts and agents — from Go or from a YAML file in CI.

## Phase 9 — Replay + Time Travel

- [x] Replay engine — `pkg/replay` (strict + lenient matching, prompt fingerprinting, fixture JSON round-trip, deep-copies responses)
- [x] Provider mocks for deterministic tests — `replay.Provider` implements `provider.Provider`, plugs straight into `agent.Config.Tools` / any caller
- [x] CLI: `galdor scry replay <run-id>` — inspects + exports recordings, refuses with a clear message when content was not captured
- [x] Time-travel in the UI — `/runs/{runID}/steps` linearizes a run into ordered graph-node steps, unfolds provider prompts/completions + tool calls per step, and explicitly states whether the run is replayable

**Outcome:** Reproducible debugging — the feature nobody in Go has done well yet.

## Phase 10 — v1.0 *(in progress)*

Hardening (production polish):

- [x] Retry + exponential backoff per provider — `provider.Retry` wrapper (respects `APIError.RetryAfter`, classifies via `IsRetryable`, never retries auth/invalid-request/unsupported)
- [x] Per-run and per-node timeouts in `graph.RunOptions{Timeout, NodeTimeout}` — parent ctx still wins when it cancels first
- [x] Panic recovery in node bodies (`graph.PanicError` + `safeCallNode`), in hooks (each callback wrapped individually — instrumentation bugs never fail the run), and in `tool.ExecuteJSON` (`tool.PanicError`). Both packages expose an `ErrPanic` sentinel + `errors.As` shape.
- [x] Structured logging via `slog` — `RunOptions.Logger` receives operational events (panics recovered, hook panics suppressed). nil Logger is a silent no-op.
- [x] Goroutine leak audit — `go.uber.org/goleak.VerifyTestMain` wired into 5 packages with concurrent code (`pkg/graph`, `pkg/mcp`, `pkg/eval`, `pkg/tool`, `pkg/a2a`). No leaks found; gate prevents regressions.
- [x] Stricter boundary validation — capability-aware `agent.Config.validate` (Tools on non-tooling provider, `ForceToolUse` without Tools, negative `MaxIterations`) + reusable `provider.Capabilities.ValidateRequest` for any caller wanting an early sanity check.

Then:

- Complete integration examples (each in `examples/integration-*` with its own README):
  - [x] `integration-support-bot` — Supervisor + 3 specialist ReAct sub-agents with separate tool registries (billing / technical / general). Scripted Provider runs it offline; swap for Anthropic/OpenAI is a one-line change.
  - [x] `integration-approval-gate` — `InterruptBefore` + `MemoryCheckpointer` + `Resume` with `OverrideState`. Three scenarios: low-risk auto-approve, high-risk dual signoff, over-cap rejection.
  - [x] `integration-mcp-server` — wraps a `tool.Registry` as an MCP server over stdio; four tools (time / math + custom lookup_doc / open_ticket); README ships the Claude Desktop config snippet.
  - [x] `integration-cost-tracked` — `BudgetProvider` middleware that wraps any provider with atomic token accounting, per-model pricing table, two-point check (pre-call refuse + post-call fail-on-overshoot).
- [x] Full docs (quickstart, conceptual guides per package, pattern recipes, migration guides, ops guide) — shipped under `docs/` (concept guides, pattern recipes, migration guides, `docs/ops.md`).
- [x] Benchmarks — five hot paths covered (`pkg/graph` runtime, `pkg/observability` span recording, `internal/store` throughput, `pkg/memory` retrieval, `pkg/tool` dispatch). Results + sizing guidance in [`docs/benchmarks.md`](docs/benchmarks.md). Headline: galdor's own overhead is 3-5 orders of magnitude smaller than a typical LLM call.
- [x] Security audit (self-review) — `govulncheck` + `gosec` wired into CI across all 8 modules, toolchain pinned to `go 1.25.10` (closes 21 stdlib CVEs), pgx bumped to v5.9.2 (closes 2 module CVEs), 9 `gosec` findings triaged + suppressed with explicit `// #nosec Gxxx -- reason` annotations. Full writeup + OWASP LLM Top 10 self-assessment in [`docs/security.md`](docs/security.md). Third-party audit deferred to post-v1.0.
- [ ] Public launch (HN, r/golang, GopherCon CFP)

**Total estimate:** ~8 months at a focused pace.

---

# Post-v1.0 — Integrator-Driven

Phases 0–10 were built top-down: what a framework needs to be credible. What
follows is built bottom-up, from concrete reports of real migrations onto
galdor. Each item links to the feedback that drove it.

Phases remain independently shippable. Within a cycle, priority is set by
(1) items unblocking another phase, (2) items cited by the most-recent
integrator report, (3) smallest "LoC deleted in integrator repos / LoC added
in galdor" ratio.

### Acceptance principle

Items in this section come from real migrations and are accepted only when
they **reinforce galdor's Go-native posture** — leaning on generics,
`context.Context`, `errors.As`, struct tags, composition via decorators, and
a single binary. Suggestions that would import another framework's shape —
even when popular or familiar — are recorded under Non-Goals, not adopted out
of sympathy. The migration guide reads as "what you stop doing in Go", not
"the equivalent abstraction for X".

When in doubt: if a proposed item could only be expressed as "the Go version
of <other framework>'s <feature>", it does not belong here. If it could be
expressed as "the natural Go shape for this problem, which happens to remove
boilerplate integrators write today", it does.

## Phase 11 — Direct-Caller Ergonomics *(shipped in v0.2.0)*

The non-agent case (classify / extract / translate / NL-to-DSL) is a large
share of production usage. v1.0 leads with `agent.Run()`; this phase makes the
one-shot `Provider.Generate` path equally first-class.

- [x] `schema.ParseJSON[T any]` — fence-stripping, prose-tolerant, typed error
      on failure. Stdlib-only, no LLM re-prompt magic. (ADR-011)
- [x] Typed errors from `Provider.Generate` — `RateLimitError{RetryAfter}`,
      `AuthError`, `TransientError`, `BadOutputError`, `ContextLengthError`,
      all `errors.As`-friendly. Existing `APIError` keeps working. (ADR-012)
- [x] `provider.RetryPolicy` — type alias for the v0.1 `RetryConfig` plus
      `provider.WithDefaultRetry(inner)` one-liner. Rejected: a `Retry` field
      on every adapter's Config (would duplicate the option across packages
      and hide the decorator pattern).
- [x] `docs/patterns/direct-provider.md` — copy-paste-runnable skeleton +
      full typed-error catalog + retry composition + observability wiring +
      testing patterns.
- [x] `pkg/testprovider` — scripted in-process Provider for unit tests:
      `testprovider.New(testprovider.Responses(...), testprovider.JSONResponses(...), testprovider.Errors(...))`.

**Outcome:** A direct-`Generate` user can ship a production interpreter without
opening any provider's source code.

**Driven by:** integrator report §2, §3, §4, §5, §10.

## Phase 12 — Structured Output (Schema-Bound)

The biggest ergonomic gap when porting from LangChain. Providers (Gemini,
OpenAI, Anthropic tool-mode) all support real schema-bound responses; galdor
exposes `ResponseFormat` but no binding. This phase closes the loop.

- [x] `provider.JSONSchemaFor[T]` + `provider.GenerateStructured[T]` — derive
      a JSON Schema from a Go type, thread it through
      `provider.Request.ResponseFormat`, and decode the reply back into `T`
      (tolerant of code fences / prose via `schema.ParseJSON`) (v0.14.0)
- [x] Per-provider wiring — Google `response_schema`, OpenAI
      `response_format: json_schema`, Anthropic forced-tool with the schema.
      Capability gating (`StructuredOutput: true`) now means something. Bedrock
      left unsupported (fronts many model families) (v0.14.0)
- [x] Concept doc (provider) + `examples/structured-output` (v0.14.0)
- [ ] *(post-v1.0)* Refactor existing examples that ask for JSON in the
      prompt (e.g. plan-and-execute helper) to use structured output where
      the provider supports it. Backward compatible — text-mode JSON path
      stays.

**Outcome:** The fence-stripping regex and permissive structs that every
integrator currently writes simply disappear.

**Driven by:** integrator report §1.

## Phase 13 — Production Polish

Smaller items that compound. Each ships independently.

- [ ] *(post-v1.0)* `pkg/pricing` — embedded per-model price table,
      `pricing.For(model).Cost(usage)`. Override-friendly. Documented refresh
      process (single file, PRs welcome). `observability.InstrumentProvider`
      decorates spans with `cost.usd` when the model is known.
- [ ] *(post-v1.0, revisit)* `schema.Template` — a minimal `text/template`
      wrapper for prompt variables. Largely covered today by
      `spellbook.Spell.Render`; decide whether a schema-level wrapper still
      adds value before building it.
- [ ] *(post-v1.0)* Granular content capture —
      `observability.WithCapturePrompt(bool)` and `WithCaptureResponse(bool)`
      separately. `WithRedactor(func(string) string)` runs before persisting
      to spans. Existing `WithCaptureContent` stays as a shortcut for both.
- [x] `CHANGELOG.md` + tagged GitHub releases for every minor.
      Hand-curated `CHANGELOG.md` shipped; releases tagged through v0.9.0.
      (Release automation à la release-please still optional.)
- [x] Doc additions: `$GOBIN`-on-`PATH` note in the README install snippet
      (with a pointer to `galdor doctor`); span-nesting paragraph in
      `docs/concepts/observability.md` (galdor spans nest under any
      caller-provided parent span via standard context propagation).

**Outcome:** Removes the most-cited friction points from real integrations
without expanding core surface area.

**Driven by:** integrator report §6, §7, §8, §11, §12, §13.

## Phase 14 — Ecosystem & Adoption

Materials that make the second, third, and tenth integration cheaper than the
first — without adding framework surface area.

- [x] `examples/integration-http-interpret` — full HTTP service: provider +
      structured output + observability + graceful shutdown + health endpoint.
      Copy-paste starter, not a `pkg/serve` abstraction. (v0.15.0)
- [x] `docs/migration/from-langchain-python.md` — concrete mappings
      (`JsonOutputParser` → `schema.ParseJSON`, LCEL pipe → "write a Go
      function", `RunnableWithFallbacks` → `provider.RetryPolicy` + typed
      errors).
- [x] Public integrator-feedback intake — `docs/feedback/` + GitHub issue
      templates (bug / feature / integration-feedback) (v0.15.0)
- [x] `galdor doctor` CLI — checks the Go toolchain, whether go-installed
      binaries land on PATH, provider credential env vars, and trace-store
      reachability (v0.15.0)

**Outcome:** Adoption cost halves between integration #1 and integration #N
because the cliffs the first one hit are paved.

**Driven by:** integrator report §3 (docs framing), §9 (resist `pkg/serve`),
"What NOT to Add" section.

---

## Explicit Non-Goals (carried forward)

These remain off the table, regardless of integrator pull:

- **Declarative chains** (`prompt | llm | parser`). Plain Go functions are the
  composition primitive.
- **A zoo of overlapping abstractions** (`Runnable`, `Chain`, `Agent`, `Tool`,
  `Memory` where the same task expresses four ways). Current core size is the
  ceiling, not the floor.
- **Vector stores or document loaders in core.** Stay in `memory/<backend>`
  modules, the way provider adapters are isolated.
- **`pkg/serve` HTTP framework helper.** Ship the example, not the
  abstraction. Revisit only after ≥3 integrations converge on the same shape.
  (ADR-013 D1.)
- **Project scaffolding** (`galdor forge` or equivalent). Templates freeze a
  moving pre-v1.0 API and rot every release — the pattern the ecosystem
  retired (create-react-app deprecated 2025, Buffalo archived 2024,
  LangChain templates). The supported path is Go's own `gonew` over
  `examples/` — thirteen compiler-verified living templates:
  `gonew github.com/YasserCR/galdor/examples/agent-react your.module/app`.
  (ADR-013 D2.)
