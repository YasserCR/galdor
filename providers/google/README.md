# providers/google

galdor adapter for [Google's Gemini API](https://ai.google.dev/api/generate-content)
(the AI Studio / Generative Language surface).

## Install

```bash
go get github.com/YasserCR/galdor/providers/google
```

Independent Go module: the core galdor module has zero Google
dependencies. The adapter speaks raw HTTP, no third-party SDK.

## Usage

```go
import (
    "context"
    "os"

    "github.com/YasserCR/galdor/pkg/provider"
    "github.com/YasserCR/galdor/pkg/schema"
    "github.com/YasserCR/galdor/providers/google"
)

func main() {
    p, err := google.New(google.Config{
        APIKey: os.Getenv("GOOGLE_API_KEY"), // AIza... from AI Studio
    })
    if err != nil {
        panic(err)
    }

    maxTokens := 256
    resp, err := p.Generate(context.Background(), provider.Request{
        Model:     "gemini-2.5-flash",
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

- `Generate` against `/v1beta/models/{model}:generateContent`.
- `Stream` against `/v1beta/models/{model}:streamGenerateContent?alt=sse`,
  consumed as SSE.
- System prompts hoisted into the dedicated `systemInstruction` field.
- Tool calls in both directions: assistant `functionCall` parts,
  tool results as `functionResponse` parts on a `user`-role content.
- Vision via `inlineData` blobs (base64 + MIME type).
- `generationConfig` for `temperature`, `topP`, `maxOutputTokens`,
  `stopSequences`, `responseMimeType` and `responseSchema`.
- Error normalization: HTTP status code + canonical `status` field +
  `details[].reason` promotion (so 400 with
  `reason: "API_KEY_INVALID"` correctly maps to `provider.ErrAuth`
  instead of `ErrInvalidRequest`).
- Thinking-model awareness: parts marked `thought: true` are excluded
  from `Message.Content`, but `thoughtsTokenCount` is still added to
  `Usage.OutputTokens` so cost tracking stays accurate.

## What it does not cover yet

- **Vertex AI**: the GCP-authenticated surface (`{region}-aiplatform.googleapis.com/v1`)
  with OAuth/ADC credentials. Reachable today via `BaseURL` override
  plus a custom `HTTPClient` that injects the OAuth token, but no
  first-class config field.
- **Cached content**: callers can construct a `CachedContent` resource
  out-of-band and pass its name through `Request.Metadata`, but the
  adapter doesn't yet manage the resource lifecycle.
- **Grounding / search / code execution**: Gemini-specific tool types
  that don't fit the `schema.ToolDef` shape; tracked for a future
  schema extension.
- **Safety settings overrides**: defaults apply (BLOCK_MEDIUM_AND_ABOVE).
  A future ADR may add a `Request.SafetyPolicy` extension point.
- **Image-by-URL**: Gemini's inline content blocks accept bytes only.
  Passing `schema.ImagePartURL` returns `provider.ErrInvalidRequest` at
  build-request time; fetch the bytes or upload via the File API.

## Function calls — the ID round-trip

Gemini's `functionCall` has no ID: parallel calls are distinguished only
by part order. The adapter synthesizes IDs of the form
`gfc_<index>_<name>` so consumers see a stable identifier in
`schema.ToolCall.ID`. When a `ToolResultMessage` is sent back, the
adapter recovers the function name from the prior assistant
`ToolCall` carried in the request's message history (or, as a fallback,
treats `ToolCallID` itself as the name).

## Testing

Unit tests run without network access against an `httptest` server:

```bash
go test -race ./...
```

Integration tests hit AI Studio. They are gated behind both the
`integration` build tag and the `GOOGLE_API_KEY` environment variable:

```bash
export GOOGLE_API_KEY=AIza...
go test -tags=integration -race ./...
```

The integration suite uses minimal prompts and caps `MaxTokens` at 32,
keeping the cost of a full run effectively zero on the AI Studio free
tier. It exercises `gemini-2.5-flash` and `gemini-2.5-flash-lite` on
basic generation plus a streaming test on Flash plus an auth-failure
test.

## Configuration knobs

| Field        | Default                                                  | Purpose                                  |
|--------------|----------------------------------------------------------|------------------------------------------|
| `APIKey`     | —                                                        | Required. AI Studio key (AIza...).       |
| `BaseURL`    | `https://generativelanguage.googleapis.com/v1beta`       | Override for Vertex AI, proxies, mocks.  |
| `HTTPClient` | `&http.Client{Timeout: 60s}`                             | Custom transports (e.g. OAuth wrapper).  |
| `UserAgent`  | empty                                                    | Appended to the default user-agent.      |

## Notes and gotchas

- **400 means many things** — Google uses HTTP 400 for both genuinely
  malformed requests AND for invalid API keys. Always check
  `errors.Is(err, provider.ErrAuth)` first; the adapter handles the
  promotion internally based on `details[].reason`.
- **`stop_reason` mapping** — Gemini's `STOP` maps to
  `schema.StopReasonEndTurn`. `MAX_TOKENS` to `StopReasonMaxTokens`.
  `SAFETY`, `RECITATION`, `BLOCKLIST`, `PROHIBITED_CONTENT` and `SPII`
  all map to `StopReasonRefusal`. Unknown values are passed through
  lower-cased.
- **Roles** — internally Gemini uses `model`, not `assistant`. The
  adapter translates both directions; consumers always see
  `schema.RoleAssistant`.
- **`Response.ProviderRaw`** — the original JSON body is preserved so
  trace consumers can extract Gemini-only fields (safety ratings,
  prompt feedback, etc.) without the adapter modelling them.
