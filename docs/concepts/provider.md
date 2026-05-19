# Provider

`pkg/provider` is the abstraction over a concrete LLM backend. Every higher layer in galdor — `pkg/agent`, `pkg/graph` nodes, `pkg/council`, evaluators — talks to a `Provider` rather than to a vendor SDK. Adapters (Anthropic, OpenAI, Google, Bedrock) live in their own Go modules under `providers/<name>/` so the core dependency tree stays small.

## The interface

```go
type Provider interface {
    Name() string
    Capabilities() Capabilities
    Generate(ctx context.Context, req Request) (*Response, error)
    Stream(ctx context.Context, req Request) (StreamReader, error)
}
```

A `Provider` must be safe for concurrent use, must propagate context cancellation to its underlying HTTP requests, and must return one of the package sentinels (`ErrUnsupported`, `ErrInvalidRequest`, `ErrAuth`, `ErrRateLimited`, `ErrServer`, `ErrContextWindow`) wrapped in a `*APIError` when something goes wrong. The rationale and full contract live in [ADR-002](../adr/ADR-002-provider-abstraction-shape.md).

`Request` and `Response` are provider-agnostic and serializable; they trade in `schema.Message`, `schema.ToolDef` and `schema.ToolCall` — see [Schema](schema.md).

```go
type Request struct {
    Model         string
    Messages      []schema.Message
    Tools         []schema.ToolDef
    ToolChoice    ToolChoice
    Temperature   *float64
    TopP          *float64
    MaxTokens     *int
    StopSequences []string
    ResponseFormat *ResponseFormat
    Metadata       map[string]string
}

type Response struct {
    Message     schema.Message
    StopReason  schema.StopReason
    Usage       schema.Usage
    Model       string
    ProviderRaw []byte
}
```

Pointer-typed sampling parameters distinguish "unset, use the provider default" from "explicit zero". `ProviderRaw` is the original wire payload, preserved for trace fidelity and for fields galdor hasn't surfaced yet.

## Things you do with it

### 1. Pick a provider

Each adapter lives in its own module and exposes a `New(Config)` constructor.

```go
import anthropic "github.com/YasserCR/galdor/providers/anthropic"

p, err := anthropic.New(anthropic.Config{APIKey: os.Getenv("ANTHROPIC_API_KEY")})
```

Swap the import path and the `Config` shape to switch vendors — nothing downstream changes:

```go
import openai "github.com/YasserCR/galdor/providers/openai"

p, err := openai.New(openai.Config{APIKey: os.Getenv("OPENAI_API_KEY")})
```

### 2. Talk to an OpenAI-compatible endpoint

The OpenAI adapter accepts a `BaseURL` so it can drive any compatible endpoint — Groq, Together, MiniMax, Mistral, vLLM, ollama-openai, you name it. The credentials live in `APIKey`; the rest of your code doesn't care.

```go
p, err := openai.New(openai.Config{
    APIKey:  os.Getenv("GROQ_API_KEY"),
    BaseURL: "https://api.groq.com/openai/v1",
})
```

The runnable [`examples/provider-interface`](../../examples/provider-interface/) demonstrates the full surface with an in-process stub.

### 3. Generate and stream

`Generate` is the non-streaming entrypoint; `Stream` returns a `StreamReader` you iterate to assemble the response.

```go
resp, err := p.Generate(ctx, provider.Request{
    Model:    "claude-haiku-4-5",
    Messages: []schema.Message{schema.UserMessage("Hello")},
})

sr, err := p.Stream(ctx, req)
defer sr.Close()
for {
    ev, err := sr.Recv(ctx)
    if errors.Is(err, io.EOF) {
        break
    }
    if ev.Type == provider.EventContentDelta {
        fmt.Print(ev.ContentDelta)
    }
}
```

`provider.CollectStream(ctx, sr)` consumes a stream and returns a single `*Response`. Adapters whose underlying transport is stream-only implement `Generate` via this helper.

### 4. Add retry middleware

`provider.Retry` wraps any provider with exponential backoff plus jitter. It respects the server's `Retry-After` header (carried on `*APIError`), never retries auth or invalid-request errors, and is safe for concurrent use.

```go
p = provider.Retry(p, provider.RetryConfig{
    MaxAttempts: 5,
    OnRetry: func(attempt int, delay time.Duration, err error) {
        slog.Warn("retrying", "attempt", attempt, "delay", delay, "err", err)
    },
})
```

Defaults: 3 attempts, 1s initial delay capped at 30s, ×2 multiplier, ±25% jitter. `IsRetryable(err)` is exported if you want to write your own middleware on the same classification.

### 5. Validate request capabilities

`Capabilities.ValidateRequest` catches obvious mismatches before the wire call — tools sent to a non-tool-calling provider, vision parts to a text-only adapter, structured outputs where the provider doesn't support them.

```go
if err := p.Capabilities().ValidateRequest(req); err != nil {
    // returns an error wrapping ErrUnsupported
    return err
}
```

`agent.Config.validate()` runs the same check at construction so misconfigured agents fail at startup instead of at first call. `provider.ValidateToolCalls(msg)` is the symmetric check on a response message: every `ToolCall` has a non-empty `ID`, non-empty `Name`, and `Arguments` that is either empty or syntactically valid JSON.

## Gotchas

- **`Capabilities` is constant.** Adapters compute it once at `New` time; do not depend on it varying across requests.
- **`Usage` zero means "not reported".** Some providers do not surface tokens on streamed responses; do not infer zero usage from zero fields.
- **`Stream` retries only at construction.** Once the `StreamReader` is open, per-frame errors are returned to the caller verbatim — the stream is stateful, blind retries would re-deliver partial output. Wrap higher-level retry logic around your stream consumer if you need it.
- **`APIError` carries `Kind` for `errors.Is`.** Match the sentinels (`ErrRateLimited`, `ErrAuth`, ...) rather than string-comparing the message.
- **`ToolChoiceRequired` only works when `Tools` is set.** The adapter forwards it to the provider; the model is then forced to call at least one tool. `agent.Config.ForceToolUse` lifts this into the agent layer.

## See also

- [Schema](schema.md) — the `Message`, `ToolDef`, `ToolCall` and `Usage` types `Request` and `Response` trade in.
- [Tool](tool.md) — how a `Registry` produces the `[]schema.ToolDef` you put on `Request.Tools`.
- [Observability](observability.md) — `observability.InstrumentProvider` wraps any `Provider` and emits GenAI-conventions OTel spans.
- [ADR-002](../adr/ADR-002-provider-abstraction-shape.md) — the design decisions behind the abstraction.
- Examples: [`provider-interface`](../../examples/provider-interface/), [`observability-trace`](../../examples/observability-trace/).
