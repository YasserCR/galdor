## embedder

`pkg/embedder` is the home for embedder implementations that don't belong inside a vendor provider module. Today it ships one type: `HTTPEmbedder`, a generic HTTP client for self-hosted embedding servers. The package satisfies `memory.Embedder`, so the rest of the memory pipeline (chunker, store, retriever) doesn't change when you swap an OpenAI key for a sidecar.

galdor exposes four ways to produce vectors. Pick by where the model runs:

| Producer | Module | Use when |
|---|---|---|
| `memory.HashingEmbedder` | `pkg/memory` | tests, offline plumbing, examples — lexical only. |
| `providers/openai.NewEmbedder` | `providers/openai` | the literal OpenAI `/v1/embeddings` shape behind an API key (Mistral, MiniMax, Together, Groq, Azure, vLLM-compat). |
| `providers/google.NewEmbedder` | `providers/google` | Gemini `embedContent`. |
| `embedder.NewHTTPEmbedder` | `pkg/embedder` | a self-hosted server you operate (HuggingFace TEI, Infinity, vLLM-embeddings, any custom box on either wire shape). |

The split is intentional: provider packages own credentialed APIs and the surrounding SDK ergonomics (organization headers, rate-limit decoding, model-specific defaults); `embedder` owns the un-credentialed, run-it-yourself case.

## The shape

```go
type Shape string

const (
    ShapeOpenAI Shape = "openai" // POST {URL}/embeddings  {"input":[...], "model":"..."}
    ShapeTEI    Shape = "tei"    // POST {URL}/embed       {"inputs":[...]}
)

type HTTPConfig struct {
    URL        string        // base; per-shape suffix is appended if missing.
    Shape      Shape         // defaults to ShapeOpenAI.
    Model      string        // OpenAI-only.
    APIKey     string        // optional Authorization: Bearer.
    HTTPClient *http.Client  // optional transport override.
    BatchSize  int           // inputs per HTTP call. Defaults to 32.
    Timeout    time.Duration // per-request. Defaults to 60s.
    Dim        int           // reported by Dimensions; sent as "dimensions" for OpenAI.
}

func NewHTTPEmbedder(cfg HTTPConfig) (*HTTPEmbedder, error)

func (e *HTTPEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error)
func (e *HTTPEmbedder) Dimensions() int
func (e *HTTPEmbedder) Ping(ctx context.Context) error
```

`Embed` splits `texts` into batches of `BatchSize` and concatenates the results in input order. `Dimensions` returns the configured `Dim`, or whatever the server returned on the first successful call, or 0. `Ping` is a single-element `Embed("ping")` that discards the vector — use it as a startup probe.

## Two shapes, one client

`ShapeTEI` matches HuggingFace's Text Embeddings Inference. The request body is `{"inputs":["..."]}` against `{URL}/embed`; the response is the flat `[[...], [...]]` array. Infinity and a few other servers expose the same shape.

`ShapeOpenAI` matches the OpenAI `/v1/embeddings` envelope: `{"input":[...], "model":"..."}` against `{URL}/embeddings`, returning `{"data":[{"index":N,"embedding":[...]}, ...]}`. vLLM with OpenAI compatibility, and anything that lists itself as OpenAI-API-compatible, lives here.

URL normalization is forgiving: pass `http://host:8080` for TEI and `/embed` is appended; pass `http://host:8000/v1` for OpenAI and `/embeddings` is appended; passing the full path is a no-op.

## Wiring it into RAG

The drop-in is one line — the rest of the chain is unchanged from any other `Embedder`:

```go
import (
    "github.com/YasserCR/galdor/pkg/embedder"
    "github.com/YasserCR/galdor/pkg/memory"
)

emb, _ := embedder.NewHTTPEmbedder(embedder.HTTPConfig{
    URL:   "http://tei:80",
    Shape: embedder.ShapeTEI,
})
if err := emb.Ping(ctx); err != nil {
    log.Fatal("embeddings sidecar not ready: ", err)
}

retriever := &memory.Retriever{Store: store, Embedder: emb, DefaultK: 5}
hits, _ := retriever.Retrieve(ctx, memory.Query{Text: question})
```

For an OpenAI-compatible self-host:

```go
emb, _ := embedder.NewHTTPEmbedder(embedder.HTTPConfig{
    URL:   "http://vllm:8000/v1/embeddings",
    Model: "BAAI/bge-base-en-v1.5",
})
```

## Errors

Any non-2xx response yields a typed `*EmbedError` carrying `Status`, `URL` and a 512-byte body snippet. Callers can branch:

```go
vecs, err := emb.Embed(ctx, texts)
var ee *embedder.EmbedError
switch {
case errors.As(err, &ee) && ee.Status == http.StatusRequestEntityTooLarge:
    // shrink the batch / truncate inputs and retry at the caller.
case errors.As(err, &ee) && ee.Status == http.StatusUnauthorized:
    // bad APIKey; do not retry.
case err != nil:
    return err
}
```

Retries cover transient server faults — 5xx and 429 — with a capped exponential backoff (100 ms × 2^n + jitter, three attempts). 4xx errors, including 413, are terminal: the server is telling you the request is malformed; retrying without changing the payload won't help.

## Constraints

- **HTTP-only.** No CGO, no ONNX, no bundled model files. The package's job is to talk to a server you already run; running the model in-process is explicitly out of scope.
- **Standard library only.** `net/http`, `encoding/json`, `bytes`, `context`, `errors`, `fmt`, `io`, `time`, `strings`. No third-party HTTP client, no dependency graph beyond `pkg/memory` for the interface.
- **No persistent state.** `HTTPEmbedder` caches the observed dimensionality after the first successful call and nothing else. Concurrent `Embed` calls are safe.

## Gotchas

- **Dimensionality is observed, not declared.** When `Dim` is 0 the value reported by `Dimensions()` is whatever the server returned on the first batch. If your store needs the dim at open time (pgvector, qdrant), set `Dim` explicitly or `Ping` first.
- **413 means shrink the batch.** TEI and Infinity reject oversize batches with 413; this client does not auto-truncate. Either lower `BatchSize`, chunk smaller, or pre-truncate inputs in the caller.
- **`Model` is OpenAI-only.** TEI ignores it — its model is whatever the server was started with. Setting `Model` on a TEI config is harmless but indicates a confused configuration.
- **`Ping` consumes a real inference.** It's not a cheap `/health` probe; it issues `Embed(["ping"])`. Use it once at startup, not in a hot loop.

## See also

- [memory](memory.md) — the `Embedder` interface this package implements, and the rest of the RAG chain.
- [RAG pattern](../patterns/rag.md) — end-to-end walkthrough; the "swapping the embedder for a self-hosted endpoint" section uses this package.
- [`providers/openai`](../../providers/openai/), [`providers/google`](../../providers/google/) — the credentialed alternatives.
