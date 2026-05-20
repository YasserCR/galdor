# galdor docs

This is the user-facing documentation for galdor, a Go-native framework for building, orchestrating and observing AI agents. The reference README is at [`../README.md`](../README.md); these pages take you from install to production patterns.

If you have 15 minutes, start with the [quickstart](quickstart.md). After that, read the concept pages for the packages you need and the patterns for the workflows you want to copy.

## Get started

- [Quickstart](quickstart.md) — install galdor, run your first ReAct agent, attach a tool, view the trace in `galdor ui`.

## Concepts

One page per package. Each follows the same shape: what it's for, the core types, the things you do with it, gotchas, and links to ADRs and examples.

- [Provider](concepts/provider.md) — `pkg/provider`: the LLM abstraction, retries, capabilities, streaming.
- [Providerset](concepts/providerset.md) — `providerset`: pick a provider at runtime via env or `Config`, OpenAI-compatible aliases.
- [Schema](concepts/schema.md) — `pkg/schema`: `Message`, `ToolCall`, `ToolDef`, `StopReason`, the data model that flows between every layer.
- [Tool](concepts/tool.md) — `pkg/tool`: generic `Tool[In, Out]`, `Registry`, `ExecuteCalls`, builtins.
- [Graph](concepts/graph.md) — `pkg/graph`: `Graph[S]`, nodes, routers, checkpoints, interrupt/resume, hooks.
- [Agent](concepts/agent.md) — `pkg/agent`: `agent.Run`, `NewReAct`, `NewPlanAndExecute`.
- [Memory](concepts/memory.md) — `pkg/memory`: short-term windows, long-term stores, chunkers, retrievers.
- [Embedder](concepts/embedder.md) — `pkg/embedder`: generic HTTP client for self-hosted embedding servers (TEI, Infinity, vLLM-embeddings).
- [Observability](concepts/observability.md) — `pkg/observability`: OTel-native spans, SQLite store, `galdor ui`.
- [Council](concepts/council.md) — `pkg/council`: Supervisor and Swarm multi-agent patterns.
- [MCP](concepts/mcp.md) — `pkg/mcp`: Anthropic Model Context Protocol client and server.
- [A2A](concepts/a2a.md) — `pkg/a2a`: Google Agent-to-Agent protocol client and server.
- [Eval](concepts/eval.md) — `pkg/eval`: in-tree eval framework with LLM-as-judge.
- [Replay](concepts/replay.md) — `pkg/replay`: record real runs to fixtures and re-run them deterministically.
- [Spellbook](concepts/spellbook.md) — `pkg/spellbook`: prompt management.

## Patterns

End-to-end workflows assembled from the concept primitives. Each maps to a runnable example under `examples/`.

- [RAG](patterns/rag.md) — chunk → embed → store → retrieve → answer.
- [Multi-agent](patterns/multi-agent.md) — Supervisor + specialist sub-agents.
- [Human-in-the-loop](patterns/human-in-the-loop.md) — `InterruptBefore` + `Resume` + state edits.
- [Cost tracking](patterns/cost-tracking.md) — budget middleware, $-denominated reporting.
- [MCP server](patterns/mcp-server.md) — expose a `tool.Registry` to Claude Desktop.
- [Replay tests](patterns/replay-tests.md) — paid-API call once, deterministic regression forever.
- [Queue worker](patterns/queue-worker.md) — agents behind BullMQ / NATS / Kafka with durable jobs and per-job run ids.
- [Streaming](patterns/streaming.md) — plumb provider token deltas through the graph to a downstream consumer.

## Migration

Coming from another framework? Each guide is a side-by-side translation of the primitives you already know.

- [From langchaingo](migration/from-langchaingo.md)
- [From Eino](migration/from-eino.md)
- [From Genkit Go](migration/from-genkit-go.md)
- [From LangChain Python](migration/from-langchain-python.md)

## Operations

- [Ops guide](ops.md) — deployment shapes, the embedded SQLite trace store, exporting to your OTel pipeline, capacity sizing.
- [Benchmarks](benchmarks.md) — runtime overhead numbers and sizing guidance.
- [Security](security.md) — automated tooling, accepted findings, OWASP LLM Top 10 self-assessment.
- [ADRs](adr/) — the canonical record of every non-trivial design decision.
