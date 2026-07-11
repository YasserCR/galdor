package okf

import (
	"context"
	"strings"

	"github.com/YasserCR/galdor/memory/sqlite"
	"github.com/YasserCR/galdor/pkg/memory"
	"github.com/YasserCR/galdor/pkg/tool"
)

// FilterTag is a reserved Query.Filter key understood by (*Store).Retrieve.
// Because OKF tags are a list but memory.Query.Filter does exact key/value
// matching, tag membership is handled here (post-filter) instead of by
// widening the core Query contract. `Filter[FilterTag] = "revenue"` keeps
// only results whose concept carries that tag.
const FilterTag = "tag"

// Store is a memory.Store over an OKF bundle. It is a lexical (BM25)
// backend: it wraps galdor's SQLite/FTS5 store — reusing that proven BM25
// implementation — and adds OKF-aware concerns (concept-first chunks,
// tag-membership filtering). It is a drop-in memory.Store; compose it with
// a vector Retriever under a memory.HybridRetriever for hybrid search
// (see examples/okf-rag).
type Store struct {
	inner *sqlite.Store
}

// NewStore builds an in-memory BM25 store from pre-chunked concepts
// (typically the output of ChunkConcepts). The caller owns Close.
func NewStore(ctx context.Context, chunks []memory.Chunk) (*Store, error) {
	inner, err := sqlite.Open(":memory:")
	if err != nil {
		return nil, err
	}
	if err := inner.Add(ctx, chunks); err != nil {
		_ = inner.Close()
		return nil, err
	}
	return &Store{inner: inner}, nil
}

// Open loads an OKF bundle from a directory and returns a ready BM25 Store.
// It is Load + ChunkConcepts + NewStore; use the pieces directly when you
// also need the documents (e.g. to build a second, vector-backed source
// for hybrid retrieval).
func Open(ctx context.Context, root string) (*Store, error) {
	docs, _, err := Load(root)
	if err != nil {
		return nil, err
	}
	return NewStore(ctx, ChunkConcepts(docs))
}

// Add ingests additional chunks. Add implements memory.Store.
func (s *Store) Add(ctx context.Context, chunks []memory.Chunk) error {
	return s.inner.Add(ctx, chunks)
}

// Retrieve runs a BM25 query. The reserved FilterTag key is applied as a
// tag-membership post-filter; every other Filter entry is pushed down to
// the underlying store as an exact metadata match. Retrieve implements
// memory.Store.
func (s *Store) Retrieve(ctx context.Context, q memory.Query) ([]memory.Result, error) {
	wantTag := ""
	if len(q.Filter) > 0 {
		if t, ok := q.Filter[FilterTag]; ok && t != "" {
			wantTag = t
			// Copy the filter without the reserved key so we never mutate
			// the caller's map and the store sees only real metadata keys.
			rest := make(map[string]string, len(q.Filter))
			for k, v := range q.Filter {
				if k != FilterTag {
					rest[k] = v
				}
			}
			if len(rest) == 0 {
				rest = nil
			}
			q.Filter = rest
		}
	}
	res, err := s.inner.Retrieve(ctx, q)
	if err != nil || wantTag == "" {
		return res, err
	}
	filtered := res[:0]
	for _, r := range res {
		if hasTag(r.Chunk.Metadata[MetaTags], wantTag) {
			filtered = append(filtered, r)
		}
	}
	return filtered, nil
}

// Delete removes every chunk of a concept. Delete implements memory.Store.
func (s *Store) Delete(ctx context.Context, documentID string) error {
	return s.inner.Delete(ctx, documentID)
}

// Close releases the underlying store. Close implements memory.Store.
func (s *Store) Close() error { return s.inner.Close() }

// Len reports the number of indexed chunks. Useful for tests and CLIs.
func (s *Store) Len(ctx context.Context) (int, error) { return s.inner.Len(ctx) }

func hasTag(csv, want string) bool {
	for _, t := range strings.Split(csv, ",") {
		if strings.EqualFold(strings.TrimSpace(t), want) {
			return true
		}
	}
	return false
}

// SearchInput is the argument schema for the tool returned by
// NewSearchTool. Only Query is required.
type SearchInput struct {
	Query string `json:"query"`
	Type  string `json:"type,omitempty"`
	Tag   string `json:"tag,omitempty"`
	K     int    `json:"k,omitempty"`
}

// SearchHit is one result row returned by the search tool.
type SearchHit struct {
	ConceptID string  `json:"concept_id"`
	Title     string  `json:"title"`
	Type      string  `json:"type"`
	Score     float32 `json:"score"`
	Snippet   string  `json:"snippet"`
}

// SearchOutput is the search tool's result payload.
type SearchOutput struct {
	Hits []SearchHit `json:"hits"`
}

// NewSearchTool wraps a retrieval source as a ReAct-callable tool so an
// agent can query an OKF bundle by text, optionally narrowing by concept
// `type` or `tag`. Pass an *okf.Store (so the tag filter is honored) or any
// memory.Store. The tool is a thin adapter over the native backend: the
// dependency points memory/okf → pkg/tool, never the reverse.
func NewSearchTool(s memory.Store) (tool.Tool[SearchInput, SearchOutput], error) {
	return tool.NewTool(
		"okf_search",
		"Search the Open Knowledge Format bundle for concepts relevant to a query. "+
			"Optionally filter by concept type (e.g. Metric, \"Warehouse Table\") or a tag.",
		func(ctx context.Context, in SearchInput) (SearchOutput, error) {
			q := memory.Query{Text: in.Query, K: in.K}
			filter := make(map[string]string, 2)
			if in.Type != "" {
				filter[MetaType] = in.Type
			}
			if in.Tag != "" {
				filter[FilterTag] = in.Tag
			}
			if len(filter) > 0 {
				q.Filter = filter
			}
			res, err := s.Retrieve(ctx, q)
			if err != nil {
				return SearchOutput{}, err
			}
			hits := make([]SearchHit, 0, len(res))
			for _, r := range res {
				hits = append(hits, SearchHit{
					ConceptID: r.Chunk.Metadata[MetaConceptID],
					Title:     r.Chunk.Metadata[MetaTitle],
					Type:      r.Chunk.Metadata[MetaType],
					Score:     r.Score,
					Snippet:   snippet(r.Chunk.Text),
				})
			}
			return SearchOutput{Hits: hits}, nil
		},
	)
}

// snippet returns the first substantive line of a chunk (skipping the
// folded header, markdown headings, table rows and fences) for display.
func snippet(text string) string {
	lines := strings.Split(text, "\n")
	// Skip the first line: it is the folded "title. description tags: ..."
	for i, line := range lines {
		if i == 0 {
			continue
		}
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "|") || strings.HasPrefix(s, "```") {
			continue
		}
		if len(s) > 140 {
			return s[:140] + "…"
		}
		return s
	}
	return ""
}
