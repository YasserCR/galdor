package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/YasserCR/galdor/pkg/memory"
)

// Reserved payload keys used to round-trip the Chunk fields that are
// not part of the user's metadata. The double-underscore prefix
// signals "system-owned" and avoids collisions with caller-supplied
// keys.
const (
	payloadKeyDocumentID = "__document_id"
	payloadKeyIndex      = "__index"
	payloadKeyText       = "__text"
)

// Config configures the qdrant Store.
type Config struct {
	// URL is the base URL of the Qdrant HTTP API, e.g.
	// http://localhost:6333. Required.
	URL string

	// Collection is the name of the Qdrant collection backing this
	// Store. Defaults to "galdor_chunks". Created on Open if missing.
	Collection string

	// Dim is the embedding dimensionality. Required when creating a
	// new collection; ignored when the collection already exists.
	Dim int

	// APIKey, when non-empty, is sent as the api-key header on every
	// request (Qdrant Cloud, or self-hosted with auth enabled).
	APIKey string

	// HTTPClient, when nil, defaults to an http.Client with a 30s
	// timeout. Override for custom transports (proxies, retries,
	// tracing).
	HTTPClient *http.Client
}

// Store is a memory.Store backed by Qdrant. The zero value is not
// usable; call Open.
type Store struct {
	baseURL    string
	collection string
	apiKey     string
	dim        int
	http       *http.Client
}

// Open returns a usable Store. It validates the config, pings the
// server and creates the collection if it does not already exist.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.URL == "" {
		return nil, errors.New("memory/qdrant: URL is required")
	}
	if cfg.Dim <= 0 {
		return nil, errors.New("memory/qdrant: Dim must be > 0")
	}
	if cfg.Collection == "" {
		cfg.Collection = "galdor_chunks"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	s := &Store{
		baseURL:    strings.TrimRight(cfg.URL, "/"),
		collection: cfg.Collection,
		apiKey:     cfg.APIKey,
		dim:        cfg.Dim,
		http:       client,
	}
	if err := s.ensureCollection(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Close is a no-op (the HTTP client owns its own pooled connections).
func (*Store) Close() error { return nil }

// Add ingests chunks. Every chunk must have a non-empty ID; callers
// are expected to assign stable IDs so re-ingestion is idempotent.
// Chunks whose Embedding length differs from the collection's
// dimension are rejected.
//
// Qdrant requires point IDs to be either unsigned ints or UUIDs. To
// keep galdor's ID model (free-form strings) usable, the user's
// Chunk.ID is stored as a payload key (`__chunk_id`) and the actual
// point ID is the SHA-1 of that string formatted as a UUID v5. This
// makes re-ingestion of the same Chunk.ID idempotent (upsert).
func (s *Store) Add(ctx context.Context, chunks []memory.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	for _, c := range chunks {
		if c.ID == "" {
			return errors.New("memory/qdrant: Chunk.ID is empty (caller must assign IDs)")
		}
		if len(c.Embedding) != s.dim {
			return fmt.Errorf("memory/qdrant: chunk %q has %d-dim embedding; collection is %d-dim", c.ID, len(c.Embedding), s.dim)
		}
	}

	points := make([]upsertPoint, 0, len(chunks))
	for _, c := range chunks {
		payload := map[string]any{
			payloadKeyDocumentID: c.DocumentID,
			payloadKeyIndex:      c.Index,
			payloadKeyText:       c.Text,
		}
		for k, v := range c.Metadata {
			payload[k] = v
		}
		points = append(points, upsertPoint{
			ID:      uuidFromString(c.ID),
			Vector:  c.Embedding,
			Payload: payload,
		})
	}
	body := map[string]any{"points": points}

	path := fmt.Sprintf("/collections/%s/points?wait=true", url.PathEscape(s.collection))
	if err := s.do(ctx, http.MethodPut, path, body, nil); err != nil {
		return fmt.Errorf("memory/qdrant: upsert: %w", err)
	}
	return nil
}

// Retrieve runs q against the collection. Requires q.Embedding to be
// set; this backend is vector-only (text retrieval belongs in the
// SQLite/BM25 adapter).
func (s *Store) Retrieve(ctx context.Context, q memory.Query) ([]memory.Result, error) {
	if len(q.Embedding) == 0 {
		return nil, errors.New("memory/qdrant: Query.Embedding is required (this backend is vector-only)")
	}
	if len(q.Embedding) != s.dim {
		return nil, fmt.Errorf("memory/qdrant: query has %d-dim embedding; collection is %d-dim", len(q.Embedding), s.dim)
	}
	k := q.K
	if k <= 0 {
		k = 5
	}

	body := buildSearchBody(q.Embedding, k, q.Filter)
	path := fmt.Sprintf("/collections/%s/points/search", url.PathEscape(s.collection))
	var resp searchResponse
	if err := s.do(ctx, http.MethodPost, path, body, &resp); err != nil {
		return nil, fmt.Errorf("memory/qdrant: search: %w", err)
	}
	results := make([]memory.Result, 0, len(resp.Result))
	for _, pt := range resp.Result {
		c := chunkFromPayload(pt.Payload)
		c.Embedding = pt.Vector
		// Qdrant's cosine score is already higher-is-better and lives
		// in [-1, 1].
		results = append(results, memory.Result{Chunk: c, Score: pt.Score})
	}
	return results, nil
}

// Delete removes every point whose `__document_id` payload field
// matches the argument.
func (s *Store) Delete(ctx context.Context, documentID string) error {
	if documentID == "" {
		return errors.New("memory/qdrant: Delete called with empty documentID")
	}
	body := map[string]any{
		"filter": map[string]any{
			"must": []any{
				map[string]any{"key": payloadKeyDocumentID, "match": map[string]any{"value": documentID}},
			},
		},
	}
	path := fmt.Sprintf("/collections/%s/points/delete?wait=true", url.PathEscape(s.collection))
	if err := s.do(ctx, http.MethodPost, path, body, nil); err != nil {
		return fmt.Errorf("memory/qdrant: delete: %w", err)
	}
	return nil
}

// Len reports the number of points in the collection. Not part of
// the Store interface; useful for tests.
func (s *Store) Len(ctx context.Context) (int, error) {
	path := fmt.Sprintf("/collections/%s", url.PathEscape(s.collection))
	var resp collectionInfoResponse
	if err := s.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return 0, err
	}
	return resp.Result.PointsCount, nil
}

func (s *Store) ensureCollection(ctx context.Context) error {
	// Idempotent: try to read; create only if missing.
	path := fmt.Sprintf("/collections/%s", url.PathEscape(s.collection))
	err := s.do(ctx, http.MethodGet, path, nil, nil)
	if err == nil {
		return nil
	}
	var herr *httpError
	if !errors.As(err, &herr) || herr.Status != http.StatusNotFound {
		return fmt.Errorf("memory/qdrant: probe collection: %w", err)
	}
	body := map[string]any{
		"vectors": map[string]any{
			"size":     s.dim,
			"distance": "Cosine",
		},
	}
	if err := s.do(ctx, http.MethodPut, path, body, nil); err != nil {
		return fmt.Errorf("memory/qdrant: create collection: %w", err)
	}
	return nil
}

// do issues an HTTP request to the Qdrant API. body and out can be
// nil. When out is non-nil, the response is JSON-decoded into it.
// Non-2xx responses return an *httpError so callers can inspect the
// status code (used by ensureCollection to detect 404).
func (s *Store) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.apiKey != "" {
		req.Header.Set("api-key", s.apiKey)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &httpError{Status: resp.StatusCode, Body: string(respBody)}
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

type httpError struct {
	Status int
	Body   string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("qdrant HTTP %d: %s", e.Status, e.Body)
}

// Wire types for the subset of the Qdrant API we use.

type upsertPoint struct {
	ID      string         `json:"id"`
	Vector  []float32      `json:"vector"`
	Payload map[string]any `json:"payload"`
}

type searchResponse struct {
	Result []struct {
		ID      any            `json:"id"`
		Score   float32        `json:"score"`
		Vector  []float32      `json:"vector,omitempty"`
		Payload map[string]any `json:"payload"`
	} `json:"result"`
}

type collectionInfoResponse struct {
	Result struct {
		PointsCount int `json:"points_count"`
	} `json:"result"`
}

// buildSearchBody constructs the Qdrant /points/search payload from
// a galdor Query. Exported as a free function so it can be unit-
// tested without a live server.
func buildSearchBody(vec []float32, k int, filter map[string]string) map[string]any {
	body := map[string]any{
		"vector":       vec,
		"limit":        k,
		"with_payload": true,
		"with_vector":  false,
	}
	if len(filter) > 0 {
		must := make([]any, 0, len(filter))
		for k, v := range filter {
			must = append(must, map[string]any{
				"key":   k,
				"match": map[string]any{"value": v},
			})
		}
		body["filter"] = map[string]any{"must": must}
	}
	return body
}

// chunkFromPayload reconstructs a Chunk from a Qdrant point's
// payload, stripping the reserved keys and routing the rest to
// Metadata.
func chunkFromPayload(payload map[string]any) memory.Chunk {
	c := memory.Chunk{}
	if v, ok := payload[payloadKeyDocumentID].(string); ok {
		c.DocumentID = v
	}
	if v, ok := payload[payloadKeyIndex].(float64); ok {
		c.Index = int(v)
	}
	if v, ok := payload[payloadKeyText].(string); ok {
		c.Text = v
	}
	var meta map[string]string
	for k, v := range payload {
		if strings.HasPrefix(k, "__") {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		if meta == nil {
			meta = map[string]string{}
		}
		meta[k] = s
	}
	c.Metadata = meta
	return c
}
