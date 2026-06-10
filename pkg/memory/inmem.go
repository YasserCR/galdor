package memory

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// InMemoryStore is a goroutine-safe Store backed by Go slices and
// maps. It is meant for tests, examples and quick prototypes; it does
// not persist across process restarts and is not optimized for large
// corpora.
//
// Retrieval mode: when the Query carries an Embedding, ranking is by
// cosine similarity. Otherwise the Query.Text is matched against
// chunk text with case-insensitive substring scoring (a poor man's
// BM25; the embedded SQLite + BM25 backend lands in Session B).
//
// The zero value is not usable; call NewInMemoryStore.
type InMemoryStore struct {
	mu     sync.RWMutex
	chunks []Chunk
	byID   map[string]int
}

// NewInMemoryStore returns an empty, usable InMemoryStore.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{byID: map[string]int{}}
}

// Add appends chunks to the store. Chunks without an ID are assigned
// one (a v4 UUID). Re-adding a chunk with an existing ID overwrites
// the previous entry, so callers can use stable IDs to make
// re-ingestion idempotent.
func (s *InMemoryStore) Add(_ context.Context, chunks []Chunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range chunks {
		if c.ID == "" {
			c.ID = uuid.NewString()
		}
		// Store an independent copy: the caller may mutate or reuse the
		// Embedding slice / Metadata map after Add returns, which would
		// otherwise silently corrupt what we hold.
		c = cloneChunk(c)
		if idx, ok := s.byID[c.ID]; ok {
			s.chunks[idx] = c
			continue
		}
		s.byID[c.ID] = len(s.chunks)
		s.chunks = append(s.chunks, c)
	}
	return nil
}

// cloneChunk returns a deep copy of c whose reference-typed fields
// (Embedding, Metadata) don't alias the caller's backing storage.
func cloneChunk(c Chunk) Chunk {
	if c.Embedding != nil {
		c.Embedding = append([]float32(nil), c.Embedding...)
	}
	if c.Metadata != nil {
		m := make(map[string]string, len(c.Metadata))
		for k, v := range c.Metadata {
			m[k] = v
		}
		c.Metadata = m
	}
	return c
}

// Retrieve returns the top-K chunks for q in descending score order.
// See InMemoryStore's package comment for the ranking rules.
func (s *InMemoryStore) Retrieve(_ context.Context, q Query) ([]Result, error) {
	if q.Text == "" && len(q.Embedding) == 0 {
		return nil, errors.New("memory: Query.Text and Query.Embedding both empty")
	}
	k := q.K
	if k <= 0 {
		k = 5
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]Result, 0, len(s.chunks))
	for _, c := range s.chunks {
		if !matchesFilter(c.Metadata, q.Filter) {
			continue
		}
		var score float32
		vector := false
		switch {
		case len(q.Embedding) > 0:
			// Vector query: rank by cosine only. A chunk without an
			// embedding is incomparable, so skip it rather than falling
			// back to a lexical score that would compete in the same
			// ranking on an entirely different scale.
			if len(c.Embedding) == 0 {
				continue
			}
			cs, err := cosine(q.Embedding, c.Embedding)
			if err != nil {
				return nil, err
			}
			score = cs
			vector = true
		case q.Text != "":
			score = lexicalScore(q.Text, c.Text)
		}
		// For lexical queries, drop entries with no overlapping
		// tokens (score == 0). For vector queries, keep results
		// down to cosine 0; only negative-cosine entries are
		// considered actively dissimilar and dropped.
		if vector {
			if score < 0 {
				continue
			}
		} else if score <= 0 {
			continue
		}
		// Return an independent copy so a caller mutating the result's
		// Embedding/Metadata can't corrupt the stored chunk.
		results = append(results, Result{Chunk: cloneChunk(c), Score: score})
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > k {
		results = results[:k]
	}
	return results, nil
}

// Delete removes every chunk whose DocumentID matches the argument.
// Returns nil even when no chunks were removed (idempotent).
func (s *InMemoryStore) Delete(_ context.Context, documentID string) error {
	if documentID == "" {
		return errors.New("memory: Delete called with empty documentID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.chunks[:0]
	s.byID = map[string]int{}
	for _, c := range s.chunks {
		if c.DocumentID == documentID {
			continue
		}
		s.byID[c.ID] = len(kept)
		kept = append(kept, c)
	}
	s.chunks = kept
	return nil
}

// Close is a no-op for InMemoryStore.
func (*InMemoryStore) Close() error { return nil }

// Len returns the number of chunks currently stored. Useful for
// tests; not part of the Store interface.
func (s *InMemoryStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.chunks)
}

func matchesFilter(meta, filter map[string]string) bool {
	for k, v := range filter {
		if meta[k] != v {
			return false
		}
	}
	return true
}

// cosine returns the cosine similarity between a and b, in [-1, 1].
// Returns 0 when either vector is zero-length.
func cosine(a, b []float32) (float32, error) {
	if len(a) != len(b) {
		// Truncating to the shorter length (the old behavior) produced a
		// plausible but wrong score over a prefix — a 768-dim query
		// against 1536-dim vectors would silently "work". A mismatch
		// means the corpus and query were embedded by different models.
		return 0, fmt.Errorf("memory: embedding dimension mismatch: query=%d vs chunk=%d", len(a), len(b))
	}
	if len(a) == 0 {
		return 0, nil
	}
	var dot, na, nb float64
	for i := range a {
		ai := float64(a[i])
		bi := float64(b[i])
		dot += ai * bi
		na += ai * ai
		nb += bi * bi
	}
	denom := math.Sqrt(na) * math.Sqrt(nb)
	if denom == 0 {
		return 0, nil
	}
	return float32(dot / denom), nil
}

// lexicalScore is a rough term-frequency match: every query token
// found in target contributes 1, normalized by query token count.
// Case-insensitive. Returns a value in [0, 1].
func lexicalScore(query, target string) float32 {
	if query == "" || target == "" {
		return 0
	}
	q := strings.Fields(strings.ToLower(query))
	if len(q) == 0 {
		return 0
	}
	t := strings.ToLower(target)
	hits := 0
	for _, tok := range q {
		if strings.Contains(t, tok) {
			hits++
		}
	}
	return float32(hits) / float32(len(q))
}
