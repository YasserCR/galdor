package okf

import (
	"context"
	"strings"

	"github.com/YasserCR/galdor/pkg/memory"
	"github.com/YasserCR/galdor/pkg/memory/bm25"
	"github.com/YasserCR/galdor/pkg/tool"
)

// Reserved Query.Filter keys understood by (*Store).Retrieve. They are not
// stored metadata but query-time directives that the core memory.Query
// contract (exact key/value matching) cannot express, so the OKF store
// applies them as post-filters:
//
//   - FilterTag keeps only results whose concept carries the given tag
//     (OKF tags are a list, not a scalar).
//   - FilterSince / FilterUntil keep only results whose timestamp is >= /
//     <= the given value, compared as ISO-8601 strings (lexicographic
//     order matches chronological order for that format). A concept with
//     no timestamp is excluded whenever either bound is set.
//
// Every other Filter entry (including MetaSection) is a real metadata key
// pushed down to the underlying store as an exact match.
const (
	FilterTag   = "tag"
	FilterSince = "since"
	FilterUntil = "until"
)

// Store is a memory.Store over an OKF bundle. It is a lexical (BM25)
// backend: it wraps galdor's native BM25 index (memory/bm25), whose
// code-aware tokenizer keeps compound identifiers (customer_id) findable
// both whole and by their parts, and adds OKF-aware concerns (concept-first
// chunks, tag-membership filtering). It is a drop-in memory.Store; compose
// it with a vector Retriever under a memory.HybridRetriever for hybrid
// search (see examples/okf-rag).
type Store struct {
	inner *bm25.Store
}

// NewStore builds an in-memory BM25 store from pre-chunked concepts
// (typically the output of ChunkConcepts). The caller owns Close.
func NewStore(ctx context.Context, chunks []memory.Chunk) (*Store, error) {
	inner := bm25.New()
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

// Retrieve runs a BM25 query. The reserved keys (FilterTag, FilterSince,
// FilterUntil) are applied as post-filters; every other Filter entry is
// pushed down to the underlying store as an exact metadata match. Retrieve
// implements memory.Store.
func (s *Store) Retrieve(ctx context.Context, q memory.Query) ([]memory.Result, error) {
	wantTag, since, until := "", "", ""
	if hasReservedFilter(q.Filter) {
		// Copy the filter without the reserved keys so we never mutate the
		// caller's map and the store sees only real metadata keys.
		rest := make(map[string]string, len(q.Filter))
		for k, v := range q.Filter {
			switch k {
			case FilterTag:
				wantTag = v
			case FilterSince:
				since = v
			case FilterUntil:
				until = v
			default:
				rest[k] = v
			}
		}
		if len(rest) == 0 {
			rest = nil
		}
		q.Filter = rest
	}
	res, err := s.inner.Retrieve(ctx, q)
	if err != nil || (wantTag == "" && since == "" && until == "") {
		return res, err
	}
	filtered := res[:0]
	for _, r := range res {
		if wantTag != "" && !hasTag(r.Chunk.Metadata[MetaTags], wantTag) {
			continue
		}
		if since != "" || until != "" {
			ts := r.Chunk.Metadata[MetaTimestamp]
			if ts == "" || (since != "" && ts < since) || (until != "" && ts > until) {
				continue
			}
		}
		filtered = append(filtered, r)
	}
	return filtered, nil
}

// hasReservedFilter reports whether the filter carries any key the OKF
// store handles itself as a post-filter rather than a metadata match.
func hasReservedFilter(filter map[string]string) bool {
	if len(filter) == 0 {
		return false
	}
	for _, k := range []string{FilterTag, FilterSince, FilterUntil} {
		if v, ok := filter[k]; ok && v != "" {
			return true
		}
	}
	return false
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
	// Since / Until bound results by ISO-8601 timestamp (inclusive).
	Since string `json:"since,omitempty"`
	Until string `json:"until,omitempty"`
	// Section keeps only chunks from a conventional body section
	// ("schema", "examples", "citations").
	Section string `json:"section,omitempty"`
	K       int    `json:"k,omitempty"`
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
			filter := make(map[string]string, 5)
			if in.Type != "" {
				filter[MetaType] = in.Type
			}
			if in.Tag != "" {
				filter[FilterTag] = in.Tag
			}
			if in.Since != "" {
				filter[FilterSince] = in.Since
			}
			if in.Until != "" {
				filter[FilterUntil] = in.Until
			}
			if in.Section != "" {
				filter[MetaSection] = in.Section
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
