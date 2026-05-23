# Direct provider usage (no agent, no graph)

Not every LLM call is an agent. Classifiers, extractors, translators, NL-to-DSL converters, single-shot rewriters — these all want one prompt, one response, no loop. galdor's quickstart leads with `agent.Run()` because agents are the more complex case; **this guide is for everyone else**.

If you find yourself reaching for `agent.Run()` only to wire up a single `Provider.Generate` call, stop. This page shows the smaller, more direct path: instantiate a provider, build a request, call it, handle errors, parse output. Around 200 lines of working Go, end to end.

## When to use

| Use direct `Provider.Generate` | Use `agent.Run()` / `pkg/graph` |
|---|---|
| One prompt → one structured response | Multi-step reasoning over tools |
| Classify / extract / translate / summarize | ReAct loops, planner/executor |
| NL-to-DSL, NL-to-SQL, NL-to-CLI | Tool calling with retries on tool errors |
| The shape of the output is fixed by the caller | The model decides what to do next |

Direct usage is what you write when porting a Python script that did `OpenAI().chat.completions.create()` once and returned the parsed result. Don't reach for agents until the model needs to decide.

## End-to-end skeleton

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/YasserCR/galdor/pkg/observability"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
	"github.com/YasserCR/galdor/providers/google"
)

// InterpretResponse is the shape your interpreter promises to return.
// Direct-Provider users own this type; nothing in galdor introspects it.
type InterpretResponse struct {
	Intent string   `json:"intent"`
	Args   []string `json:"args"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Instantiate the provider. Each adapter lives in its own
	//    module under providers/<name>; pull whichever you need.
	inner, err := google.New(google.Config{
		APIKey: os.Getenv("GOOGLE_API_KEY"),
	})
	if err != nil {
		return fmt.Errorf("google.New: %w", err)
	}

	// 2. Compose decorators around the raw provider:
	//    - Retry handles 429 / 5xx transparently.
	//    - InstrumentProvider emits OpenTelemetry spans.
	//    Order matters: instrument the outer (post-retry) view so
	//    your spans see one logical call per Generate, not one
	//    per attempt.
	tracer := otel.Tracer("interpreter")
	p := observability.InstrumentProvider(
		provider.WithDefaultRetry(inner),
		tracer,
		observability.WithCaptureContent(true),
	)

	// 3. Build the request. provider.Request is the lingua franca;
	//    each field is documented in pkg/provider/request.go.
	req := provider.Request{
		Model: "gemini-2.5-flash",
		Messages: []schema.Message{
			schema.SystemMessage("Translate Telegram messages into structured CLI batches. Reply JSON only."),
			schema.UserMessage("buy 42 widgets and notify finance"),
		},
		Temperature: 0,
	}

	// 4. Call Generate and classify errors via the typed wrappers.
	resp, err := p.Generate(ctx, req)
	if err != nil {
		return handle(err)
	}

	// 5. Parse the response text into your shape. ParseJSON tolerates
	//    the common LLM realities (code fences, prose around JSON).
	parsed, err := schema.ParseJSON[InterpretResponse](resp.Message.Text())
	if err != nil {
		return handle(err)
	}

	fmt.Printf("intent=%s args=%v (tokens=%d/%d)\n",
		parsed.Intent, parsed.Args,
		resp.Usage.InputTokens, resp.Usage.OutputTokens)
	return nil
}
```

That's the whole thing. The rest of this page documents each step in more depth.

## Errors: classifying with typed wrappers

`Provider.Generate` returns typed errors that compose with the standard `errors.As` pattern. Reach for `errors.As` first; `errors.Is` against the sentinels is the lower-level fallback when you only need a yes/no.

```go
func handle(err error) error {
	// Rate-limited: server told us when to come back.
	var rl *provider.RateLimitError
	if errors.As(err, &rl) {
		slog.Warn("rate limited",
			"provider", rl.Provider,
			"retry_after_s", rl.RetryAfter,
			"status", rl.StatusCode)
		// Caller can do its own retry here, or just propagate —
		// provider.Retry already handled the in-line case.
		return err
	}

	// Auth: don't retry, surface to operators.
	var ae *provider.AuthError
	if errors.As(err, &ae) {
		slog.Error("auth failed", "provider", ae.Provider, "msg", ae.Message)
		return fmt.Errorf("configuration: %w", err)
	}

	// Context-window: the request itself is too big. Retrying won't help;
	// chunk the input or pick a larger-context model.
	var ce *provider.ContextLengthError
	if errors.As(err, &ce) {
		return fmt.Errorf("input too long for %s: %w", ce.Provider, err)
	}

	// Invalid request: model name typo, contradictory params, etc.
	var ire *provider.InvalidRequestError
	if errors.As(err, &ire) {
		return fmt.Errorf("bad request: %w", err)
	}

	// Bad output from ParseJSON / JSONOf — the call succeeded but the
	// payload couldn't be unmarshaled. Inspect Raw for debugging.
	var bad *schema.BadOutputError
	if errors.As(err, &bad) {
		slog.Warn("model returned non-conforming JSON",
			"provider", bad.Provider,
			"raw", bad.Raw,
			"reason", bad.Reason)
		return err
	}

	// Anything else (transient 5xx that exhausted retries, context
	// cancellation, network I/O) — let it bubble.
	return err
}
```

Available typed errors (all unwrap to `*provider.APIError`):

| Type | Sentinel | Retryable? | Notes |
|---|---|---|---|
| `*RateLimitError` | `ErrRateLimited` | yes | `RetryAfter` in seconds; honored by `provider.Retry` |
| `*TransientError` | `ErrServer` | yes | 5xx-class |
| `*AuthError` | `ErrAuth` | no | 401/403 |
| `*InvalidRequestError` | `ErrInvalidRequest` | no | 4xx malformed |
| `*ContextLengthError` | `ErrContextWindow` | no | request too big |
| `*UnsupportedError` | `ErrUnsupported` | no | feature not in capabilities |

`*schema.BadOutputError` is returned by `schema.ParseJSON[T]` (and the upcoming `schema.JSONOf[T]`) when parsing the *response body* fails. It is not an `APIError`.

## Retry: a decorator, not a config field

galdor composes retry as a wrapper around the inner provider. The defaults are right for almost everyone:

```go
p := provider.WithDefaultRetry(inner) // 3 attempts, 1s→30s exponential, ±25% jitter
```

When you need more control:

```go
p := provider.Retry(inner, provider.RetryPolicy{
	MaxAttempts:  5,
	InitialDelay: 500 * time.Millisecond,
	MaxDelay:     20 * time.Second,
	Multiplier:   2.0,
	Jitter:       0.3,
	OnRetry: func(attempt int, delay time.Duration, err error) {
		slog.Warn("retrying", "attempt", attempt, "delay", delay, "err", err)
	},
})
```

`RetryPolicy` is an alias for `RetryConfig`; both names work. The middleware honors `*RateLimitError.RetryAfter` when the server provides a hint, falling back to the exponential schedule otherwise.

What it does NOT retry: `*AuthError`, `*InvalidRequestError`, `*UnsupportedError`, `*ContextLengthError`. Those don't get better by sleeping.

## Observability: one decorator wires it all

`observability.InstrumentProvider` adds OpenTelemetry spans around every `Generate` and `Stream` call, with GenAI semantic conventions for model name, token counts, and stop reason. It's a separate decorator so you can apply it independently of retry.

```go
tracer := otel.Tracer("my-service") // or any trace.Tracer
p := observability.InstrumentProvider(
	provider.WithDefaultRetry(inner),
	tracer,
	observability.WithCaptureContent(true), // include prompts and completions on the span
)
```

If you've already configured an OTel tracer provider (the package uses the global one by default), spans appear in your collector with the rest of your traces. If not, galdor ships an embedded SQLite exporter; see `docs/patterns/observability.md`.

For compliance-sensitive workloads, capture is opt-in and can be narrowed:

```go
observability.WithCaptureContent(true)   // both prompt and response
// Phase 13 will add WithCapturePrompt and WithCaptureResponse separately.
```

## Testing: scripted in-process provider

`pkg/testprovider` ships a scripted provider for unit tests so you can exercise the interpreter without a network call:

```go
import "github.com/YasserCR/galdor/pkg/testprovider"

func TestInterpret_BuyIntent(t *testing.T) {
	p := testprovider.New(
		testprovider.JSONResponses(InterpretResponse{
			Intent: "buy",
			Args:   []string{"42", "widgets"},
		}),
	)
	got, err := interpret(context.Background(), p, "buy 42 widgets")
	if err != nil {
		t.Fatal(err)
	}
	if got.Intent != "buy" {
		t.Errorf("intent = %q", got.Intent)
	}
}
```

Script error paths the same way:

```go
p := testprovider.New(
	testprovider.Errors(provider.Classify(&provider.APIError{
		Kind: provider.ErrRateLimited, Provider: "test", RetryAfter: 1,
	})),
)
_, err := interpret(ctx, p, "anything")
var rl *provider.RateLimitError
if !errors.As(err, &rl) {
	t.Fatalf("expected *RateLimitError; got %T", err)
}
```

`testprovider.Requests()` returns the captured prompts so you can assert what the interpreter actually sent.

## What this guide intentionally leaves out

- **Tool calling.** If your single-shot call uses tools, you've graduated to the agent loop; read `docs/concepts/agent.md`.
- **Streaming.** Covered in `docs/patterns/streaming.md`. Direct streaming is the same shape as direct `Generate`, just with `Provider.Stream` instead.
- **Multi-provider fallback.** Compose another decorator around your retry-wrapped provider that catches a specific error type and falls back. Five lines; no framework feature needed.
- **Prompt templates.** Coming in Phase 13 (`schema.Template`). For now, `fmt.Sprintf` or `text/template` directly.

## The point

You should be able to read this page, copy the skeleton at the top, and have a production-shaped Go interpreter running in five minutes. Everything more sophisticated than that is opt-in: agents when the model decides, graphs when state shape matters, observability when you need traces. Don't add layers until they earn their keep.
