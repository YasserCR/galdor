# Migrating from LangChain (Python)

This guide is for Python LangChain users considering a move to
Go. Be honest with yourself about why: if you depend on the
ecosystem breadth — every vector store, every loader, every
integration — galdor will feel narrower. galdor's wins are
elsewhere: single-binary deploy, OTel-native, self-hostable
dashboard, first-party A2A, and Go ergonomics (compile-time
type safety, no GIL, structured concurrency).

## Expectations to set

| | LangChain Python + LangSmith | galdor |
|---|---|---|
| Provider breadth | very broad | four first-party (Anthropic, OpenAI / OAI-compatible, Google, Bedrock) |
| Vector store breadth | very broad | three (SQLite/BM25, pgvector, qdrant) |
| Document loaders | very broad | none shipped — write what you need, the input type is `memory.Document` |
| Deployment | Python runtime + dependencies | one static Go binary |
| Observability | LangSmith (closed-source SaaS; self-host on Enterprise) | OTel-native, embedded SQLite + dashboard, optional OTLP to any backend |
| MCP | client + tool-as-server, first-party | client + server (stdio), first-party |
| A2A (Google spec) | not first-party | client + server first-party |

If your LangChain code is "import langchain, plug in 12 specific
integrations, deploy on Python infra" — staying on LangChain
will be faster.

If your code is "use a few well-known providers, want a single
deployable, want OTel out of the box, plan to run on locked-down
infra" — galdor is built for that.

## ChatModel / Provider

| In LangChain Python | In galdor |
|---|---|
| `ChatAnthropic(model="claude-haiku-4-5")` | `anthropic.New(anthropic.Config{APIKey, ...})` |
| `ChatOpenAI(model="gpt-4o-mini")` | `openai.New(openai.Config{APIKey, ...})` |
| `ChatVertexAI(...)` | `google.New(google.Config{APIKey, ...})` |
| `ChatBedrock(...)` | `bedrock.New(bedrock.Config{...})` |
| `model.invoke(prompt)` | `p.Generate(ctx, provider.Request{Model, Messages, ...})` |
| `model.stream(prompt)` | `p.Stream(ctx, req) (StreamReader, error)` |
| `model.bind_tools(tools)` | `provider.Request{Tools: defs, ToolChoice: ...}` |

```go
resp, err := p.Generate(ctx, provider.Request{
    Model:    "claude-haiku-4-5",
    Messages: []schema.Message{schema.UserMessage("hi")},
})
fmt.Println(resp.Message.Text())
```

`provider.Request` is the moral equivalent of LangChain's
`invoke(input, config)` — every option is a field, not a kwarg.

## Tools

| In LangChain Python | In galdor |
|---|---|
| `@tool` decorator on a function | `tool.MustNewTool(name, desc, fn)` |
| Pydantic `BaseModel` for input schema | a Go struct with `jsonschema:"..."` tags |
| `StructuredTool.from_function(...)` | same as above; the generic input/output type *is* the schema |
| `tools=[t1, t2]` passed to a chain | `tool.NewRegistry(t1, t2)` then `agent.Config{Tools: reg}` |

```go
type lookupIn struct {
    InvoiceID string `json:"invoice_id" jsonschema:"required, invoice ID"`
}
type lookupOut struct {
    Customer string  `json:"customer"`
    Amount   float64 `json:"amount"`
}

lookup := tool.MustNewTool("lookup_invoice", "Look up an invoice",
    func(ctx context.Context, in lookupIn) (lookupOut, error) {
        return lookupOut{Customer: "ACME", Amount: 42.50}, nil
    })

reg, _ := tool.NewRegistry(lookup)
```

A Python `@tool` returns `str` and parses the args dict
runtime; galdor's `tool.MustNewTool` is generic, so the input
and output are real Go structs. Schema mismatches surface at
compile time.

## LangGraph → pkg/graph

| In LangGraph | In galdor |
|---|---|
| `StateGraph(State)` | `graph.New[State]()` |
| `g.add_node("name", fn)` | `g.AddNode("name", fn)` |
| `g.add_edge("a", "b")` | `g.AddEdge("a", "b")` |
| `g.add_conditional_edges("a", router)` | `g.AddConditionalEdge("a", router)` |
| `START` / `END` | `graph.START` / `graph.END` |
| `g.compile()` | `g.Compile()` |
| `graph.invoke(initial_state)` | `r.Invoke(ctx, initialState)` |
| `interrupt_before=["execute"]` | `g.InterruptBefore("execute")` |
| `MemorySaver()` | `graph.NewMemoryCheckpointer[State]()` |
| `graph.invoke(..., thread_id=...)` | `r.InvokeWith(ctx, state, graph.RunOptions[State]{RunID: ..., Checkpointer: ckpt})` |
| `graph.get_state(thread_id)` | `ckpt.Load(ctx, runID)` |
| `graph.update_state(thread_id, values)` then resume | `r.Resume(ctx, RunOptions{RunID, Checkpointer, OverrideState: &edited})` |

The mental model is almost identical. The Go version is typed:
`graph.New[MyState]()` parameterizes the state on a Go type, and
nodes are `func(ctx, S) (S, error)`. No `TypedDict` runtime
indirection.

```go
g := graph.New[TransferState]().
    AddNode("validate", validate).
    AddNode("execute", execute).
    AddEdge(graph.START, "validate").
    AddEdge("validate", "execute").
    AddEdge("execute", graph.END).
    InterruptBefore("execute")

r, _ := g.Compile()
```

See [human-in-the-loop](../patterns/human-in-the-loop.md) for
the full pause / resume flow.

## Agents (ReAct)

| In LangChain | In galdor |
|---|---|
| `create_react_agent(llm, tools)` | `agent.NewReAct(agent.Config{Provider, Tools, Model})` |
| `AgentExecutor.invoke({"input": "..."})` | `r.Invoke(ctx, agent.State{Messages: ...})` |
| `agent.stream(...)` | `r.Stream(ctx, state)` |

```go
r, _ := agent.NewReAct(agent.Config{
    Provider: p,
    Tools:    reg,
    Model:    "claude-haiku-4-5",
})
final, _ := r.Invoke(ctx, agent.State{
    Messages: []schema.Message{schema.UserMessage("what's the weather in Quito?")},
})
fmt.Println(final.FinalText)
```

For one-shot use, `agent.Run(ctx, cfg, input, sysPrompts...)` is
the convenience wrapper.

## Retrievers / vector stores

| In LangChain | In galdor |
|---|---|
| `Chroma(...).as_retriever()` | `&memory.Retriever{Store: store, Embedder: emb, DefaultK: 3}` |
| `OpenAIEmbeddings()` | `providers/openai.NewEmbedder(openai.EmbedderConfig{...})` |
| `GoogleGenerativeAIEmbeddings(...)` | `providers/google.NewEmbedder(google.EmbedderConfig{...})` |
| `RecursiveCharacterTextSplitter(...)` | `chunk.Recursive{Size, Overlap}` |
| `vectorstore.add_documents(docs)` | `store.Add(ctx, chunks)` |
| `retriever.invoke(query)` | `retriever.Retrieve(ctx, memory.Query{Text: q})` |

```go
store, _ := sqlite.Open("./rag.db")
emb, _ := openaiprov.NewEmbedder(openaiprov.EmbedderConfig{
    APIKey: os.Getenv("OPENAI_API_KEY"),
})

retriever := &memory.Retriever{Store: store, Embedder: emb, DefaultK: 3}
hits, _ := retriever.Retrieve(ctx, memory.Query{Text: question})
```

Document loaders aren't shipped — you wire whatever ingest path
makes sense (Go's stdlib reads JSON, CSV, plain text; for PDFs
and HTML, pull in a Go library directly). The chunk-embed-store
flow doesn't care where the text came from.

See [rag](../patterns/rag.md).

## LangSmith → OTel + embedded dashboard

| In LangSmith | In galdor |
|---|---|
| Hosted SaaS or self-host on the Enterprise plan | self-host out of the box, single binary |
| LangSmith trace UI | `galdor ui --db ./traces.db` |
| LangSmith run grouping (project / tag) | OTel resource attributes / span attributes |
| LangSmith eval UI | `pkg/eval` in-tree; results print to stdout or render in the dashboard |
| LangSmith dataset + replay | `galdor scry replay <run-id> -o fixture.json` + `replay.NewProvider` |

```go
exporter, _ := observability.NewSQLiteExporter("./traces.db")
tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
p = observability.InstrumentProvider(p, tp.Tracer("agent"),
    observability.WithCaptureContent(true))
```

The same OTel spans pipe to Datadog / Honeycomb / Grafana Tempo
via OTLP if your org runs a shared observability backend. The
SQLite dashboard is for the cases where you don't.

## Multi-agent

| In LangGraph | In galdor |
|---|---|
| `create_supervisor(workers)` | `council.NewSupervisor(...)` |
| swarm via LangGraph patterns | `council.NewSwarm(...)` |
| hierarchical agents | nested Supervisors (a worker can be another supervisor) |

See [multi-agent](../patterns/multi-agent.md).

## MCP

LangChain has first-party MCP integration on both client and
tool-as-server side. galdor has client + server (stdio) in
`pkg/mcp`. See [mcp-server](../patterns/mcp-server.md) for the
Claude Desktop wiring.

## What you give up moving to Go

- The breadth of the LangChain ecosystem. If half your code is
  one-line imports of niche adapters, you'll be writing more
  integration code in Go.
- The Python REPL feedback loop. Go compiles fast, but it's
  still compile-then-run, not eval-as-you-go.
- The hosted LangSmith eval UI. galdor's eval framework is
  in-tree, deterministic, prints results, and can render in the
  embedded dashboard — but it's not the rich hosted experience.
- Dynamic agents constructed at runtime from config blobs.
  galdor's surface is typed; agents are constructed via Go code.
  Config-driven agents are possible but require more setup.

## What you gain

- A single static binary. Drop into a scratch container, ship
  it as a CLI tool, run it air-gapped — no Python runtime, no
  pip install, no virtualenv.
- OTel-native observability. The traces pipe wherever you
  already pipe service traces.
- First-party A2A (Google spec). LangChain doesn't ship it
  natively.
- Strong typing across providers, tools, and graph state.
  Schema mismatches are compile errors.
- A self-hostable dashboard with zero external dependencies.
  The trace store is one SQLite file you can back up, ship, or
  `git diff` between runs.

## When to stay on LangChain Python

- Your team writes Python and switching language is the harder
  problem.
- Your stack depends on integrations galdor doesn't have.
- You're already paying for LangSmith and the hosted eval /
  dataset UI is core to your workflow.

## When to migrate

- You're deploying into infra that can't reach external SaaS
  (compliance, data residency, air-gap).
- You want a single binary you can drop into a container.
- You're already invested in OTel and want agent spans to drop
  into the same pipeline as the rest of your services.
- Your team is more comfortable in Go.
