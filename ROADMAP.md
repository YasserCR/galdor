# galdor Roadmap

Phases are designed so that each one delivers value on its own. If the project pauses at any phase, what shipped is still useful.

Authoritative source for scope per phase: [`docs/PLAN.md`](docs/PLAN.md) §8.

## Phase 0 — Foundations *(current)*

- [x] Public repo, Apache 2.0 LICENSE
- [x] OSS metadata (README, CONTRIBUTING, GOVERNANCE, CODE_OF_CONDUCT, NOTICE, DCO.txt)
- [x] Module structure scaffold (`cmd/`, `pkg/`, `internal/`, `docs/`)
- [x] CI: `go test -race`, `go vet`, `golangci-lint`
- [x] DCO enforcement workflow
- [x] ADR-001 with foundational decisions
- [ ] Branch protection on `main` (manual GitHub setting)
- [ ] Landing page at `galdor.org`

**Outcome:** `go get` works, the module is importable, the repo passes CI clean.

## Phase 1 — Provider Layer

- [x] `Provider` interface and `Request` / `Response` / shared `schema.Message` types
- [x] Streaming primitives (`StreamReader`, `Event`, `CollectStream`)
- [x] Anthropic adapter (reference) — `providers/anthropic`
- [x] OpenAI adapter — `providers/openai` (also targets Groq, Together, MiniMax, Mistral, ... via `BaseURL`)
- [x] Google Gemini adapter — `providers/google` (AI Studio surface; Vertex AI via `BaseURL` + custom `HTTPClient`)
- [x] AWS Bedrock adapter — `providers/bedrock` (Converse API; uses AWS SDK Go v2 for SigV4 and Event Stream framing; **compatibility validation against a live AWS account pending**, see README status)
- [ ] Tool-calling normalization across providers
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
- [ ] Plan-and-Execute helper (`pkg/agent.NewPlanAndExecute` — follow-up)

**Outcome:** First real agent. Feature parity with basic LangGraph.

## Phase 4 — Observability Core

- [x] OTel instrumentation in provider, tool, graph (`pkg/observability`: `InstrumentProvider` / `InstrumentTool` / `InstrumentRegistry` / `TraceHooks[S]`); GenAI semantic conventions honored
- [x] `graph.Hooks[S]` lifecycle extension point in `pkg/graph` (BeforeRun / AfterRun / BeforeNode / AfterNode)
- [x] Embedded storage (SQLite via `modernc.org/sqlite`) — `internal/store` + `observability.SQLiteExporter`
- [x] CLI `galdor scry list` / `galdor scry show <run-id>` over the SQLite store (text + JSON output)
- [x] Latency (p50/p95/p99) and token metrics — `galdor scry stats [--by overall|provider|model]` (cost tracking needs a per-model price table; tracked for future session)
- [x] `galdor scry tail` live-follow mode (text + json output, configurable poll interval)

**Outcome:** First-class agent debugging from the CLI.

## Phase 5 — Web UI

- Embedded HTTP server
- Run list, span tree, input/output diff
- Live graph visualization
- All assets embedded into the binary

**Outcome:** Self-hosted "Langsmith-local" working.

## Phase 6 — Memory + RAG

- Memory interfaces (short-term and long-term)
- Embedded backend (SQLite + BM25 fallback)
- `pgvector` adapter
- `qdrant` adapter
- Chunking and embedding helpers

**Outcome:** End-to-end RAG without external services.

## Phase 7 — Multi-Agent + MCP

- Supervisor and swarm patterns
- A2A protocol implementation
- MCP client
- MCP server (expose a galdor agent as an MCP tool)

**Outcome:** Multi-agent systems and ecosystem interop.

## Phase 8 — Eval Framework

- LLM-as-judge primitives
- Custom Go scorers
- Versioned datasets
- CI integration with exit codes

**Outcome:** Inline regression testing for prompts and agents.

## Phase 9 — Replay + Time Travel

- Replay engine
- Provider mocks for deterministic tests
- Time-travel in the UI

**Outcome:** Reproducible debugging — the feature nobody in Go has done well yet.

## Phase 10 — v1.0

- Hardening, benchmarks, full docs
- Security audit
- Production examples
- Public launch (HN, r/golang, GopherCon CFP)

**Total estimate:** ~8 months at a focused pace.
