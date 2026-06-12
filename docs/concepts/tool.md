# Tool

`pkg/tool` is galdor's type-safe tool system. A tool is a regular Go function with typed input and output; the JSON Schema published to the LLM is derived from the input struct's reflection metadata at construction time. No magic strings, no `interface{}`, no parallel description maintained alongside the code.

## The shape

```go
type Tool[In any, Out any] interface {
    AnyTool
    Execute(ctx context.Context, input In) (Out, error)
}

type AnyTool interface {
    Name() string
    Description() string
    Schema() *jsonschema.Schema
    ExecuteJSON(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}
```

`Tool[In, Out]` is what you work with in typed call-sites. `AnyTool` is the type-erased view a `Registry` stores and an executor dispatches against — that's the path tool calls coming back from the LLM travel.

The registry is a name-indexed, concurrency-safe collection:

```go
type Registry struct { /* opaque */ }

func NewRegistry(tools ...AnyTool) (*Registry, error)
func (r *Registry) Add(t AnyTool) error
func (r *Registry) Get(name string) (AnyTool, bool)
func (r *Registry) Tools() []AnyTool                  // sorted, snapshot
func (r *Registry) ToolDefs() ([]schema.ToolDef, error)
```

The executor:

```go
type Result struct {
    ID     string
    Name   string
    Output json.RawMessage
    Err    error
}

func ExecuteCalls(ctx context.Context, reg *Registry, calls []schema.ToolCall) []Result
func AsToolResultMessages(results []Result) []schema.Message
```


## Things you do with it

### 1. Define a tool

Annotate fields with `jsonschema` tags to feed the generator. `json` tags drive field naming (and `omitempty` marks fields as not required).

```go
import "github.com/YasserCR/galdor/pkg/tool"

type weatherIn struct {
    City    string `json:"city" jsonschema:"City to look up"`
    Country string `json:"country,omitempty" jsonschema:"ISO-3166 code (optional)"`
    Days    int    `json:"days" jsonschema:"description=Forecast days,format=int"`
}
type weatherOut struct {
    TempC float64 `json:"temp_c"`
    Sky   string  `json:"sky"`
}

weather := tool.MustNewTool("weather", "Look up the weather for a city",
    func(ctx context.Context, in weatherIn) (weatherOut, error) {
        return weatherOut{TempC: 18.5, Sky: "clear"}, nil
    })
```

Tag conventions:

- `jsonschema:"<description>"` — field description.
- `jsonschema:"<description>,format=<fmt>"` — description plus an explicit format hint.
- `jsonschema:"<description>,enum=a;b;c"` — enum-constrained string.
- `json:"name,omitempty"` — field name plus "not required".

`NewTool` returns an error if the schema can't be derived (recursive types, unsupported kinds); `MustNewTool` panics. Use the `Must*` form for package-level vars where a schema failure is a startup-time bug.

### 2. Register and use

```go
reg, err := tool.NewRegistry(weather, otherTool)
if err != nil {
    log.Fatal(err)
}
```

Pass the registry to an agent (`agent.Config.Tools`) or to a provider request directly:

```go
defs, _ := reg.ToolDefs()
resp, _ := p.Generate(ctx, provider.Request{
    Model: "claude-haiku-4-5",
    Messages: msgs,
    Tools: defs,
})
```

### 3. Dispatch tool calls manually

If you're driving the loop by hand (not using `agent.NewReAct`), `ExecuteCalls` runs every call concurrently and preserves input order.

```go
results := tool.ExecuteCalls(ctx, reg, resp.Message.ToolCalls)

msgs = append(msgs, resp.Message)
msgs = append(msgs, tool.AsToolResultMessages(results)...)
```

Errors are surfaced as the result body so the model sees them and can recover. Use the `Result.Err` field directly if you want different handling.

### 4. Use the builtins

`pkg/tool/builtins` ships a small set of safe, conservative tools every agent eventually wants:

```go
import "github.com/YasserCR/galdor/pkg/tool/builtins"

now := builtins.MustNewTimeTool()
math := builtins.MustNewMathTool()

httpGet := builtins.MustNewHTTPGetTool(builtins.HTTPGetOptions{
    AllowedHosts: []string{"docs.example.com"},
    MaxBytes:     1 << 20,
})
fileRead := builtins.MustNewFileReadTool(builtins.FileReadOptions{
    BaseDir:  "/var/data/agent-sandbox",
    MaxBytes: 1 << 20,
})

reg, _ := tool.NewRegistry(now, math, httpGet, fileRead)
```

- `time` — `operation=now|parse|format`, IANA timezone, Go layout strings. Side-effect-free.
- `math` — `op=add|sub|mul|div|mod|pow|sqrt|abs|ln|log10|exp`. Domain errors surfaced.
- `http_get` — GET-only, host-allowlist gated, HTTPS-only by default, body capped at `MaxBytes`.
- `file_read` — reads files under a `BaseDir` confinement, rejects symlinks by default, body capped.

A shell / process-execution tool is intentionally **not** shipped — running arbitrary processes needs a sandboxing scheme that should be opt-in per deployment. Write your own if your environment permits it.

### 5. Bypass a tool's typed surface

If you only have raw JSON (e.g., when implementing a custom protocol adapter) and an `AnyTool`, use `ExecuteJSON`:

```go
out, err := tool.ExecuteJSON(ctx, json.RawMessage(`{"city":"Quito"}`))
```

This is the path `ExecuteCalls` takes internally. Decode errors come back wrapped in `tool.ErrInvalidInput`; `errors.Is(err, tool.ErrInvalidInput)` lets you surface a recoverable retry hint to the model.

## Gotchas

- **Panics are contained.** A tool that dereferences a nil pointer or indexes out of bounds is converted to a `*tool.PanicError` (matchable with `errors.Is(err, tool.ErrPanic)`). The model sees a failed result it can react to; the host process keeps running. Well-written tools return errors, but the safety net is real and you should not rely on tools never panicking.
- **Concurrency is intentional.** `ExecuteCalls` runs every call in its own goroutine. If your tool touches shared mutable state, you own the locking.
- **Order is preserved.** The result slice indexes match the input call slice regardless of which goroutine finished first. `AsToolResultMessages` preserves the same order so the assistant sees results in the order it requested them.
- **Registry returns sorted snapshots.** `Tools()` and `ToolDefs()` are stable, alphabetical, and copies — safe to iterate without holding the registry lock. The provider's `Tools` field will be in the same order across runs.
- **Recursive input types are rejected at construction.** The reflection-based schema generator detects cycles and returns an error rather than producing an infinite document.

## See also

- [Schema](schema.md) — `ToolDef` and `ToolCall` are the wire types tools serialize into.
- [Agent](agent.md) — `agent.Config.Tools` plugs a `*Registry` into the ReAct or Plan-and-Execute loop.
- [MCP server pattern](../patterns/mcp-server.md) — expose a registry over the Model Context Protocol.
- Examples: [`tools-loop`](../../examples/tools-loop/), [`agent-react`](../../examples/agent-react/).
