# galdor

> *galdor* (n., Old English, c. 9th century): incantation, spell, a chanted word that bends reality.

**A Go-native framework for building, orchestrating and observing AI agents.**
Native OpenTelemetry. Embedded dashboard. One binary. No external SaaS. Apache 2.0.

[![Go Reference](https://pkg.go.dev/badge/github.com/YasserCR/galdor.svg)](https://pkg.go.dev/github.com/YasserCR/galdor)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.25%2B-00ADD8.svg)](https://go.dev)

---

## Why galdor

The table below was last verified against each project's repo, releases and official docs in May 2026. Sources are linked under the table; PRs welcome when something drifts.

| | galdor | LangChain Python + LangSmith | LangChainGo | Eino | Genkit Go |
|---|---|---|---|---|---|
| Latest release | pre-alpha, v0.x | langchain-core v1.4.0 (May 2026) | v0.1.14 (Oct 2025) | v0.8.13 stable, v0.9.0-alpha active (May 2026) — pre-1.0 | mcp plugin v1.8.0 GA (May 2026) |
| Language / runtime | Go | Python | Go | Go | Go |
| Observability story | OTel-native, with an embedded SQLite trace store + dashboard served from the same binary | LangSmith (closed-source SaaS) | callbacks only, no OTel | callbacks; the shipped tracing target is Langfuse, not OTel | OTel-native; Genkit Monitoring (the hosted dashboard) is Google-Cloud only |
| End-to-end self-hostable (incl. dashboard) | yes | no — self-hosted LangSmith requires the paid Enterprise plan | yes (BYO observability stack) | yes (Apache framework + self-hosted Langfuse) | partial — OTel exporters point anywhere, but the polished Genkit Monitoring dashboard is GCP-only |
| Dependency footprint | core module pulls 6 direct + 14 indirect (the OTel + SQLite stack) | n/a | monolithic module; `go.sum` is 1,523 lines (≈200+ unique upstream modules) | core + per-component modules under `eino-ext` | per-plugin Go packages under `firebase/genkit/go/plugins/*` |
| MCP (Anthropic spec) | client + server, stdio | client + tool-as-server, first-party | client only, via 3rd-party adapters (e.g. `i2y/langchaingo-mcp-adapter`) | client only, first-party | client + server, first-party (stdio / SSE / StreamableHTTP) |
| A2A (Google spec) | client + server | not first-party | no | no | **no** — even though Google authored A2A, its Go support lives in the separate `a2aproject/a2a-go` SDK and in ADK Go, not in Genkit |
| Multi-agent built in | Supervisor + Swarm in `pkg/council` | LangGraph: supervisor, hierarchy, swarm | `agents` package (ReAct, conversational); no supervisor/swarm/hierarchy | DeepAgent (supervisor + sub-agent delegation) + graph orchestration | Flows + tool-calling agents; supervisor/swarm not first-class |
| Replay (record real run → deterministic re-run) | yes (record-to-fixture, replay anywhere) | LangSmith dataset replay (in the SaaS) | no (mock + conformance suite, not record/replay) | no | no documented offline fixture replay |
| Eval framework | yes, in-tree | `langchain.evaluation` + LangSmith eval UI | none | none | yes, `evaluators` plugin |
| License | Apache 2.0 | LangChain MIT; LangSmith proprietary | MIT | Apache 2.0 | Apache 2.0 |

galdor's distinctive position: **OTel-native + a single-binary self-hosted dashboard + first-party MCP server + first-party A2A server**, all in Go. None of the other four projects ship all of those today.

If your stack runs Python comfortably and you're happy paying for LangSmith, LangChain is the most mature option. If you need broad Go provider coverage today (more adapters than galdor's four), Eino is further along — at the cost of no OTel and no A2A. If you need Go *and* MCP server-side exposure *and* A2A interop in one place, galdor is currently the only framework that ships both first-party.

Sources (verified May 2026): [langchain-ai/langchain](https://github.com/langchain-ai/langchain), [LangSmith self-host docs](https://docs.langchain.com/langsmith/architectural-overview), [tmc/langchaingo](https://github.com/tmc/langchaingo), [cloudwego/eino](https://github.com/cloudwego/eino) + [eino-ext](https://github.com/cloudwego/eino-ext), [firebase/genkit/go/plugins/mcp](https://pkg.go.dev/github.com/firebase/genkit/go/plugins/mcp), [firebase/genkit/go/plugins](https://github.com/firebase/genkit/tree/main/go/plugins), [a2aproject/a2a-go](https://github.com/a2aproject/a2a-go).

---

## Status

**`v0.7.0` tagged. Looking for early integrators.**

The 10-phase roadmap is functionally complete: provider abstraction (Anthropic, OpenAI/MiniMax/Groq/Together/DeepSeek/vLLM/Ollama via `BaseURL` or [`providerset`](providerset/), Google Gemini, AWS Bedrock) · type-safe tools with reflection-derived JSON schemas · directed graph runtime with checkpoints, interrupt/resume and branch-map conditional edges · ReAct and Plan-and-Execute agent helpers · native OTel observability with embedded SQLite trace store, auto-WAL-checkpointing exporter, auto-stamped run ids, and an orphan-span warning banner · embedded web dashboard with live SSE, per-run DAG, time-travel · short-term memory windows + long-term memory backends (in-mem, SQLite/BM25, pgvector, qdrant) · provider-backed and HTTP/TEI embedders · Council multi-agent patterns (Supervisor, Swarm) · MCP client + server over stdio, SSE, and Streamable HTTP · A2A protocol (Google) · inline eval framework with LLM-as-judge · deterministic replay with prompt fingerprinting · per-provider retry/backoff, run/node timeouts, panic recovery, structured logging, goroutine leak gates, capability-aware validation · thinking-block strip middleware for OpenAI-compat thinking models.

**What's next:** real-world integration feedback. If you're shipping agents in Go and the table at the top resonates, try galdor on your stack and open an issue — the framework has covered the surface; the remaining edges only show up in actual deployments. The [pragma-galdor](https://github.com/YasserCR/galdor/blob/main/docs/patterns/queue-worker.md) retro is one such report, and it shaped most of v0.1.0; more would be welcome.

Between `v0.1.0` and `v1.0.0`, minor versions may still introduce breaking changes — pin a specific tag in your `go.mod` if you need reproducibility. See [`ROADMAP.md`](ROADMAP.md) for full phase tracking.

---

## Install

```bash
go get github.com/YasserCR/galdor@v0.7.0
# plus the provider(s) you need:
go get github.com/YasserCR/galdor/providers/anthropic@v0.7.0
go get github.com/YasserCR/galdor/providers/openai@v0.7.0
# or pick a provider at runtime via env var:
go get github.com/YasserCR/galdor/providerset@v0.7.0
```

The core module pulls only what it needs — providers, memory backends and protocol adapters live in their own Go modules so your dependency tree stays tight.

For the CLI + dashboard:

```bash
go install github.com/YasserCR/galdor/cmd/galdor@v0.7.0
galdor ui --db ./traces.db   # open http://127.0.0.1:7777
```

---

## Quickstart

A complete ReAct agent in 20 lines:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/YasserCR/galdor/pkg/agent"
	anthropic "github.com/YasserCR/galdor/providers/anthropic"
)

func main() {
	p, err := anthropic.New(anthropic.Config{APIKey: os.Getenv("ANTHROPIC_API_KEY")})
	if err != nil {
		log.Fatal(err)
	}

	answer, err := agent.Run(context.Background(), agent.Config{
		Provider: p,
		Model:    "claude-haiku-4-5",
	}, "What is the capital of Ecuador?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(answer)
}
```

Swap `anthropic` for `openai` (works with MiniMax / Groq / Together / Mistral via `BaseURL`), `google` (Gemini), or `bedrock` and nothing else changes.

---

## Highlights

### Type-safe tools (generics + reflection-derived JSON Schema)

```go
import (
	"context"
	"github.com/YasserCR/galdor/pkg/tool"
)

type weatherIn struct {
	City string `json:"city" jsonschema:"required, city to look up"`
}
type weatherOut struct {
	Temp float64 `json:"temp_c"`
	Sky  string  `json:"sky"`
}

weather := tool.MustNewTool("weather", "Look up the weather for a city",
	func(ctx context.Context, in weatherIn) (weatherOut, error) {
		return weatherOut{Temp: 18.5, Sky: "clear"}, nil
	})

reg, _ := tool.NewRegistry(weather)

answer, _ := agent.Run(ctx, agent.Config{
	Provider: p, Tools: reg, Model: "claude-haiku-4-5",
}, "How's the weather in Quito?")
```

`In` and `Out` are real Go types — the JSON schema published to the LLM is derived from `In`'s reflection metadata. No magic strings, no `interface{}`.

### Native OpenTelemetry — built in, not bolted on

```go
import (
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"github.com/YasserCR/galdor/pkg/observability"
)

exporter, _ := observability.NewSQLiteExporter("./traces.db")
tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
tracer := tp.Tracer("my-agent")

// Wrap your provider — every LLM call now produces a span.
p = observability.InstrumentProvider(p, tracer,
	observability.WithCaptureContent(true))
```

Every LLM call, tool invocation, and graph node becomes an OTel span following the GenAI semantic conventions. Inspect them with `galdor ui` or pipe them to your existing Datadog / Honeycomb / Grafana stack — same data, your choice of consumer.

### Multi-agent: Supervisor and Swarm built in

```go
import "github.com/YasserCR/galdor/pkg/council"

supervisor, _ := council.NewSupervisor(council.SupervisorConfig{
	Provider: p, Model: "claude-haiku-4-5",
	Workers: []council.Worker{
		{Name: "billing", Description: "handles invoices, refunds",
			Run: billingWorker},
		{Name: "technical", Description: "diagnoses bugs, outages",
			Run: technicalWorker},
	},
})

final, _ := supervisor.Invoke(ctx, council.SupervisorState{Input: userMessage})
```

A scripted-LLM routing supervisor that delegates each turn to specialists. See the full example: [`examples/integration-support-bot`](examples/integration-support-bot/).

### Human-in-the-loop with `InterruptBefore`

```go
g := graph.New[TransferState]().
	AddNode("validate", validate).
	AddNode("execute", execute).
	AddEdge(graph.START, "validate").
	AddEdge("validate", "execute").
	InterruptBefore("execute")  // ← pause for human approval

r, _ := g.Compile()
ckpt := graph.NewMemoryCheckpointer[TransferState]()

// Phase 1: run until the gate. Returns ErrInterrupted.
_, err := r.InvokeWith(ctx, init, graph.RunOptions[TransferState]{
	RunID: runID, Checkpointer: ckpt,
})

// Phase 2: human reviews and edits state.
ck, _, _ := ckpt.Load(ctx, runID)
decision := promptHuman(ck.State)  // your UI / Slack bot / etc.

// Phase 3: resume with the decision injected.
final, _ := r.Resume(ctx, graph.RunOptions[TransferState]{
	RunID: runID, Checkpointer: ckpt, OverrideState: &decision,
})
```

Auditable, safe-by-construction approval flows. See [`examples/integration-approval-gate`](examples/integration-approval-gate/).

### Replay: paid-API → fixture → deterministic test

```go
// One-time: record a real run with prompt/completion capture on,
// then export the recording.
//
//   galdor scry replay <run-id> -o fixture.json

// Forever after: replay the run for free in CI.
rec, _ := replay.LoadFromFile("fixture.json")
mock := replay.NewProvider(rec.Calls, replay.ModeStrict)

r, _ := agent.NewReAct(agent.Config{Provider: mock, Model: "...", Tools: reg})
final, _ := r.Invoke(ctx, state)
// If your prompts drifted, ErrPromptMismatch tells you exactly which call.
```

Regression tests for prompts and agents that don't hit the network and don't burn tokens. See [`examples/integration-cost-tracked`](examples/integration-cost-tracked/) for the complementary budget-enforcement pattern.

### MCP server: expose your tools to Claude Desktop in 20 lines

```go
import (
	"github.com/YasserCR/galdor/pkg/mcp"
	"github.com/YasserCR/galdor/pkg/tool/builtins"
)

func main() {
	now, _ := builtins.NewTimeTool()
	math, _ := builtins.NewMathTool()
	reg, _ := tool.NewRegistry(now, math, yourCustomTool)

	srv := mcp.NewServer(reg, mcp.ServerInfo{Name: "my-tools", Version: "0.1"})
	transport := mcp.NewStdioTransport(os.Stdin, os.Stdout)
	_ = srv.Serve(context.Background(), transport)
}
```

Build the binary, point Claude Desktop's `claude_desktop_config.json` at it, restart Claude Desktop. Your tools appear in the picker. Full instructions in [`examples/integration-mcp-server`](examples/integration-mcp-server/).

For long-lived daemons that many clients share, swap the transport — SSE for IDE-compatibility today, Streamable HTTP for the post-2024-11-05 spec:

```go
// pre-2024-11-05 spec (the SSE transport Cursor/Claude Desktop still default to)
transport := mcp.NewSSETransport(":4000")
// 2024-11-05 spec (single endpoint, session id via Mcp-Session-Id header)
transport := mcp.NewStreamableHTTPTransport(":4000")
```

### Pick a provider at runtime

```go
import "github.com/YasserCR/galdor/providerset"

// Reads LLM_PROVIDER, LLM_API_KEY, LLM_BASE_URL, LLM_HTTP_TIMEOUT.
// Supports anthropic, openai, google, bedrock + 7 OpenAI-compatible
// aliases: groq, together, mistral, minimax, deepseek, vllm, ollama.
p, err := providerset.FromEnv()
```

The equivalent of LiteLLM for Go: one switch, every supported provider, no per-app boilerplate. Lives in its own module so the core stays lean. See [`docs/concepts/providerset.md`](docs/concepts/providerset.md).

### Self-hosted embeddings via HTTP

```go
import "github.com/YasserCR/galdor/pkg/embedder"

// Works against HuggingFace TEI, Infinity, vLLM-embeddings, or any
// OpenAI-compatible /embeddings endpoint. Stdlib-only, no CGO.
emb, _ := embedder.NewHTTPEmbedder(embedder.HTTPConfig{
    URL:   "http://localhost:8080",
    Shape: embedder.ShapeTEI,
})
```

Plugs into `memory.Retriever` directly; satisfies `memory.Embedder`. See [`docs/concepts/embedder.md`](docs/concepts/embedder.md).

### Thinking-model output, sanitized

```go
import "github.com/YasserCR/galdor/pkg/provider"

// Opt-in middleware that strips <think>...</think> blocks emitted
// inline by OpenAI-compat thinking models (MiniMax, DeepSeek, Qwen).
// Handles closing tags split across stream deltas.
p = provider.StripThinkingBlocks(p)
```

### Production hardening (Phase 10)

```go
import "github.com/YasserCR/galdor/pkg/provider"

// Automatic retry with exponential backoff + jitter; respects the
// server's Retry-After header; never retries auth/invalid-request.
p = provider.Retry(p, provider.RetryConfig{
	MaxAttempts: 5,
	OnRetry: func(n int, d time.Duration, err error) {
		slog.Warn("retrying", "attempt", n, "delay", d, "err", err)
	},
})

// Per-run and per-node timeouts; panic recovery in nodes, tools,
// and hooks; structured logging via slog.
final, err := r.InvokeWith(ctx, state, graph.RunOptions[State]{
	Timeout:     2 * time.Minute,
	NodeTimeout: 30 * time.Second,
	Logger:      slog.New(slog.NewJSONHandler(os.Stdout, nil)),
})
```

---

## Architecture (at a glance)

```
┌─────────────────────────────────────────────────────────────┐
│  CLI (galdor scry/ui)    Web dashboard with SSE + per-run DAG│
├─────────────────────────────────────────────────────────────┤
│  Eval Framework  │  Replay Engine  │  Time-travel UI        │
├─────────────────────────────────────────────────────────────┤
│  Agent Runtime (graph executor over goroutines + channels)  │
├─────────────────────────────────────────────────────────────┤
│  Tools  │  Memory  │  Embedder  │  Council  │  MCP  │  A2A  │
├─────────────────────────────────────────────────────────────┤
│  Provider Abstraction + Providerset (env-driven selection)  │
├─────────────────────────────────────────────────────────────┤
│  Observability Core (OTel-native, embedded SQLite backend)  │
└─────────────────────────────────────────────────────────────┘
```

See [`ARCHITECTURE.md`](ARCHITECTURE.md) for the full module map and [`docs/adr/`](docs/adr/) for design decisions.

---

## Complete examples

Each one is a runnable end-to-end demo with its own README.

| Example | What it shows |
|---|---|
| [`integration-support-bot`](examples/integration-support-bot/) | Supervisor + 3 specialist ReAct sub-agents with separate tool registries (billing, technical, general). |
| [`integration-approval-gate`](examples/integration-approval-gate/) | `InterruptBefore` + `MemoryCheckpointer` + `Resume`. Banking-style transfers with low/high/over-cap scenarios. |
| [`integration-mcp-server`](examples/integration-mcp-server/) | Wraps a `tool.Registry` as an MCP server over stdio, connectable from Claude Desktop. |
| [`integration-cost-tracked`](examples/integration-cost-tracked/) | `BudgetProvider` middleware enforcing a token cap with $-denominated reporting. |

Smaller, feature-focused examples live alongside:

| Example | What it shows |
|---|---|
| [`agent-react`](examples/agent-react/) | Minimum ReAct loop with tools |
| [`tools-loop`](examples/tools-loop/) | LLM ↔ tools dispatch cycle |
| [`graph-counter`](examples/graph-counter/) | Counting nodes in a graph |
| [`graph-interrupt`](examples/graph-interrupt/) | The `InterruptBefore` primitive on its own |
| [`memory-rag`](examples/memory-rag/) | Chunk → embed → SQLite → Retriever |
| [`observability-trace`](examples/observability-trace/) | Wiring the trace exporter |
| [`scry-store`](examples/scry-store/) | Working with the SQLite trace store |
| [`provider-interface`](examples/provider-interface/) | Implementing a custom `Provider` |
| [`eval-suite`](examples/eval-suite/) | `eval.Config` + scorers + `RunAndExit` |

---

## Provider matrix

| Provider | Module path | Streaming | Tools | Vision | Notes |
|---|---|---|---|---|---|
| Anthropic | `providers/anthropic` | yes | yes | yes | reference adapter; prompt caching honored |
| OpenAI | `providers/openai` | yes | yes | yes | also works against Mistral, MiniMax, Together, Groq, vLLM via `BaseURL` |
| Google Gemini | `providers/google` | yes | yes | yes | AI Studio surface; Vertex AI via custom `HTTPClient` |
| AWS Bedrock | `providers/bedrock` | yes | yes | yes | Converse API; SigV4 via AWS SDK Go v2 |

For runtime selection across all of the above plus seven OpenAI-compatible aliases (`groq`, `together`, `mistral`, `minimax`, `deepseek`, `vllm`, `ollama`), pick a provider via env var with [`providerset.FromEnv()`](providerset/) instead of importing each adapter directly.

Embedders ship in the same provider modules: `openai.NewEmbedder` (covers OpenAI-compatible endpoints) and `google.NewEmbedder`. For self-hosted embeddings (TEI, Infinity, vLLM-embeddings, or any OpenAI-compatible `/embeddings` endpoint), use [`pkg/embedder.HTTPEmbedder`](pkg/embedder/).

---

## Memory backends

| Backend | Module path | Best for |
|---|---|---|
| in-memory | `pkg/memory` (`InMemoryStore`) | tests, getting-started |
| SQLite + BM25 | `memory/sqlite` | single-process production, embedded apps |
| pgvector | `memory/pgvector` | Postgres-centric stacks |
| qdrant | `memory/qdrant` | dedicated vector DB |

Same `memory.Store` interface across all four — swap by changing one constructor.

---

## Use galdor when…

* You're shipping into infrastructure that can't reach an external SaaS (compliance, data residency, air-gap).
* You want a single binary you can drop into a container, no Python runtime, no GCP or LangSmith dependency.
* You care about audit trails — the SQLite store + replay engine make every run reconstructable from disk.
* You're already invested in OTel — galdor's spans drop into your existing pipeline (Datadog, Honeycomb, Grafana, Tempo) without glue code.
* Your team is more comfortable in Go than in Python.

## Don't use galdor when…

* You need the broadest possible ecosystem of pre-built tools, vector stores, and document loaders — LangChain Python still wins on raw integration count.
* You need broader Go provider coverage today than the four galdor ships — Eino currently has more provider components in `eino-ext`.
* You need very specific provider features galdor hasn't surfaced yet (audio, file uploads, certain vision modes). Check the provider matrix above.
* You're an early-stage prototyper who wants a rich hosted GUI to poke at — galdor's dashboard is intentionally lean.

---

## CLI

```bash
galdor ui              --db ./traces.db
galdor scry list       --db ./traces.db
galdor scry show       --db ./traces.db <run-id>
galdor scry stats      --db ./traces.db [--by overall|provider|model]
galdor scry tail       --db ./traces.db [--interval 1s]
galdor scry replay     --db ./traces.db <run-id> [-o fixture.json]
```

`scry` is the introspection family (Old English: *to perceive, to discern*). Every command honors `$GALDOR_DB` and `~/.galdor/traces.db` as fallback paths.

---

## Documentation

Start at [`docs/`](docs/) — the index covers quickstart, one conceptual guide per package, applied patterns, migration guides from langchaingo / Eino / Genkit Go / LangChain Python, and the ops guide.

* [`docs/quickstart.md`](docs/quickstart.md) — install → first ReAct agent → first tool → first traced run, in 15 minutes
* [`docs/concepts/`](docs/concepts/) — one page per package (provider, schema, tool, graph, agent, memory, observability, council, mcp, a2a, eval, replay, spellbook)
* [`docs/patterns/`](docs/patterns/) — RAG, multi-agent, human-in-the-loop, cost tracking, MCP server, replay-driven tests
* [`docs/migration/`](docs/migration/) — coming from another framework? side-by-side translations
* [`docs/ops.md`](docs/ops.md) — deployment shapes, trace store retention, exporting to your OTel pipeline
* [`docs/benchmarks.md`](docs/benchmarks.md) — runtime overhead, throughput numbers, sizing guidance
* [`docs/security.md`](docs/security.md) — automated tooling, accepted findings, OWASP LLM Top 10 self-assessment
* [`docs/adr/`](docs/adr/) — architectural decision records
* [`ARCHITECTURE.md`](ARCHITECTURE.md) — module map and design invariants
* [`ROADMAP.md`](ROADMAP.md) — phase-by-phase delivery tracker
* [`GOVERNANCE.md`](GOVERNANCE.md) — how decisions get made
* [`CONTRIBUTING.md`](CONTRIBUTING.md) — how to send patches
* [godoc reference](https://pkg.go.dev/github.com/YasserCR/galdor) — API surface

---

## Contributing

galdor uses the [Developer Certificate of Origin (DCO)](DCO.txt) — every commit must be signed off:

```bash
git commit -s -m "..."
```

PRs welcome. We don't require a CLA. See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the dev loop.

---

## Governance

galdor is currently maintained by a single BDFL with an explicit plan to transition to a multi-maintainer model once three contributors with sustained activity exist. See [`GOVERNANCE.md`](GOVERNANCE.md).

---

## License

galdor is licensed under the [Apache License 2.0](LICENSE) — permissive, with an explicit patent grant, widely accepted by enterprise legal review.

Apache 2.0 is the contract; this README is a description. The code in this repository today is published under Apache 2.0 and any version released under that license stays available under it forever — that's what Apache 2.0 means. Forks are welcome.

---

*"The incantation framework for Go agents."*
