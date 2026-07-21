package bm25

import (
	"context"
	"errors"
	"maps"
	"math"
	"sort"
	"sync"

	"github.com/google/uuid"

	"github.com/YasserCR/galdor/pkg/memory"
)

// Okapi BM25 parameters. k1 controls term-frequency saturation, b the
// document-length normalization; 1.2 / 0.75 are the standard defaults.
const (
	defaultK1 = 1.2
	defaultB  = 0.75
)

// Store is an in-memory BM25 lexical index. It is goroutine-safe and
// implements memory.Store. Retrieval ignores Query.Embedding — this is a
// purely lexical ranker; compose it with a vector source under a
// memory.HybridRetriever for hybrid search.
//
// The zero value is not usable; construct with New.
type Store struct {
	mu       sync.RWMutex
	tok      Tokenizer
	docs     []*indexed
	byID     map[string]int // chunk ID -> index into docs
	df       map[string]int // term -> number of docs containing it
	totalLen int            // sum of document lengths, for avgdl
	k1, b    float64
}

// indexed is one stored chunk with its term frequencies and token length.
type indexed struct {
	chunk  memory.Chunk
	tf     map[string]int
	length int
}

// Option configures a Store at construction.
type Option func(*Store)

// WithTokenizer overrides the default CodeTokenizer.
func WithTokenizer(t Tokenizer) Option {
	return func(s *Store) {
		if t != nil {
			s.tok = t
		}
	}
}

// WithParams overrides the BM25 k1 and b constants.
func WithParams(k1, b float64) Option {
	return func(s *Store) { s.k1, s.b = k1, b }
}

// New returns an empty, usable BM25 Store.
func New(opts ...Option) *Store {
	s := &Store{
		tok:  CodeTokenizer{},
		byID: map[string]int{},
		df:   map[string]int{},
		k1:   defaultK1,
		b:    defaultB,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Add indexes chunks. A chunk with an empty ID is assigned one; re-adding a
// chunk with an existing ID overwrites it (stable IDs make re-ingestion
// idempotent). Add implements memory.Store.
func (s *Store) Add(_ context.Context, chunks []memory.Chunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range chunks {
		if c.ID == "" {
			c.ID = uuid.NewString()
		}
		c = cloneChunk(c)
		tf := make(map[string]int)
		length := 0
		for _, t := range s.tok.Tokenize(c.Text) {
			tf[t]++
			length++
		}
		d := &indexed{chunk: c, tf: tf, length: length}
		if i, ok := s.byID[c.ID]; ok {
			s.docs[i] = d
		} else {
			s.byID[c.ID] = len(s.docs)
			s.docs = append(s.docs, d)
		}
	}
	s.rebuildStats()
	return nil
}

// Retrieve returns the top-K chunks for q by BM25 score, descending.
// Query.Filter is applied as an exact metadata match. Retrieve implements
// memory.Store.
func (s *Store) Retrieve(_ context.Context, q memory.Query) ([]memory.Result, error) {
	if q.Text == "" {
		return nil, errors.New("bm25: Query.Text is empty")
	}
	k := q.K
	if k <= 0 {
		k = 5
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	n := len(s.docs)
	if n == 0 {
		return nil, nil
	}
	qterms := s.uniqueTerms(q.Text)
	if len(qterms) == 0 {
		return nil, nil
	}
	avgdl := float64(s.totalLen) / float64(n)

	var results []memory.Result
	for _, d := range s.docs {
		if !matchesFilter(d.chunk.Metadata, q.Filter) {
			continue
		}
		score := s.score(d, qterms, n, avgdl)
		if score > 0 {
			results = append(results, memory.Result{Chunk: cloneChunk(d.chunk), Score: float32(score)})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > k {
		results = results[:k]
	}
	return results, nil
}

// score sums the BM25 contribution of every query term present in d.
func (s *Store) score(d *indexed, qterms []string, n int, avgdl float64) float64 {
	var norm float64
	if avgdl > 0 {
		norm = float64(d.length) / avgdl
	}
	var total float64
	for _, term := range qterms {
		f := d.tf[term]
		if f == 0 {
			continue
		}
		df := s.df[term]
		// Lucene-style BM25 IDF: always positive thanks to the +1.
		idf := math.Log(1 + (float64(n)-float64(df)+0.5)/(float64(df)+0.5))
		denom := float64(f) + s.k1*(1-s.b+s.b*norm)
		total += idf * (float64(f) * (s.k1 + 1)) / denom
	}
	return total
}

// Delete removes every chunk whose DocumentID matches. Delete implements
// memory.Store.
func (s *Store) Delete(_ context.Context, documentID string) error {
	if documentID == "" {
		return errors.New("bm25: Delete called with empty documentID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.docs[:0]
	s.byID = make(map[string]int)
	for _, d := range s.docs {
		if d.chunk.DocumentID == documentID {
			continue
		}
		s.byID[d.chunk.ID] = len(kept)
		kept = append(kept, d)
	}
	s.docs = kept
	s.rebuildStats()
	return nil
}

// Close implements memory.Store; it is a no-op for an in-memory index.
func (s *Store) Close() error { return nil }

// Len reports the number of indexed chunks. It matches the signature the
// OKF store expects of its inner backend.
func (s *Store) Len(_ context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.docs), nil
}

// rebuildStats recomputes the corpus-wide document frequencies and total
// length from the current documents. Called under the write lock after any
// mutation; O(corpus) but the corpora this backend targets are small.
func (s *Store) rebuildStats() {
	df := make(map[string]int)
	total := 0
	for _, d := range s.docs {
		total += d.length
		for term := range d.tf {
			df[term]++
		}
	}
	s.df = df
	s.totalLen = total
}

// uniqueTerms tokenizes text and returns its distinct terms in first-seen
// order. Repeated query terms don't multiply a document's score.
func (s *Store) uniqueTerms(text string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, t := range s.tok.Tokenize(text) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// matchesFilter reports whether meta satisfies every key/value in filter
// (exact match). An empty filter matches everything.
func matchesFilter(meta, filter map[string]string) bool {
	for k, v := range filter {
		if meta[k] != v {
			return false
		}
	}
	return true
}

// cloneChunk returns a copy of c whose Metadata / Embedding don't alias the
// caller's storage, so neither side can corrupt the other after Add/Retrieve.
func cloneChunk(c memory.Chunk) memory.Chunk {
	if c.Embedding != nil {
		c.Embedding = append([]float32(nil), c.Embedding...)
	}
	if c.Metadata != nil {
		c.Metadata = maps.Clone(c.Metadata)
	}
	return c
}
