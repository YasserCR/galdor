# Migrating from langchaingo

This guide is for users coming from
[tmc/langchaingo](https://github.com/tmc/langchaingo). The two
projects target Go, share many idioms, and differ mainly in the
observability story and the multi-agent surface. langchaingo's
strength is its breadth of integration adapters; galdor's
strength is OTel-native observability + first-party MCP server +
first-party A2A + replay.

The translations below cover the most common idioms.

## Provider / LLM

| In langchaingo | In galdor |
|---|---|
| `llms.LLM` (interface) | `provider.Provider` (interface) |
| `openai.New(opts...)` | `openai.New(openai.Config{APIKey, BaseURL, ...})` |
| `anthropic.New(opts...)` | `anthropic.New(anthropic.Config{APIKey, ...})` |
| `googleai.New(ctx, opts...)` | `google.New(google.Config{APIKey, ...})` |
| `bedrock.New(...)` | `bedrock.New(bedrock.Config{...})` |
| `llm.Call(ctx, prompt)` / `GenerateContent` | `p.Generate(ctx, provider.Request{Model, Messages, Tools, ...})` |
| `streaming.WithStreamingFunc(...)` option | `p.Stream(ctx, req) (StreamReader, error)` |

galdor uses a `Request` struct rather than functional options.
The fields you'd express with options in langchaingo
(`temperature`, `max_tokens`, `tools`, `tool_choice`) are first-
class fields on `provider.Request`.

```go
resp, err := p.Generate(ctx, provider.Request{
    Model:       "claude-haiku-4-5",
    Messages:    []schema.Message{schema.UserMessage("hi")},
    Temperature: ptr(0.7),
    MaxTokens:   ptr(1024),
})
```

`Messages` is `[]schema.Message`, where each message has a
`Role`, `Content` (parts: text, image, …), and optional
`ToolCalls`. The `schema.SystemMessage(...)`,
`schema.UserMessage(...)`, `schema.AssistantMessage(...)`,
`schema.ToolResultMessage(...)` helpers cover the common cases.

## Tools

| In langchaingo | In galdor |
|---|---|
| `tools.Tool` interface (Name, Description, Call) | `tool.Tool[In, Out]` (generic; input/output are real Go types) |
| Manual JSON-Schema string | Derived from `In`'s reflection metadata (`jsonschema:"..."` tags) |
| Function-tool helpers in `agents` | `tool.MustNewTool(name, desc, func(ctx, in) (out, error))` |
| `serpapi.New(...)` etc | not shipped first-party — write or import an MCP server |

```go
type weatherIn struct {
    City string `json:"city" jsonschema:"required, city to look up"`
}
type weatherOut struct {
    Temp float64 `json:"temp_c"`
    Sky  string  `json:"sky"`
}

weather := tool.MustNewTool("weather", "Look up the weather",
    func(ctx context.Context, in weatherIn) (weatherOut, error) {
        return weatherOut{Temp: 18.5, Sky: "clear"}, nil
    })

reg, _ := tool.NewRegistry(weather)
```

The registry is what you pass to agents. No `interface{}`,
no manual schema string.

## Chains / Agents

| In langchaingo | In galdor |
|---|---|
| `chains.LLMChain` | one node in `pkg/graph` |
| `chains.SequentialChain` | a graph with `AddEdge(a, b)` chains |
| `agents.NewOneShotAgent` / `agents.NewConversationalAgent` | `pkg/agent.NewReAct` |
| `agents.Executor` | the `*graph.Runnable[State]` returned by `NewReAct` |
| custom chain by implementing `Chain` | a custom `graph.NodeFunc[State]` |

```go
r, _ := agent.NewReAct(agent.Config{
    Provider: p,
    Tools:    reg,
    Model:    "claude-haiku-4-5",
})
final, _ := r.Invoke(ctx, agent.State{
    Messages: []schema.Message{schema.UserMessage("what's the weather?")},
})
```

For non-ReAct flows, use `pkg/graph` directly. The same runnable
supports `Invoke`, `Stream`, `InvokeWith` (with checkpoints +
hooks + timeouts), and `Resume`.

## Memory

| In langchaingo | In galdor |
|---|---|
| `memory.ConversationBuffer` etc | `pkg/memory.Window` (sliding window of messages) |
| `vectorstores.{Chroma,Pinecone,Weaviate,...}` | `memory/sqlite`, `memory/pgvector`, `memory/qdrant` |
| `embeddings.OpenAI` | `providers/openai.NewEmbedder` |
| `embeddings.GoogleAI` | `providers/google.NewEmbedder` |
| `textsplitter.RecursiveCharacter` | `pkg/memory/chunk.Recursive` |

The vector-store list is shorter on the galdor side; if you
need a store that isn't shipped, the `memory.Store` interface is
narrow (Add, Retrieve, Len, Close). See
[rag](../patterns/rag.md) for the full chunk-embed-store-retrieve
flow.

## Observability

| In langchaingo | In galdor |
|---|---|
| `callbacks.Handler` interface | OTel spans, automatically, via `observability.InstrumentProvider` |
| no built-in trace store | `observability.NewSQLiteExporter` + the embedded dashboard |
| BYO logging via callbacks | structured logs via `slog.Logger` plumbed through `RunOptions{Logger}` |
| no run/replay | `pkg/replay` — record a real run, export a fixture, replay in CI |

```go
exporter, _ := observability.NewSQLiteExporter("./traces.db")
tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
p = observability.InstrumentProvider(p, tp.Tracer("agent"),
    observability.WithCaptureContent(true))
```

Every provider call, tool invocation, and graph node becomes an
OTel span. `galdor ui --db ./traces.db` serves the trace
dashboard locally; pipe the same spans to Datadog / Honeycomb /
Tempo if you already have an OTel pipeline.

## What langchaingo has that galdor doesn't

- **Breadth of vector stores and document loaders.** langchaingo
  ships adapters for Chroma, Pinecone, Weaviate, Milvus and
  many loaders. galdor ships three vector stores
  (SQLite/BM25, pgvector, qdrant). If your stack depends on a
  store galdor doesn't have, you either implement
  `memory.Store` (two short methods) or stay on langchaingo for
  that piece.
- **A wider provider matrix.** langchaingo's `llms/` covers more
  adapters out of the box (Cohere, Ollama, local llama.cpp,
  etc). galdor ships four providers, plus OpenAI-compatible
  endpoints (Mistral, MiniMax, Together, Groq, vLLM) via the
  OpenAI provider's `BaseURL`.

## What galdor has that langchaingo doesn't

- **OTel-native observability.** langchaingo's callbacks emit no
  spans; galdor's `pkg/observability` is OTel-first.
- **Embedded trace store + dashboard.** Single-binary
  introspection; no LangSmith required.
- **First-party MCP client + server.** langchaingo has client-
  side MCP via a third-party adapter
  (`i2y/langchaingo-mcp-adapter`); server-side is not first-party.
- **First-party A2A (Google spec).** Not in langchaingo.
- **Supervisor / Swarm in `pkg/council`.** langchaingo has
  `agents` (ReAct, conversational); no supervisor / swarm /
  hierarchy primitives.
- **Replay engine.** Record a real run, export a fixture,
  replay deterministically in CI — see
  [replay-tests](../patterns/replay-tests.md).

## Tradeoffs to expect

- langchaingo is a monolithic module; galdor is split (core +
  per-provider + per-memory-backend). Your `go.sum` will be
  smaller after migrating, but you'll add explicit `go get` lines
  for each adapter.
- langchaingo's `llms.Call` is the simplest possible call;
  galdor's `Generate` requires constructing a `Request`. A
  one-shot wrapper (`agent.Run`) exists for the simple case.
- langchaingo's tool surface is `interface{}` heavy; galdor's
  is generics-heavy. Compile-time type errors replace runtime
  schema mismatches.
