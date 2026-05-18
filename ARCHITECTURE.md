# galdor Architecture

This document is a working overview of the galdor architecture. Authoritative decisions live in [`docs/adr/`](docs/adr/); this file summarizes them and is updated as new decisions land.

## Layered view

```
+-------------------------------------------------------------+
|  CLI + Web UI (observability dashboard, agent playground)   |
+-------------------------------------------------------------+
|  Eval Framework  |  Replay Engine  |  Prompt Registry       |
+-------------------------------------------------------------+
|  Agent Runtime (graph executor with goroutines + channels)  |
+-------------------------------------------------------------+
|  Tools  |  Memory  |  RAG/Retrieval  |  Multi-Agent (A2A)   |
+-------------------------------------------------------------+
|  Provider Abstraction (Anthropic, OpenAI, Google, Ollama)   |
+-------------------------------------------------------------+
|  Observability Core (OTel-native, embedded backend)         |
+-------------------------------------------------------------+
|  Storage (in-memory, embedded SQLite, optional Postgres)    |
+-------------------------------------------------------------+
```

## Module map

| Path | Purpose | Phase |
|------|---------|-------|
| `cmd/galdor/` | Single binary CLI (`galdor cast`, `scry`, `weave`, ...) | 0+ |
| `pkg/provider/` | `Provider` interface and shared types | 1 |
| `pkg/tool/` | Type-safe tool system with generics | 2 |
| `pkg/graph/` | Generic graph runtime over goroutines/channels | 3 |
| `pkg/agent/` | High-level agent helpers (ReAct, Plan-and-Execute, ...) | 3 |
| `pkg/observability/` | OTel tracing, eval framework, replay engine | 4 |
| `pkg/memory/` | Short- and long-term memory interfaces | 6 |
| `pkg/council/` | Multi-agent orchestration (themed high-level) | 7 |
| `pkg/spellbook/` | Prompt registry (themed high-level) | 4+ |
| `pkg/mcp/` | Model Context Protocol client and server | 7 |
| `pkg/schema/` | Shared types: Message, Run, Span, etc. | 1 |
| `internal/store/` | Storage adapters (SQLite, Postgres) | 4 |
| `internal/ui/` | Embedded web UI assets | 5 |
| `internal/jsonschema/` | Reflection-based JSON Schema generation | 2 |
| `providers/<name>/` | Per-provider Go module (Anthropic, OpenAI, ...) | 1+ |
| `memory/<backend>/` | Per-backend Go module (pgvector, qdrant, ...) | 6 |
| `examples/<name>/` | Runnable examples doubling as integration tests | each phase |
| `e2e/` | Opt-in end-to-end tests against real providers | 1+ |

## Design invariants

1. **One binary by default.** The framework, the observability UI and the MCP server all run in the same process when desired. External stores are optional.
2. **Context-first.** Every operation that can block accepts a `context.Context`. Cancellation propagates everywhere.
3. **Streaming is the default.** Any LLM call can be consumed as a stream without changing the call site.
4. **Type-safe over stringly-typed.** Generics where it pays off. Reflection only at edges (schema generation, decoding).
5. **Errors as values.** No `panic` outside `init`. No `Must*` in hot paths.
6. **Determinism where possible, reproducibility where not.** Replay reproduces a run given the same seed and mocks.
7. **Provider details never leak.** Switching providers is a configuration line, not a code change.

## Boundaries

- `pkg/` is the public API. Anything here is subject to SemVer once v1.0 ships.
- `internal/` is implementation. It can change without notice.
- `providers/<name>/` and `memory/<backend>/` are independent Go modules so the core stays dependency-light. A user only pulls the adapters they need.

## Observability — the differentiator

galdor's observability stack lives in the same binary:

- **Tracer:** OTel-native spans for every LLM call, tool, node and edge.
- **Embedded backend:** spans written to a local SQLite store by default. Postgres or ClickHouse can be plugged in for scale.
- **Web UI:** served by the binary itself on a local port (default `:6006`), rendering runs, span trees, input/output diffs and graph visualizations.
- **Replay engine:** any run can be reconstructed from its spans and re-executed, optionally with provider mocks.
- **Eval framework:** LLM-as-judge, custom scorers and regression datasets, with CI-friendly exit codes.

## Open ADRs

- **ADR-001** — Foundational decisions (this commit).
- **ADR-002** — Cancellation model for partially executed graphs.
- **ADR-003** — Retry and backoff policy per provider.
- **ADR-004** — Streaming event schema (alignment with OTel GenAI conventions).
- **ADR-005** — Checkpoint serialization format (JSON vs protobuf).
- **ADR-006** — Cross-provider prompt caching policy.
- **ADR-007** — Cost tracking model.
- **ADR-008** — Tool sandboxing and permissioning (shell, file system).

See [`docs/adr/`](docs/adr/) for the canonical records as they land.
