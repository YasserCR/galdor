# providers/openai

galdor adapter for [OpenAI's Chat Completions API](https://platform.openai.com/docs/api-reference/chat).

Because the Chat Completions surface is the *de facto* wire standard for
many providers, the same adapter targets all of them via the `BaseURL`
config field. No second adapter needed for compatible backends.

## Install

```bash
go get github.com/YasserCR/galdor/providers/openai
```

## Usage

```go
import (
    "context"
    "os"

    "github.com/YasserCR/galdor/pkg/provider"
    "github.com/YasserCR/galdor/pkg/schema"
    "github.com/YasserCR/galdor/providers/openai"
)

func main() {
    p, err := openai.New(openai.Config{
        APIKey: os.Getenv("OPENAI_API_KEY"),
    })
    if err != nil {
        panic(err)
    }

    maxTokens := 256
    resp, err := p.Generate(context.Background(), provider.Request{
        Model:     "gpt-4o-mini",
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

### Targeting OpenAI-compatible providers

Most LLM providers expose a Chat Completions endpoint. Point this
adapter at any of them with `BaseURL`:

```go
p, _ := openai.New(openai.Config{
    APIKey:  os.Getenv("MINIMAX_API_KEY"),
    BaseURL: "https://api.minimax.io/v1",
})
resp, _ := p.Generate(ctx, provider.Request{Model: "MiniMax-M1", ...})
```

Known compatible endpoints (consult each provider's docs for the
correct `BaseURL` and model names):

| Provider     | Base URL                                |
|--------------|-----------------------------------------|
| OpenAI       | `https://api.openai.com` *(default)*    |
| Groq         | `https://api.groq.com/openai/v1`        |
| Together AI  | `https://api.together.xyz/v1`           |
| Fireworks    | `https://api.fireworks.ai/inference/v1` |
| MiniMax      | `https://api.minimax.io/v1`             |
| Mistral      | `https://api.mistral.ai/v1`             |
| DeepInfra    | `https://api.deepinfra.com/v1/openai`   |
| Anyscale     | `https://api.endpoints.anyscale.com/v1` |
| OpenRouter   | `https://openrouter.ai/api/v1`          |

Provider-specific behavior (e.g. cache reporting, JSON-mode strictness,
which finish_reason values are emitted) varies; the adapter's error
normalization works on the cross-provider subset that OpenAI's
`error.type` / `error.code` vocabulary covers.

## What it covers (Phase 1)

- `Generate` (non-streaming) and `Stream` (SSE) against
  `/v1/chat/completions`.
- System prompts as `role=system` messages.
- Tool calls in both directions: assistant `tool_calls`, tool results
  as `role=tool` messages with `tool_call_id`.
- Vision via `image_url` content parts (URL or `data:` base64).
- `response_format` for `json_object` and `json_schema` (with
  `strict: true`).
- Error normalization: 401/403 → `provider.ErrAuth`, 429 →
  `provider.ErrRateLimited` with `RetryAfter`, 5xx →
  `provider.ErrServer`, 4xx → `provider.ErrInvalidRequest`,
  `context_length_exceeded` (any status) → `provider.ErrContextWindow`.
- Streaming usage: `stream_options.include_usage = true` is always sent
  so the terminal `EventMessageStop` carries real token counts.

## Testing

Unit tests run without network access against an `httptest` server:

```bash
go test -race ./...
```

Integration tests hit a real backend. They are gated behind the
`integration` build tag and the `OPENAI_API_KEY` environment variable:

```bash
export OPENAI_API_KEY=sk-...
go test -tags=integration -race ./...
```

To target an OpenAI-compatible provider:

```bash
export OPENAI_API_KEY=...                # the provider's key
export OPENAI_BASE_URL=https://...       # the provider's chat-completions endpoint
export OPENAI_MODEL=MiniMax-M1           # an alias accepted by that provider
go test -tags=integration -race ./...
```

The integration suite uses minimal prompts and caps `MaxTokens` at 32,
keeping the cost of a full run well under one US cent.

## Notes and gotchas

- `Capabilities().PromptCaching` is **false** even though OpenAI does
  cache prompt prefixes automatically. The capability flag specifically
  means "`schema.CacheControl` hints are honored", and OpenAI silently
  ignores them. Token reuse is still visible in
  `Usage.CacheReadTokens` when the provider reports
  `prompt_tokens_details.cached_tokens`.
- Some OpenAI-compatible providers do not implement `stream_options`.
  In that case the terminal `EventMessageStop` may carry zero `Usage`;
  the rest of the stream still works.
- `Response.ProviderRaw` carries the original JSON body for trace
  fidelity.
