# Schema

`pkg/schema` is the lingua franca between every other package: providers, tools, agents, graphs, observability and replay all talk in terms of these types. They are deliberately minimal, free of provider specifics, and JSON-friendly so they can be serialized for traces, checkpoints and the wire. If you're going to read one package's source before building on galdor, read this one — everything else just composes these primitives.

## The shape

A `Message` is one entry in a conversation. The `Content` slice carries the body as parts to support multimodal inputs.

```go
type Message struct {
    Role         Role
    Content      []ContentPart
    Name         string
    ToolCalls    []ToolCall
    ToolCallID   string
    CacheControl *CacheControl
}

type Role string

const (
    RoleSystem    Role = "system"
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
    RoleTool      Role = "tool"
)

type ContentPart struct {
    Type  ContentType   // ContentTypeText | ContentTypeImage
    Text  string
    Image *ImageContent
}
```

Roles are normalized across providers — Anthropic's "human" and OpenAI's "user" both map to `RoleUser`, and the system prompt is always `RoleSystem` regardless of whether the provider exposes it as a dedicated field or as the first message. Adapters translate.

Tool calls and tool definitions:

```go
type ToolDef struct {
    Name        string
    Description string
    Schema      json.RawMessage  // JSON Schema document for the input
}

type ToolCall struct {
    ID        string
    Name      string
    Arguments json.RawMessage  // JSON matching ToolDef.Schema
}
```

`Usage` and `StopReason` are returned on every `provider.Response`:

```go
type Usage struct {
    InputTokens         int
    OutputTokens        int
    CacheCreationTokens int
    CacheReadTokens     int
}

type StopReason string

const (
    StopReasonEndTurn      StopReason = "end_turn"
    StopReasonMaxTokens    StopReason = "max_tokens"
    StopReasonToolUse      StopReason = "tool_use"
    StopReasonStopSequence StopReason = "stop_sequence"
    StopReasonRefusal      StopReason = "refusal"
    StopReasonError        StopReason = "error"
)
```

Zero fields on `Usage` mean "not reported by the provider", not "zero tokens used". `Usage.Total()` returns `InputTokens + OutputTokens` — cache tokens are a subset of input tokens and are not added separately.

## Things you do with it

### 1. Build a conversation

The single-text helpers cover the 95% case.

```go
import "github.com/YasserCR/galdor/pkg/schema"

msgs := []schema.Message{
    schema.SystemMessage("You are a helpful assistant."),
    schema.UserMessage("What is the capital of Ecuador?"),
}
```

The full set: `SystemMessage`, `UserMessage`, `AssistantMessage`, `ToolResultMessage(callID, result)`. Each returns a `Message` with a single text `ContentPart`.

### 2. Send a multimodal message

For images, build the `Content` slice yourself:

```go
msg := schema.Message{
    Role: schema.RoleUser,
    Content: []schema.ContentPart{
        schema.TextPart("What's in this image?"),
        schema.ImagePartURL("https://example.com/cat.jpg"),
        // or inline:
        // schema.ImagePartData(bytes, "image/png"),
    },
}
```

`Capabilities.VisionInput` reports whether the provider can handle image parts; `Capabilities.ValidateRequest` rejects the request before the wire call if it can't.

### 3. Read the assistant's reply

```go
resp, _ := p.Generate(ctx, req)

text := resp.Message.Text()           // concatenated text parts
calls := resp.Message.ToolCalls       // nil when the model didn't request tools
stop  := resp.StopReason
usage := resp.Usage
```

`Message.Text()` concatenates every `ContentTypeText` part and skips the rest. Useful for logging and simple consumers; if you need image parts back from a multimodal model, iterate `Message.Content` directly.

### 4. Handle a tool call round-trip

When `resp.StopReason == StopReasonToolUse`, the model has produced one or more `ToolCall` entries in `resp.Message.ToolCalls`. Execute them and append a `ToolResultMessage` per call before the next `Generate`.

```go
results := tool.ExecuteCalls(ctx, registry, resp.Message.ToolCalls)

msgs = append(msgs, resp.Message)                 // assistant's tool-call turn
msgs = append(msgs, tool.AsToolResultMessages(results)...) // results, in the same order
```

The `ToolCallID` on each tool-result message must match the `ID` on the originating call. The `tool` package handles that for you; if you're building responses by hand, mirror the IDs verbatim.

### 5. Hint at prompt caching

For providers that support it (Anthropic today), attach a `CacheControl` directive to the last message of the prefix you want cached. Providers that don't support caching silently ignore the hint.

```go
msgs[0].CacheControl = schema.EphemeralCache()
```

`Capabilities.PromptCaching` reports whether the hint will be honored.

## Gotchas

- **`Content` vs `ToolCalls`.** An assistant message that requests tools may have empty `Content`. Do not assume `len(Content) > 0` on every `RoleAssistant` message.
- **Tool messages need both fields.** A `ToolResultMessage` must set `Role = RoleTool`, populate `ToolCallID`, and have the result as a text part. The `ToolResultMessage` helper does all three.
- **`StopReasonToolUse` is not synthetic.** Adapters only emit it when the model actually requested tools. If you get `StopReasonEndTurn` with `len(ToolCalls) > 0`, that's a bug in the adapter; the cross-provider invariant is that `len(ToolCalls) > 0 ⇒ StopReason == StopReasonToolUse`. Use `provider.ValidateToolCalls(msg)` for stronger assertions on `ID` and `Arguments`.
- **`Role.Valid()` exists.** If you accept role strings from external input, validate them.

## See also

- [Provider](provider.md) — `Request` and `Response` carry these types.
- [Tool](tool.md) — `ToolDef` schemas are generated from Go structs; `ExecuteCalls` returns results you turn into `ToolResultMessage` instances.
- [ADR-001](../adr/ADR-001-foundational-decisions.md) — the rationale for the canonical type set.
- Examples: any example under [`examples/`](../../examples/) — they all import this package.
