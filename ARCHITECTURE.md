
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
|  Provider Abstraction (Anthropic, OpenAI, Google, Bedrock)  |
+-------------------------------------------------------------+
|  Observability Core (OTel-native, embedded backend)         |
+-------------------------------------------------------------+
|  Storage (in-memory, embedded SQLite; OTel export for scale)|
+-------------------------------------------------------------+
```

## Module map

| Path | Purpose | Phase |
|------|---------|-------|
| `cmd/galdor/` | Single binary CLI (`scry`, `ui`, `mcp`, `weave`, `trial`, `cast`, `council`, `spellbook`, `doctor`). **Its own Go module** so the config-driven verbs can depend on `providerset` without pulling the provider stack into the core (ADR-014 D3). | 0+ |
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
| `internal/store/` | Embedded trace/metric store (SQLite) | 4 |
| `internal/ui/` | Embedded web UI assets | 5 |
| `internal/jsonschema/` | Reflection-based JSON Schema generation | 2 |
| `providers/<name>/` | Per-provider Go module (Anthropic, OpenAI, ...) | 1+ |
| `memory/<backend>/` | Per-backend Go module (pgvector, qdrant, ...) | 6 |
| `examples/<name>/` | Runnable examples doubling as integration tests | each phase |
| `**/integration_test.go` | Opt-in (env-gated) tests against real providers/backends, per module | 1+ |

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
- **Embedded backend:** spans written to a local SQLite store by default. Because the tracer is OTel-native, spans can also be exported to any OTel-compatible collector for scale.
- **Web UI:** served by the binary itself on a local port (default `127.0.0.1:7777`), rendering runs, span trees, input/output diffs and graph visualizations.
- **Replay engine:** any run can be reconstructed from its spans and re-executed, optionally with provider mocks.
- **Eval framework:** LLM-as-judge, custom scorers and regression datasets, with CI-friendly exit codes.

## Architecture decision records

Fifteen ADRs are recorded and Accepted (ADR-001 … ADR-015), covering the
foundational decisions, the provider/tool/graph/agent shapes, observability
and the SQLite store, the Web UI, typed errors, the CLI surface, the
declarative config format and CLI module split, and schema-bound structured
output. See [`docs/adr/`](docs/adr/) for the canonical index and records.
