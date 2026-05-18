# providers/bedrock

galdor adapter for [AWS Bedrock](https://aws.amazon.com/bedrock/) via the
unified [Converse API](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html).

## Install

```bash
go get github.com/YasserCR/galdor/providers/bedrock
```

Independent Go module: galdor's core stays dependency-free. This module
pulls AWS SDK Go v2 (`aws`, `config`, `service/bedrockruntime`,
`smithy-go`) — only callers that actually want Bedrock take the
dependency.

## Why the AWS SDK, when other adapters use raw HTTP

The other galdor adapters (Anthropic, OpenAI, Google) speak the wire
protocol directly because it's small and readable. Bedrock is different:

- **SigV4 signing** with credential chain resolution (env vars,
  `~/.aws/credentials`, IAM role, SSO, EC2/ECS metadata) is non-trivial
  to do safely by hand.
- **Streaming** uses the AWS Event Stream binary framing
  (`vnd.amazon.eventstream`), not SSE; the SDK already parses it.
- **Per-model wire** differences across vendors (Claude/Nova/Llama/...)
  are abstracted by the Converse API, but the SDK is what surfaces
  them as typed Go values.

Re-implementing all of the above buys very little and risks subtle bugs.

## Usage

```go
import (
    "context"

    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/YasserCR/galdor/pkg/provider"
    "github.com/YasserCR/galdor/pkg/schema"
    "github.com/YasserCR/galdor/providers/bedrock"
)

func main() {
    ctx := context.Background()
    awsCfg, err := config.LoadDefaultConfig(ctx,
        config.WithRegion("us-east-1"),
    )
    if err != nil {
        panic(err)
    }

    p, err := bedrock.New(bedrock.Config{AWS: awsCfg})
    if err != nil {
        panic(err)
    }

    maxTokens := 256
    resp, err := p.Generate(ctx, provider.Request{
        Model:     "anthropic.claude-3-5-haiku-20241022-v1:0",
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

The `Model` field carries the Bedrock model ID, an inference-profile
ID, or the ARN of either — whatever AWS Bedrock accepts, galdor
forwards unchanged.

## What it covers

- `Generate` via Bedrock Converse: system prompts hoisted into the
  dedicated `System` field, multimodal text+image inputs, tool calls
  in both directions, structured response handling.
- `Stream` via Bedrock ConverseStream: typed event mapping for
  `MessageStart`, `ContentBlockStart`, `ContentBlockDelta`,
  `ContentBlockStop`, `MessageStop` and `Metadata`. `MessageStop` is
  deferred until after `Metadata` so the terminal galdor event carries
  final `Usage`.
- Tool calls: assistant `toolUse` blocks round-trip as
  `schema.ToolCall`; tool results fold onto a trailing `user` message
  as `toolResult` blocks. `ToolChoiceNone` is implemented by dropping
  the tool config entirely (Bedrock has no native "none" mode).
- Vision: inline image bytes only (PNG, JPEG, GIF, WebP). URL-based
  images are rejected at build-request time because Bedrock requires
  inline bytes.
- Error normalization: typed Bedrock exceptions
  (`ValidationException`, `AccessDeniedException`, `ThrottlingException`,
  `ServiceQuotaExceededException`, `InternalServerException`,
  `ModelStreamErrorException`, etc.) map to the galdor sentinels
  (`ErrInvalidRequest`, `ErrAuth`, `ErrRateLimited`, `ErrServer`).
  Unknown error codes fall back to the smithy `APIError` interface,
  with `ErrServer` as the safe default.

## Configuration knobs

| Field            | Purpose                                                                                  |
|------------------|------------------------------------------------------------------------------------------|
| `AWS`            | Required. `aws.Config` carrying region, credentials and HTTP client.                     |
| `ClientOptions`  | Optional. Functional options applied to the underlying `bedrockruntime.Options`.         |

Use `config.LoadDefaultConfig(ctx)` from `aws-sdk-go-v2/config` to build
`AWS` with the standard credential chain. For custom HTTP transports
(proxies, debugging middleware) pass them via `aws.Config.HTTPClient`.
For per-call endpoint overrides (private endpoints, mocks) use
`ClientOptions` to set `BaseEndpoint`.

## Testing

Unit tests run without network access. They use `httptest` plus the
SDK's `BaseEndpoint` override for `Generate`, and synthetic typed events
fed through a channel for `Stream` (the AWS Event Stream binary framing
makes end-to-end stream testing via `httptest` impractical, so the
SDK-side decode boundary is the test seam).

```bash
go test -race ./...
```

Coverage at the time of writing: **82.6%** of statements.

## Integration tests

End-to-end tests against real Bedrock are gated behind:

1. The `integration` build tag.
2. AWS credentials resolvable by the default chain
   (`AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY`, or `AWS_PROFILE`).
3. A configured region (`AWS_REGION` or via the resolved profile).

```bash
export AWS_REGION=us-east-1
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
# optional: pin the model used for the test suite
export BEDROCK_TEST_MODEL_ID=anthropic.claude-3-5-haiku-20241022-v1:0
go test -tags=integration -race ./providers/bedrock/...
```

Prompts are minimal and `MaxTokens` is capped at 32 so a full
integration run costs a fraction of a cent on the cheapest Claude
Haiku tier.

### Status

The adapter compiles, passes all unit tests, and is shaped against the
SDK and Converse API exactly as documented by AWS. **End-to-end
compatibility tests against a live Bedrock account are still pending**
— they will be run before this README's status line moves from
`pending validation` to `verified`. Until then, treat the adapter as
implementation-complete but not yet field-verified.

If an end-to-end run reveals an issue (most likely candidates: a wire
field that the SDK names slightly differently from what we map, or a
model-specific quirk in `stopReason`), a follow-up fix lands and the
status is updated.

## Notes and gotchas

- **Anonymous credentials don't work for Bedrock.** Unlike some other
  AWS services, Bedrock always requires real credentials. Tests in
  this module use static dummy credentials (`AKIAEXAMPLE` /
  `secret/dummy`) so the SDK is happy to sign the request and the
  test server can ignore the signature.
- **Model IDs.** Bedrock distinguishes between *on-demand* model IDs
  (`anthropic.claude-3-5-haiku-20241022-v1:0`) and *inference profile*
  IDs (`us.anthropic.claude-3-5-haiku-20241022-v1:0`, with a region
  prefix). Either form works as long as it's the same one your
  account has access to.
- **`Response.ProviderRaw`** carries a JSON-serialized form of the
  SDK's decoded response (the raw HTTP body isn't exposed by the SDK).
  Trace consumers get a stable, machine-readable form; the exact wire
  bytes are not preserved.
