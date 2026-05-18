# ADR-004 — Tool system shape

- **Status:** Accepted
- **Date:** 2026-05-18
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

ADR-002 settled the provider abstraction and explicitly punted the
typed tool surface to a future ADR: `pkg/tool` was always going to need
its own. Phase 2 builds the tool runtime. Several design choices have
to be made now because they ripple into the graph runtime (Phase 3),
the eval framework (Phase 8), and every adapter that already exists.

The questions this ADR answers:

1. How do tools expose their input shape — runtime values, hand-written
   schemas, or schemas derived from Go types?
2. Generics or strings? `Tool[In, Out]` or `Tool` with `any` for
   inputs and outputs?
3. How do callers store heterogeneous tools in a registry when each
   has different type parameters?
4. What happens on bad input from the model? On a panicking tool? On
   context cancellation mid-dispatch?
5. How do tool results round-trip back to the assistant — what format
   does the agent put in the next `Request.Messages`?

## Decisions

### D1. Tool inputs are typed Go structs; schemas are reflected from them

A tool is declared with a typed input:

```go
type WeatherIn struct {
    City string `json:"city" jsonschema:"City to look up"`
}
type WeatherOut struct {
    TempC int `json:"temp_c"`
}

wx, _ := tool.NewTool("weather", "Look up the weather",
    func(ctx context.Context, in WeatherIn) (WeatherOut, error) {
        return WeatherOut{TempC: 21}, nil
    })
```

`internal/jsonschema` walks the input type at `NewTool` time and
produces the JSON Schema document the LLM sees. Hand-built schemas are
still possible — the implementation accepts a `*jsonschema.Schema`
internally — but the supported path is "write a struct, let the
framework do the rest". This is the model
[Python LangChain](https://python.langchain.com), [pydantic-ai](https://ai.pydantic.dev),
and the OpenAI / Anthropic SDKs converged on, and it pays for itself
the first time a tool grows a third field and you'd otherwise have to
update both the schema and the decoder.

### D2. Generics on the typed surface, type-erased interface for registries

`Tool[In, Out]` carries the input and output types through `Execute`,
so callers using a tool directly get compile-time safety. But a
registry has to hold tools with different parametrizations side by
side, which Go generics cannot express. The resolution is two
interfaces:

```go
type Tool[In, Out any] interface {
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

Every concrete `Tool[In, Out]` also satisfies `AnyTool`. Registries,
the executor, and provider adapters all consume the erased
`ExecuteJSON` path because they only ever see `schema.ToolCall.Arguments`
— a `json.RawMessage`. Typed `Execute` exists for the cases where the
caller actually has values of the right Go type in hand (unit tests,
chained tools, hand-stitched workflows).

### D3. `NewTool` and `MustNewTool`

`NewTool` returns `(Tool[In, Out], error)` so a schema generation
failure (recursive type, unsupported kind) doesn't crash. `MustNewTool`
panics on the same conditions — appropriate for package-level `var`
declarations where a schema generation failure is a programmer error
caught at startup. The pattern mirrors `regexp.MustCompile` /
`template.Must` and removes friction in the common "I know my struct
is fine" case.

### D4. The reflection-derived schema follows JSON Schema 2020-12, minimally

`internal/jsonschema` emits the subset of JSON Schema that providers
consume: `type`, `properties`, `required`, `items`, `enum`, `format`,
`description`, `additionalProperties`. Recursion via `*Self` is
detected and rejected (no `$ref` / `$defs` for now); polymorphism
constructs (`oneOf`, `allOf`, `anyOf`) are out of scope. If callers
need them they can hand-build a `*jsonschema.Schema` and use the lower-
level `tool.Tool` constructor (not yet exposed; future change if
demand exists).

Field naming is taken from `json:"..."` tags so the schema and the
encoding/json behavior cannot diverge. `omitempty` on a field marks it
optional in JSON Schema's `required` array; pointer fields are
similarly optional. Description and format hints live in a separate
`jsonschema:"..."` tag with a simple comma-separated grammar
(`jsonschema:"<description>,format=<fmt>,enum=a;b;c"`).

### D5. The executor runs tool calls concurrently, preserving input order

`tool.ExecuteCalls(ctx, registry, calls)` fans out one goroutine per
call and waits for all of them with `sync.WaitGroup`. The returned
`[]Result` matches the input order so callers can zip results with
calls or feed them back to the LLM in the same order they were
emitted (some providers — Anthropic in particular — require this).

Cancellation: a canceled context short-circuits unstarted calls (they
return `ctx.Err()`) and propagates into running tool functions via
their own context. `ExecuteCalls` always `Wait()`s before returning,
so callers never observe a goroutine leak even when they cancel
aggressively.

### D6. Bad input fails the tool call, never the dispatch loop

When the LLM emits JSON that doesn't decode into the tool's `In` type,
`ExecuteJSON` returns `ErrInvalidInput` (a sentinel). The result is
captured in `Result.Err`, the rest of the batch keeps running, and
`AsToolResultMessages` produces an `"error: ..."` tool-result
message that the model can read and recover from. The caller can also
match the sentinel and decide to abort.

The same pattern applies to unknown tool names (`ErrUnknownTool`) and
to errors returned by the tool's own function. Distinguishing them
matters because "unknown tool" usually means the model hallucinated a
name and should self-correct, while a tool-internal error often means
the *world* failed and the agent may need a different strategy.

### D7. Tool results round-trip via `schema.ToolResultMessage`

`tool.AsToolResultMessages(results)` returns a `[]schema.Message`
ready to append to the next `Request.Messages`. Each result becomes
one tool-role message keyed by `ToolCallID`. Adapters already know
how to translate that into their provider-specific wire shape
(Anthropic's `tool_result` content block; OpenAI's `role=tool` message;
Gemini's `functionResponse` part; Bedrock's `toolResult`). The agent
code stays provider-agnostic.

The text body is the tool output's JSON, or `"error: <message>"` on
failure. Empty outputs become `"null"`. This is intentionally not
clever — model-readable JSON is good enough; structured tool-result
metadata can come later if needed (probably via a future ADR).

### D8. `internal/jsonschema` is internal on purpose

The schema generator's API will evolve: more tag options, possible
`$ref` support, maybe pluggable type hooks. Marking it `internal/`
keeps it private until those decisions settle. Callers who need a
schema for a custom purpose should re-export the surface via
`pkg/tool.Tool.Schema()` instead.

## Consequences

**Positive.** The tool surface is small (one constructor, one
registry, one executor) and works the same against every provider
adapter. The end-to-end loop in `examples/tools-loop` is ~150 lines of
clearly-segmented code, half of which is the scripted stub provider.
JSON Schema generation is automatic for the 99% case while still
allowing escape hatches for the 1%. Coverage on the new packages is
high (jsonschema 91.3%, tool 96.0%).

**Negative.** Generics + interface erasure (D2) trade compile-time
ergonomics on registries for runtime polymorphism. Users have to
remember that the registry sees `AnyTool`, not their `Tool[In, Out]`.
The reflection-derived schema (D1) cannot express polymorphic union
types or unbounded recursion — callers with that need have to either
hand-write a `*jsonschema.Schema` or normalize their input type.

## Out of scope

- **Built-in tools** (http, file read, shell, math, time, etc.) — the
  next session of Phase 2 lands them under `pkg/tool/builtins/`.
- **MCP-bridged tools** — Phase 7 wires `pkg/mcp` clients to produce
  `AnyTool`-conforming wrappers, so MCP servers act as drop-in tool
  sources.
- **Streaming tool outputs** — the current contract is one
  `json.RawMessage` per call. If a tool needs to stream large outputs
  back to the model, a future ADR can extend `AnyTool` with a
  streaming variant.
- **Tool sandboxing / permissions** — already deferred to ADR-008
  per ADR-001.

## References

- ADR-001 §D2 (Go floor, see also ADR-003), §D15 (`context.Context`
  universality), §D14 (no panics outside `init`).
- ADR-002 — provider abstraction.
- `internal/jsonschema/reflect.go`, `pkg/tool/tool.go`,
  `pkg/tool/registry.go`, `pkg/tool/executor.go`.
- `examples/tools-loop/` — runnable end-to-end demo.
