# Quickstart

Five minutes from `go get` to a traced ReAct agent with a typed tool.

## 1. Install

galdor needs Go 1.25 or newer ([ADR-003](adr/ADR-003-bump-go-floor-to-1.25.md)).

```bash
go get github.com/YasserCR/galdor@latest
go get github.com/YasserCR/galdor/providers/anthropic@latest
```

Provider modules are independent so the core tree stays small. Add `providers/openai`, `providers/google` or `providers/bedrock` instead — the rest of the code below does not change.

For the CLI and the embedded dashboard:

```bash
go install github.com/YasserCR/galdor/cmd/galdor@latest
```

Set an API key in your environment so the snippets run as-is:

```bash
export ANTHROPIC_API_KEY=...
```

## 2. Your first ReAct agent

`agent.Run` is the one-shot wrapper around `NewReAct`. It builds a ReAct loop, invokes it once, and returns the assistant's final text.

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

Swap `anthropic.New` for `openai.New`, `google.New`, or `bedrock.New` and nothing else changes. See [Provider](concepts/provider.md) for the full surface and the `BaseURL` escape hatch that lets the OpenAI adapter target Groq, Together, MiniMax and friends.

## 3. Add a typed tool

Tools are regular Go functions with typed inputs and outputs. galdor derives the JSON Schema published to the model from the input struct's reflection metadata.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/YasserCR/galdor/pkg/agent"
	"github.com/YasserCR/galdor/pkg/tool"
	anthropic "github.com/YasserCR/galdor/providers/anthropic"
)

type weatherIn struct {
	City string `json:"city" jsonschema:"City to look up"`
}
type weatherOut struct {
	TempC float64 `json:"temp_c"`
	Sky   string  `json:"sky"`
}

func weather(_ context.Context, in weatherIn) (weatherOut, error) {
	return weatherOut{TempC: 18.5, Sky: "clear"}, nil
}

func main() {
	p, err := anthropic.New(anthropic.Config{APIKey: os.Getenv("ANTHROPIC_API_KEY")})
	if err != nil {
		log.Fatal(err)
	}

	reg, err := tool.NewRegistry(
		tool.MustNewTool("weather", "Look up the weather for a city", weather),
	)
	if err != nil {
		log.Fatal(err)
	}

	answer, err := agent.Run(context.Background(), agent.Config{
		Provider: p,
		Tools:    reg,
		Model:    "claude-haiku-4-5",
	}, "How's the weather in Quito?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(answer)
}
```

The ReAct loop now runs `model → tools → model → ...` until the model emits a final answer without tool calls or hits the iteration cap. See [Tool](concepts/tool.md) for the tag conventions and the `pkg/tool/builtins` set (time, math, http_get, file_read).

## 4. Trace your first run

galdor's observability is OTel-native: every provider call, tool execution and graph node becomes a span. Point a `TracerProvider` at the bundled SQLite exporter and wrap the provider.

```go
import (
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"github.com/YasserCR/galdor/pkg/observability"
)

exporter, _ := observability.NewSQLiteExporter("./traces.db")
tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
defer tp.Shutdown(context.Background())

tracer := tp.Tracer("quickstart")
p = observability.InstrumentProvider(p, tracer,
	observability.WithCaptureContent(true))
```

Anything you run through the wrapped provider now writes spans to `./traces.db`. For full setup including agent / graph hooks, see [Observability](concepts/observability.md) and the runnable [`examples/observability-trace`](../examples/observability-trace/).

## 5. View runs in `galdor ui`

```bash
galdor ui --db ./traces.db
# open http://127.0.0.1:7777
```

You'll see each run, its spans, the provider request and response (when `WithCaptureContent` is on), per-node timing, and a live SSE feed of in-flight runs. Same data is available on the command line:

```bash
galdor scry list  --db ./traces.db
galdor scry show  --db ./traces.db <run-id>
galdor scry stats --db ./traces.db
galdor scry tail  --db ./traces.db
```

## Next steps

Pick a topic based on what you're building.

- Building a multi-step workflow with branching: read [Graph](concepts/graph.md) and run [`examples/graph-counter`](../examples/graph-counter/).
- Pausing for a human approval: read [Human-in-the-loop](patterns/human-in-the-loop.md) and run [`examples/graph-interrupt`](../examples/graph-interrupt/).
- Coordinating multiple agents: read [Council](concepts/council.md) and [Multi-agent](patterns/multi-agent.md).
- Exposing your tools to Claude Desktop: read [MCP server](patterns/mcp-server.md).
- Adding retrieval: read [Memory](concepts/memory.md) and [RAG](patterns/rag.md).
- Pinning your prompts in CI: read [Replay](concepts/replay.md) and [Replay tests](patterns/replay-tests.md).
- Capping spend: read [Cost tracking](patterns/cost-tracking.md).
- Coming from langchaingo, Eino, Genkit Go, or LangChain Python: see the [Migration](README.md#migration) section.
