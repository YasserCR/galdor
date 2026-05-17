// Package provider defines the abstraction over LLM backends used by
// galdor.
//
// A Provider exposes a uniform Generate / Stream interface across
// heterogeneous backends (Anthropic, OpenAI, Google, Bedrock, Azure,
// Ollama, vLLM, ...). Concrete adapters live in their own Go modules
// under providers/<name>/ so the core remains dependency-light.
//
// The package also provides shared request and response types, a stream
// iterator (StreamReader, Event), normalized error sentinels (ErrAuth,
// ErrRateLimited, ...) and the CollectStream helper that bridges
// streaming and non-streaming consumers.
package provider
