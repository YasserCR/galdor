// Package provider defines the abstraction over LLM backends used by
// galdor.
//
// A Provider exposes a uniform Generate / Stream interface across
// heterogeneous backends (Anthropic, OpenAI, Google, Bedrock, Azure,
// Ollama, vLLM, ...). Concrete adapters live in their own Go modules
// under providers/<name>/ so the core remains dependency-light.
//
// # Errors
//
// Adapters return typed wrappers around *APIError so callers can use
// the idiomatic errors.As pattern:
//
//	var rl *provider.RateLimitError
//	if errors.As(err, &rl) {
//	    time.Sleep(time.Duration(rl.RetryAfter) * time.Second)
//	    return retry()
//	}
//
// errors.Is(err, ErrRateLimited) and errors.As(err, &apiErr) keep
// working unchanged via the Unwrap chain.
//
// # Retry
//
// Retry is composed as a decorator, not built into adapter config:
//
//	inner, _ := google.New(google.Config{APIKey: key})
//	p := provider.Retry(inner, provider.RetryPolicy{MaxAttempts: 3})
//
// For the common case where the defaults are fine:
//
//	p := provider.WithDefaultRetry(inner)
//
// The middleware classifies via IsRetryable (rate-limit + transient
// 5xx are retried; auth, invalid-request, context-window are not) and
// respects APIError.RetryAfter when the server provides a hint.
//
// # Shared types
//
// The package also provides shared request and response types, a stream
// iterator (StreamReader, Event), normalized error sentinels (ErrAuth,
// ErrRateLimited, ...) and the CollectStream helper that bridges
// streaming and non-streaming consumers.
package provider
