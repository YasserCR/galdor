# Changelog

All notable changes to galdor are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Pre-1.0 conventions: minor versions (v0.x.0) may introduce breaking changes;
patch versions (v0.x.y) are reserved for backward-compatible fixes and release
hygiene (docs, build metadata).

## [Unreleased]

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

[Unreleased]: https://github.com/YasserCR/galdor/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/YasserCR/galdor/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/YasserCR/galdor/releases/tag/v0.1.0
