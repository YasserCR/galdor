package provider

import "errors"

// Sentinel errors returned by Provider implementations. Callers match them
// with errors.Is so adapters can wrap them with context-specific detail.
//
//	if errors.Is(err, provider.ErrRateLimited) { ... }
var (
	// ErrUnsupported is returned when a method or option is not supported
	// by the underlying provider (e.g., Stream on a non-streaming
	// provider, or Tools when Capabilities.ToolCalling is false).
	ErrUnsupported = errors.New("operation not supported by provider")

	// ErrInvalidRequest is returned when the Request fails provider-side
	// validation (unknown model, bad schema, contradictory parameters).
	ErrInvalidRequest = errors.New("invalid request")

	// ErrAuth is returned for authentication or authorization failures.
	ErrAuth = errors.New("authentication failed")

	// ErrRateLimited is returned when the provider throttles the caller.
	// Adapters should set RetryAfter on the wrapped APIError when known.
	ErrRateLimited = errors.New("rate limited")

	// ErrServer is returned for 5xx-class provider failures that may be
	// transient.
	ErrServer = errors.New("provider server error")

	// ErrContextWindow is returned when the assembled request exceeds the
	// provider's context window.
	ErrContextWindow = errors.New("context window exceeded")
)

// APIError wraps a provider-side failure with normalized fields. Adapters
// build it once at the failure boundary so callers receive uniform errors
// regardless of provider.
//
// Use errors.As(err, &apiErr) to inspect, or errors.Is(err, sentinel) to
// classify by kind.
type APIError struct {
	// Kind matches one of the sentinel errors above (ErrAuth, ErrRateLimited,
	// ...). It is what errors.Is reports.
	Kind error

	// Provider is the adapter name that produced the error (e.g. "anthropic").
	Provider string

	// StatusCode is the HTTP status from the provider, when available.
	StatusCode int

	// Message is the provider's human-readable error message, when surfaced.
	Message string

	// RetryAfter is the server-suggested backoff for ErrRateLimited, in
	// seconds. Zero when unknown.
	RetryAfter int
}

// Error implements the error interface.
func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	prefix := e.Provider
	if prefix == "" {
		prefix = "provider"
	}
	if e.Message != "" {
		return prefix + ": " + e.Message
	}
	if e.Kind != nil {
		return prefix + ": " + e.Kind.Error()
	}
	return prefix + ": unknown error"
}

// Unwrap exposes the Kind so errors.Is matches the sentinels.
func (e *APIError) Unwrap() error { return e.Kind }
