# galdor

> *galdor* (n., Old English, c. 9th century): incantation, spell, a chanted word that bends reality.

**A Go-native framework for building, orchestrating and observing AI agents.**
Native OpenTelemetry. Embedded dashboard. One binary. No external SaaS. Apache 2.0.

[![Go Reference](https://pkg.go.dev/badge/github.com/YasserCR/galdor.svg)](https://pkg.go.dev/github.com/YasserCR/galdor)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.25%2B-00ADD8.svg)](https://go.dev)

---

## Why galdor

| | galdor | LangChain Python + LangSmith | LangChainGo | Eino | Genkit Go |
|---|---|---|---|---|---|
| Language / runtime | Go | Python + SaaS | Go | Go | Go |
| Observability | Native OTel + embedded dashboard | External SaaS (Langsmith) | bring your own | OTel callbacks | OTel + Genkit UI |
| Self-hosted by default | yes, single binary + SQLite | no, needs Langsmith | partial | partial | partial |
| Core deps (count) | ~10 direct | n/a | 170+ | varies | varies |
| Per-provider Go modules | yes | n/a | no, monolithic | yes | partial |
| MCP (Anthropic spec) | client + server | yes | no | no | no |
| A2A (Google spec) | client + server | partial | no | no | no |
| Multi-agent built in | Supervisor + Swarm | LangGraph | via LangGraph-Go | yes | partial |
| Replay (paid-API → fixture → free replay) | yes | no | no | no | no |
| Eval framework | yes | Langsmith | external | no | yes |
| License | Apache 2.0 | MIT, SaaS-locked tier | MIT | Apache 2.0 | Apache 2.0 |
| Status | pre-alpha, v0.x | 2.x stable | 0.x stable | 1.x | 1.x |

galdor is **the framework you'd want if your stack can't run Python or can't ship trace data to an external SaaS**. Banks, defense, regulated industries, anyone shipping a single binary into private infra.

---

## Status

**Pre-alpha — APIs are unstable until v1.0.** What works today:

Foundations · provider abstraction (Anthropic, OpenAI/MiniMax/Groq/Together, Google Gemini, AWS Bedrock) · type-safe tools with reflection-derived JSON schemas · directed graph runtime with checkpoints and interrupt/resume · ReAct and Plan-and-Execute agent helpers · native OTel observability with embedded SQLite trace store and web dashboard · short-term memory windows + long-term memory backends (in-mem, SQLite/BM25, pgvector, qdrant) · chunking and embedding helpers · Council multi-agent patterns (Supervisor, Swarm) · MCP client and server (stdio) · A2A protocol (Google) · inline eval framework with LLM-as-judge · deterministic replay with prompt fingerprinting · time-travel UI · per-provider retry/backoff · per-run / per-node timeouts · panic recovery in nodes / hooks / tools · structured logging via slog · goroutine leak gates · capability-aware boundary validation.

See [`ROADMAP.md`](ROADMAP.md) for full phase tracking.

---

## Install

```bash
go get github.com/YasserCR/galdor@latest
# plus the provider(s) you need:
go get github.com/YasserCR/galdor/providers/anthropic@latest
go get github.com/YasserCR/galdor/providers/openai@latest
```

The core module pulls only what it needs — providers, memory backends and protocol adapters live in their own Go modules so your dependency tree stays tight.

For the CLI + dashboard:

```bash
go install github.com/YasserCR/galdor/cmd/galdor@latest
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
│  CLI (galdor scry/ui)    Web dashboard with SSE live feed   │
├─────────────────────────────────────────────────────────────┤
│  Eval Framework  │  Replay Engine  │  Time-travel UI        │
├─────────────────────────────────────────────────────────────┤
│  Agent Runtime (graph executor over goroutines + channels)  │
├─────────────────────────────────────────────────────────────┤
│  Tools  │  Memory  │  Council  │  MCP  │  A2A               │
├─────────────────────────────────────────────────────────────┤
│  Provider Abstraction (Anthropic, OpenAI, Google, Bedrock)  │
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

Embedders ship in the same provider modules: `openai.NewEmbedder` (covers OpenAI-compatible endpoints) and `google.NewEmbedder`.

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
* You want a single binary you can drop into a container without a Python runtime.
* You care about audit trails — galdor's SQLite store + replay engine make every run reconstructable forever.
* You're already invested in OTel — galdor's spans drop into your existing pipeline (Datadog, Honeycomb, Grafana, Tempo) with zero glue code.
* Your team is more comfortable in Go than in Python.

## Don't use galdor when…

* You need the broadest possible ecosystem of pre-built tools — LangChain (Python) still wins on integration count.
* You're an early-stage prototyper who needs LangSmith-style heuristics and rich GUI dashboards over your own — galdor's dashboard is intentionally lean.
* You need very specific provider features galdor hasn't surfaced yet (file uploads, vision streaming, audio). Check the provider matrix above.

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

* [`ARCHITECTURE.md`](ARCHITECTURE.md) — module map and design invariants
* [`ROADMAP.md`](ROADMAP.md) — phase-by-phase delivery tracker
* [`GOVERNANCE.md`](GOVERNANCE.md) — how decisions get made
* [`CONTRIBUTING.md`](CONTRIBUTING.md) — how to send patches
* [`docs/adr/`](docs/adr/) — architectural decision records
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
