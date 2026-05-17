// Package provider defines the abstraction for LLM providers used by galdor.
//
// A Provider exposes a uniform interface over heterogeneous backends
// (Anthropic, OpenAI, Google, Bedrock, Azure, Ollama, vLLM, ...). Concrete
// adapters live in their own Go modules under providers/<name>/ so that the
// core remains dependency-light.
//
// Status: stub (Phase 1).
package provider
