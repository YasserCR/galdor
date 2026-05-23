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

// Typed error wrappers. Each embeds *APIError so callers can use the
// idiomatic errors.As pattern instead of manually inspecting Kind:
//
//	var rl *provider.RateLimitError
//	if errors.As(err, &rl) {
//	    time.Sleep(time.Duration(rl.RetryAfter) * time.Second)
//	    return retry()
//	}
//
// The embedded *APIError makes errors.As(err, &apiErr) and
// errors.Is(err, sentinel) keep working without changes. Adapters build
// the typed wrapper via Classify; see ADR-012.

// RateLimitError signals the provider throttled the request. RetryAfter
// (inherited from APIError) carries the server-suggested backoff in
// seconds when known; zero means no hint.
type RateLimitError struct{ *APIError }

// Unwrap returns the embedded *APIError so chained errors.As / errors.Is
// continue to traverse into Kind.
func (e *RateLimitError) Unwrap() error { return e.APIError }

// AuthError signals an authentication or authorization failure. Not
// retryable; callers should surface this to operators.
type AuthError struct{ *APIError }

// Unwrap returns the embedded *APIError.
func (e *AuthError) Unwrap() error { return e.APIError }

// InvalidRequestError signals a 4xx-class failure caused by malformed
// or contradictory request fields (unknown model, bad schema, ...).
// Not retryable as-is.
type InvalidRequestError struct{ *APIError }

// Unwrap returns the embedded *APIError.
func (e *InvalidRequestError) Unwrap() error { return e.APIError }

// TransientError signals a 5xx-class provider failure that may succeed
// on retry. Distinct from RateLimitError so callers can apply different
// backoff policies per kind.
type TransientError struct{ *APIError }

// Unwrap returns the embedded *APIError.
func (e *TransientError) Unwrap() error { return e.APIError }

// ContextLengthError signals the assembled request exceeded the
// provider's context window. Retrying without changing the request will
// fail the same way.
type ContextLengthError struct{ *APIError }

// Unwrap returns the embedded *APIError.
func (e *ContextLengthError) Unwrap() error { return e.APIError }

// UnsupportedError signals the request asked for a capability the
// provider does not implement (e.g. tool calls on a model that lacks
// them). Callers should gate via Capabilities() to avoid this; the
// error is the fallback when the gate is missed.
type UnsupportedError struct{ *APIError }

// Unwrap returns the embedded *APIError.
func (e *UnsupportedError) Unwrap() error { return e.APIError }

// Classify wraps an *APIError in the typed struct corresponding to its
// Kind. Adapters call this at the failure boundary instead of returning
// a raw *APIError so callers get the ergonomic errors.As shape.
//
// When apiErr is nil, Classify returns nil. When Kind is nil or does
// not match a known sentinel, the *APIError is returned unchanged —
// this is intentionally defensive: an adapter that forgets to set Kind
// still produces a usable error.
func Classify(apiErr *APIError) error {
	if apiErr == nil {
		return nil
	}
	// errors.Is comparisons (rather than a switch on apiErr.Kind) keep
	// errorlint quiet: Kind is typed as error so a bare switch trips
	// the "switch on an error fails on wrapped errors" heuristic, even
	// though Kind here is always a bare sentinel produced by the
	// adapter immediately above. Behavior is identical.
	switch {
	case errors.Is(apiErr.Kind, ErrRateLimited):
		return &RateLimitError{APIError: apiErr}
	case errors.Is(apiErr.Kind, ErrAuth):
		return &AuthError{APIError: apiErr}
	case errors.Is(apiErr.Kind, ErrInvalidRequest):
		return &InvalidRequestError{APIError: apiErr}
	case errors.Is(apiErr.Kind, ErrServer):
		return &TransientError{APIError: apiErr}
	case errors.Is(apiErr.Kind, ErrContextWindow):
		return &ContextLengthError{APIError: apiErr}
	case errors.Is(apiErr.Kind, ErrUnsupported):
		return &UnsupportedError{APIError: apiErr}
	default:
		return apiErr
	}
}

// BadOutputError signals that the provider returned successfully but
// the response body could not be parsed into the expected shape.
//
// Lives in pkg/schema (not here) so that schema.ParseJSON[T] and
// future schema.JSONOf[T] can return it without forcing a circular
// import (pkg/provider imports pkg/schema). See pkg/schema for the
// definition.
