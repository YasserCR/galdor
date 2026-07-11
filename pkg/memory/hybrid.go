package memory

import (
	"context"
	"errors"
	"sort"
)

// DefaultRRFK is the reciprocal-rank-fusion constant used when
// HybridRetriever.RRFK is unset. 60 is the near-universal default from
// Cormack, Clarke & Büttcher (SIGIR 2009); it damps the contribution of
// low-ranked items without needing any score calibration between sources.
const DefaultRRFK = 60

// Searcher is the minimal retrieval capability HybridRetriever composes.
// It is intentionally the read half of Store, so both memory.Store
// (SQLite/BM25, pgvector, qdrant, ...) and *memory.Retriever satisfy it
// with no changes. A vector source is typically a *Retriever carrying an
// Embedder (it turns Query.Text into an embedding internally); a lexical
// source is a BM25 Store or a *Retriever with a nil Embedder.
type Searcher interface {
	Retrieve(ctx context.Context, q Query) ([]Result, error)
}

// HybridRetriever fuses the rankings of several retrieval Sources with
// Reciprocal Rank Fusion (RRF). It is the standard way to combine a
// lexical (BM25) ranking with a dense (vector) one: RRF operates on ranks,
// not raw scores, so no cross-source score normalization is required.
//
// HybridRetriever does not embed or tokenize anything itself — it forwards
// the same Query to every Source and lets each decide its modality. It is
// a composer, not a Store: it has no Add/Delete/Close.
type HybridRetriever struct {
	// Sources are queried independently; their rankings are fused. At
	// least one is required.
	Sources []Searcher

	// K is the number of fused results to return. Query.K takes
	// precedence when set; otherwise K is used, defaulting to 5.
	K int

	// RRFK is the fusion constant. Defaults to DefaultRRFK (60).
	RRFK int

	// Pool is how many results to request from each Source before fusing.
	// A larger pool gives RRF more overlap to work with. Defaults to
	// max(4*K, 20).
	Pool int
}

// Retrieve queries every Source with q (each capped at Pool results),
// fuses the rankings with RRF, and returns the top-K in descending fused
// score. Chunks are fused by Chunk.ID; the first Source to surface a chunk
// contributes the returned Chunk value. If any Source errors, the error is
// returned rather than a silently degraded ranking.
func (h *HybridRetriever) Retrieve(ctx context.Context, q Query) ([]Result, error) {
	if len(h.Sources) == 0 {
		return nil, errors.New("memory: HybridRetriever has no Sources")
	}

	rrfK := h.RRFK
	if rrfK <= 0 {
		rrfK = DefaultRRFK
	}
	k := q.K
	if k <= 0 {
		k = h.K
	}
	if k <= 0 {
		k = 5
	}
	pool := h.Pool
	if pool <= 0 {
		pool = 4 * k
		if pool < 20 {
			pool = 20
		}
	}

	// acc accumulates the fused score for one chunk, remembering the order
	// in which it was first seen so ties break deterministically.
	type acc struct {
		chunk Chunk
		score float64
		order int
	}
	fused := make(map[string]*acc)
	var order int

	for _, src := range h.Sources {
		if src == nil {
			return nil, errors.New("memory: HybridRetriever has a nil Source")
		}
		sq := q
		sq.K = pool
		res, err := src.Retrieve(ctx, sq)
		if err != nil {
			return nil, err
		}
		for rank, r := range res {
			// RRF: 1/(k + rank). rank is 0-based here, so use rank+1.
			contrib := 1.0 / float64(rrfK+rank+1)
			key := r.Chunk.ID
			if key == "" {
				// Fall back to a composite key so ID-less chunks still fuse
				// per (document, ordinal) instead of colliding on "".
				key = r.Chunk.DocumentID + "\x00" + itoa(r.Chunk.Index)
			}
			if a, ok := fused[key]; ok {
				a.score += contrib
			} else {
				fused[key] = &acc{chunk: r.Chunk, score: contrib, order: order}
				order++
			}
		}
	}

	accs := make([]*acc, 0, len(fused))
	for _, a := range fused {
		accs = append(accs, a)
	}
	sort.SliceStable(accs, func(i, j int) bool {
		if accs[i].score != accs[j].score {
			return accs[i].score > accs[j].score
		}
		return accs[i].order < accs[j].order
	})

	if len(accs) > k {
		accs = accs[:k]
	}
	out := make([]Result, len(accs))
	for i, a := range accs {
		out[i] = Result{Chunk: a.chunk, Score: float32(a.score)}
	}
	return out, nil
}

// itoa is a tiny, allocation-light int-to-string for composite fusion
// keys; avoids pulling strconv into the hot path for a rare fallback.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
