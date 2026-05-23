package schema

// BadOutputError signals that an LLM response could not be parsed into
// the expected shape. It is distinct from provider.APIError because it
// has no HTTP/transport context — it describes a *content* failure,
// not a provider-side failure: the API call succeeded, the body just
// wasn't what the caller asked for.
//
// Returned by ParseJSON[T], and by future schema-bound JSONOf[T]
// paths when the provider claims schema support but emits
// non-conforming output.
//
// Lives in pkg/schema (not pkg/provider) so it can be referenced by
// schema-side helpers without forcing a circular import — pkg/provider
// already imports pkg/schema for Message and ToolDef.
type BadOutputError struct {
	// Provider is the adapter name that produced the bad output, or
	// "schema" when the failure originated in a parser called directly
	// by the user.
	Provider string

	// Raw is the original bytes/text that failed to parse. Capped to a
	// sane size by the producer to avoid huge error strings.
	Raw string

	// Reason is a short human-readable description of what went wrong
	// ("invalid JSON", "missing required field 'name'", ...).
	Reason string

	// Cause is the wrapped underlying error when one exists
	// (e.g. *json.SyntaxError). May be nil.
	Cause error
}

// Error implements the error interface.
func (e *BadOutputError) Error() string {
	if e == nil {
		return "<nil>"
	}
	prefix := e.Provider
	if prefix == "" {
		prefix = "schema"
	}
	if e.Reason != "" {
		return prefix + ": bad output: " + e.Reason
	}
	return prefix + ": bad output"
}

// Unwrap exposes the underlying cause for errors.Is / errors.As.
func (e *BadOutputError) Unwrap() error { return e.Cause }
