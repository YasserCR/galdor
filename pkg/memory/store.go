package memory

import (
	"context"
	"errors"
)

// Store is the long-term memory interface. Implementations may be
// purely lexical (BM25), purely vector (pgvector, qdrant) or hybrid
// (SQLite + BM25 with optional embeddings).
//
// Add ingests chunks. Retrieve returns the top-K results for a query
// in descending relevance order. Delete removes the chunks belonging
// to a document; implementations that don't support deletion return
// ErrUnsupported. Close releases store-owned resources (DB handles,
// gRPC clients) and is safe to call multiple times.
type Store interface {
	Add(ctx context.Context, chunks []Chunk) error
	Retrieve(ctx context.Context, q Query) ([]Result, error)
	Delete(ctx context.Context, documentID string) error
	Close() error
}

// Embedder turns text into dense vectors. Implementations wrap
// provider-specific embedding APIs (OpenAI, Google, Cohere, ...) or
// local models. Embed must preserve input order: out[i] is the
// vector for texts[i].
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions reports the vector size produced by Embed. Stores
	// use it to allocate columns / validate inputs.
	Dimensions() int
}

// Retriever is a convenience wrapper that composes an Embedder and a
// Store: callers pass a Query with only Text set, Retriever fills in
// Embedding before delegating to the underlying Store. Use it when
// the calling code shouldn't know whether the backend is lexical or
// vector-based.
type Retriever struct {
	Store    Store
	Embedder Embedder
	DefaultK int
}

// Retrieve runs q through the configured Embedder (when Text is set
// and Embedding is not) and forwards the result to Store.Retrieve.
// When Embedder is nil, Retrieve forwards the query unchanged.
func (r *Retriever) Retrieve(ctx context.Context, q Query) ([]Result, error) {
	if r.Store == nil {
		return nil, errors.New("memory: Retriever.Store is nil")
	}
	if r.Embedder != nil && len(q.Embedding) == 0 && q.Text != "" {
		vecs, err := r.Embedder.Embed(ctx, []string{q.Text})
		if err != nil {
			return nil, err
		}
		if len(vecs) == 1 {
			q.Embedding = vecs[0]
		}
	}
	if q.K <= 0 {
		q.K = r.DefaultK
	}
	return r.Store.Retrieve(ctx, q)
}

// ErrUnsupported is returned by Store implementations for operations
// they do not implement (typically Delete on append-only backends).
var ErrUnsupported = errors.New("memory: operation not supported by this Store")
