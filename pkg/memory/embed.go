package memory

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"strings"
)

// EmbedderFunc adapts a plain function to the Embedder interface.
// The Dim field declares the embedding dimensionality so Dimensions()
// can satisfy the interface.
type EmbedderFunc struct {
	Dim int
	Fn  func(ctx context.Context, texts []string) ([][]float32, error)
}

// Embed implements Embedder.
func (e EmbedderFunc) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.Fn(ctx, texts)
}

// Dimensions implements Embedder.
func (e EmbedderFunc) Dimensions() int { return e.Dim }

// HashingEmbedder is a deterministic, network-free Embedder useful
// for tests, examples and offline development. It hashes each
// whitespace-separated token of the input into a fixed-size float32
// vector with the "feature hashing" / "hashing trick" technique used
// in scikit-learn's HashingVectorizer.
//
// Quality is not comparable to a trained embedding model — the
// vectors capture lexical overlap, not semantic similarity. Use it
// to wire up RAG plumbing end-to-end without depending on a paid
// API; swap in a real embedder (OpenAI text-embedding-3-small,
// Cohere, Voyage, ...) when running for real.
type HashingEmbedder struct {
	// Dim is the embedding dimensionality. 256 is a reasonable
	// default for small corpora.
	Dim int
}

// NewHashingEmbedder returns a HashingEmbedder with the given
// dimensionality (must be > 0).
func NewHashingEmbedder(dim int) HashingEmbedder {
	if dim <= 0 {
		dim = 256
	}
	return HashingEmbedder{Dim: dim}
}

// Embed implements Embedder.
func (h HashingEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = h.embedOne(t)
	}
	return out, nil
}

// Dimensions implements Embedder.
func (h HashingEmbedder) Dimensions() int {
	if h.Dim <= 0 {
		return 256
	}
	return h.Dim
}

func (h HashingEmbedder) embedOne(text string) []float32 {
	dim := h.Dimensions()
	vec := make([]float32, dim)
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return vec
	}
	for _, tok := range tokens {
		sum := sha256.Sum256([]byte(tok))
		// Use the first 4 bytes for the bucket index and the next 4
		// bytes for the sign — emulates signed feature hashing.
		bucket := binary.BigEndian.Uint32(sum[:4]) % uint32(dim) // #nosec G115 -- dim is validated > 0 in Dimensions()
		sign := float32(1)
		if sum[4]&1 == 0 {
			sign = -1
		}
		vec[bucket] += sign
	}
	// L2-normalize so cosine reduces to a dot product and so vectors
	// from documents of different lengths remain comparable.
	var sumSq float64
	for _, v := range vec {
		sumSq += float64(v) * float64(v)
	}
	if sumSq == 0 {
		return vec
	}
	inv := float32(1.0 / math.Sqrt(sumSq))
	for i := range vec {
		vec[i] *= inv
	}
	return vec
}

func tokenize(s string) []string {
	if s == "" {
		return nil
	}
	lower := strings.ToLower(s)
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			cur.WriteRune(r)
		case r >= 0x00C0: // letters with diacritics (rough): keep them
			cur.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}
