// Package embedder provides a generic HTTP client for self-hosted
// embedding services.
//
// galdor ships three other ways to produce vectors and one of them is
// almost certainly what you want first:
//
//   - providers/openai.NewEmbedder — OpenAI's hosted /v1/embeddings,
//     plus any third-party endpoint that mimics the same payload
//     exactly (Mistral, MiniMax, Together, Groq, Azure, vLLM with
//     OpenAI compatibility on). Use this when you already pay for an
//     API key and the request body is the literal OpenAI shape.
//   - providers/google.NewEmbedder — Gemini's embedContent endpoint.
//     Same reasoning as the openai package but for Google.
//   - memory.HashingEmbedder — deterministic, network-free,
//     lexical-only. For tests, examples and offline plumbing; not
//     comparable to a trained model.
//
// embedder.HTTPEmbedder is the fourth option: an HTTP client that
// talks to a self-hosted embedding server without imposing an SDK,
// without CGO, and without bundling any model files. It supports two
// wire shapes — the OpenAI /v1/embeddings JSON envelope and the
// HuggingFace Text Embeddings Inference (TEI) flat-array shape — so
// it covers TEI, Infinity, vLLM-embeddings, and any other server you
// can point at one of those URLs.
//
// Pick HTTPEmbedder when you run inference yourself (cost, latency,
// data-residency, or model-choice reasons) and you don't want to
// hand-roll the HTTP plumbing each time. Pick a provider package when
// the model lives behind someone else's API key.
package embedder
