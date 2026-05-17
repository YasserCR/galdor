package provider

import "context"

// Provider is the abstraction over a concrete LLM backend (Anthropic,
// OpenAI, Google, Bedrock, Azure, Ollama, vLLM, ...).
//
// Implementations live in independent Go modules under providers/<name>/
// so the core stays dependency-light. A Provider must be safe for
// concurrent use by multiple goroutines.
//
// Cancellation: every blocking method accepts a context.Context. Adapters
// MUST propagate cancellation to in-flight HTTP requests and partial
// stream reads.
type Provider interface {
	// Name returns the adapter identifier (e.g. "anthropic", "openai").
	// It is stable across versions and used in trace attributes.
	Name() string

	// Capabilities reports what the provider can do. The value is
	// constant for the lifetime of the Provider.
	Capabilities() Capabilities

	// Generate executes a single non-streaming generation. Adapters that
	// only expose a streaming transport may implement this by consuming
	// their own stream via CollectStream.
	Generate(ctx context.Context, req Request) (*Response, error)

	// Stream executes a streaming generation. Adapters that do not
	// support streaming return ErrUnsupported.
	Stream(ctx context.Context, req Request) (StreamReader, error)
}
