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
- [ ] Tool-calling normalization across providers
- [x] Unit tests with recorded HTTP fixtures (httptest)
- [x] Opt-in integration tests against the real API (gated by `integration` tag + per-provider env var)

**Outcome:** Drop-in SDK that already supersedes most ad-hoc wrappers.

## Phase 2 — Tools + Schema

- Generics-based tool system
- JSON Schema generation from Go structs
- Concurrent tool-call execution
- 5–10 built-in tools (http, file read, shell, math, time, ...)

**Outcome:** Basic LLM ↔ tools loop working.

## Phase 3 — Graph Runtime

- `Graph[S]`, `Node`, `Edge`, `Runnable[S]`
- Conditional edges
- Streaming event channel
- Checkpointer interface + in-memory implementation
- Interrupt / resume
- ReAct and Plan-and-Execute as helpers

**Outcome:** First real agent. Feature parity with basic LangGraph.

## Phase 4 — Observability Core

- OTel instrumentation in provider, tool, graph
- Embedded storage (SQLite)
- CLI `galdor scry` for trace exploration
- Latency, token and cost metrics

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
