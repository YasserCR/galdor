# providers/anthropic

galdor adapter for [Anthropic's Messages API](https://docs.anthropic.com/en/api/messages).

## Install

```bash
go get github.com/YasserCR/galdor/providers/anthropic
```

This is an independent Go module so you only pull it when you actually
use Anthropic. The core galdor module has no Anthropic dependency.

## Usage

```go
import (
    "context"
    "os"

    "github.com/YasserCR/galdor/pkg/provider"
    "github.com/YasserCR/galdor/pkg/schema"
    "github.com/YasserCR/galdor/providers/anthropic"
)

func main() {
    p, err := anthropic.New(anthropic.Config{
        APIKey: os.Getenv("ANTHROPIC_API_KEY"),
    })
    if err != nil {
        panic(err)
    }

    maxTokens := 256
    resp, err := p.Generate(context.Background(), provider.Request{
        Model:     "claude-haiku-4-5",
        MaxTokens: &maxTokens,
        Messages: []schema.Message{
            schema.SystemMessage("Reply briefly."),
            schema.UserMessage("Hello, world!"),
        },
    })
    if err != nil {
        panic(err)
    }
    println(resp.Message.Text())
}
```

## What it covers (Phase 1)

- `Generate` (non-streaming) and `Stream` (SSE) against `/v1/messages`.
- System prompt extraction into Anthropic's dedicated `system` field.
- Tool calls in both directions: `tool_use` in the assistant reply,
  `tool_result` folded onto the trailing user message.
- Vision via `image` content blocks (URL or base64 inline data).
- Prompt caching hints (`schema.EphemeralCache()`) attached to the last
  block of the carrier message.
- Error normalization: 401/403 → `provider.ErrAuth`, 429 →
  `provider.ErrRateLimited` with `RetryAfter`, 5xx → `provider.ErrServer`,
  4xx → `provider.ErrInvalidRequest`. Streaming `event: error` frames
  are surfaced through `StreamReader.Recv`.

## Testing

Unit tests run without network access against an `httptest` server:

```bash
go test -race ./...
```

Integration tests hit the real API. They are gated behind both the
`integration` build tag and the `ANTHROPIC_API_KEY` environment variable
so they never run in CI without explicit credentials.

```bash
export ANTHROPIC_API_KEY=sk-ant-...
go test -tags=integration -race ./...
```

The integration suite uses minimal prompts (`MaxTokens` capped at 32) and
exercises the basic generation path on `claude-haiku-4-5`,
`claude-sonnet-4-6` and `claude-opus-4-7`, plus a streaming smoke test
on Haiku. Expected total cost: well under one US cent per run.

## Configuration knobs

| Field        | Default                       | Purpose                                  |
|--------------|-------------------------------|------------------------------------------|
| `APIKey`     | —                             | Required.                                |
| `BaseURL`    | `https://api.anthropic.com`   | Override for mocks or gateways.          |
| `APIVersion` | `2023-06-01`                  | Sent as `anthropic-version`.             |
| `HTTPClient` | `&http.Client{Timeout: 60s}`  | Replace for custom transports / proxies. |
| `UserAgent`  | empty                         | Appended to the default `user-agent`.    |

## Notes and gotchas

- `Request.ToolChoice = ToolChoiceRequired` maps to Anthropic's
  `tool_choice.type = "any"`.
- `MaxTokens` defaults to 1024 when unset, matching Anthropic's API
  requirement that the field be present.
- `Response.ProviderRaw` carries the original JSON body so trace
  consumers can extract Anthropic-only fields not yet modeled in
  `pkg/schema`.
