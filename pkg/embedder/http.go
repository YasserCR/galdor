package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/YasserCR/galdor/pkg/memory"
)

// Compile-time assertion: HTTPEmbedder satisfies memory.Embedder.
var _ memory.Embedder = (*HTTPEmbedder)(nil)

// Shape is the wire format of the embedding endpoint.
type Shape string

const (
	// ShapeOpenAI is the OpenAI /v1/embeddings JSON envelope.
	ShapeOpenAI Shape = "openai"
	// ShapeTEI is the HuggingFace Text Embeddings Inference shape.
	ShapeTEI Shape = "tei"
)

const (
	defaultBatchSize  = 32
	defaultTimeout    = 60 * time.Second
	defaultMaxRetries = 3
	bodySnippetLimit  = 512
)

// HTTPConfig configures an HTTPEmbedder. URL is required; the
// per-shape suffix ("/embed" or "/embeddings") is appended when
// missing. Shape defaults to ShapeOpenAI; BatchSize to 32; Timeout
// to 60s (ignored when HTTPClient is set). Dim, when > 0, is reported
// by Dimensions and forwarded as "dimensions" for OpenAI. Model is
// OpenAI-only. APIKey is sent as Authorization: Bearer when set.
type HTTPConfig struct {
	URL        string
	Shape      Shape
	Model      string
	APIKey     string
	HTTPClient *http.Client
	BatchSize  int
	Timeout    time.Duration
	Dim        int
}

// EmbedError is the typed error for any non-2xx response. Body is
// truncated to 512 bytes for diagnostics.
type EmbedError struct {
	Status    int
	URL, Body string
}

// Error implements error.
func (e *EmbedError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("embedder: HTTP %d from %s", e.Status, e.URL)
	}
	return fmt.Sprintf("embedder: HTTP %d from %s: %s", e.Status, e.URL, e.Body)
}

// HTTPEmbedder is an HTTP-only memory.Embedder. Safe for concurrent use.
type HTTPEmbedder struct {
	url       string
	shape     Shape
	model     string
	apiKey    string
	client    *http.Client
	batchSize int
	dim       int
}

// NewHTTPEmbedder validates cfg and returns a ready embedder.
func NewHTTPEmbedder(cfg HTTPConfig) (*HTTPEmbedder, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, errors.New("embedder: URL is required")
	}
	shape := cfg.Shape
	if shape == "" {
		shape = ShapeOpenAI
	}
	if shape != ShapeOpenAI && shape != ShapeTEI {
		return nil, fmt.Errorf("embedder: unknown shape %q", string(shape))
	}
	client := cfg.HTTPClient
	if client == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		client = &http.Client{Timeout: timeout}
	}
	batch := cfg.BatchSize
	if batch <= 0 {
		batch = defaultBatchSize
	}
	u := strings.TrimRight(cfg.URL, "/")
	switch shape {
	case ShapeTEI:
		if !strings.HasSuffix(u, "/embed") {
			u += "/embed"
		}
	case ShapeOpenAI:
		if !strings.HasSuffix(u, "/embeddings") {
			u += "/embeddings"
		}
	}
	return &HTTPEmbedder{
		url: u, shape: shape, model: cfg.Model, apiKey: cfg.APIKey,
		client: client, batchSize: batch, dim: cfg.Dim,
	}, nil
}

// Embed implements memory.Embedder. Inputs longer than BatchSize are
// split across multiple HTTP calls and re-assembled in input order.
func (e *HTTPEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += e.batchSize {
		end := start + e.batchSize
		if end > len(texts) {
			end = len(texts)
		}
		body, err := e.encode(texts[start:end])
		if err != nil {
			return nil, err
		}
		raw, err := e.doWithRetry(ctx, body)
		if err != nil {
			return nil, err
		}
		vecs, err := e.decode(raw, end-start)
		if err != nil {
			return nil, err
		}
		if len(vecs) != end-start {
			return nil, fmt.Errorf("embedder: server returned %d vectors for %d inputs", len(vecs), end-start)
		}
		out = append(out, vecs...)
	}
	if e.dim == 0 && len(out) > 0 && len(out[0]) > 0 {
		e.dim = len(out[0])
	}
	return out, nil
}

// Dimensions implements memory.Embedder.
func (e *HTTPEmbedder) Dimensions() int { return e.dim }

// Ping issues a single-element Embed and discards the vector.
func (e *HTTPEmbedder) Ping(ctx context.Context) error {
	_, err := e.Embed(ctx, []string{"ping"})
	return err
}

func (e *HTTPEmbedder) encode(texts []string) ([]byte, error) {
	if e.shape == ShapeTEI {
		return json.Marshal(map[string]any{"inputs": texts})
	}
	req := openAIRequest{Input: texts, Model: e.model}
	if e.dim > 0 {
		req.Dimensions = e.dim
	}
	return json.Marshal(req)
}

func (e *HTTPEmbedder) decode(raw []byte, n int) ([][]float32, error) {
	if e.shape == ShapeTEI {
		var out [][]float32
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("embedder: decode tei response: %w", err)
		}
		return out, nil
	}
	var env openAIResponse
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("embedder: decode openai response: %w", err)
	}
	vecs := make([][]float32, n)
	for i, d := range env.Data {
		idx := d.Index
		if idx < 0 || idx >= n {
			idx = i
		}
		if idx >= n {
			return nil, fmt.Errorf("embedder: openai response index %d out of range", idx)
		}
		vecs[idx] = d.Embedding
	}
	return vecs, nil
}

func (e *HTTPEmbedder) doWithRetry(ctx context.Context, body []byte) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < defaultMaxRetries; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, attempt); err != nil {
				return nil, err
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		if e.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+e.apiKey)
		}
		resp, err := e.client.Do(req)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			lastErr = err
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return raw, nil
		}
		// Transient: 5xx + 429. 4xx (incl. 413) is terminal.
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			lastErr = &EmbedError{Status: resp.StatusCode, URL: e.url, Body: snippet(raw)}
			continue
		}
		return nil, &EmbedError{Status: resp.StatusCode, URL: e.url, Body: snippet(raw)}
	}
	if lastErr == nil {
		lastErr = errors.New("embedder: exhausted retries")
	}
	return nil, lastErr
}

// sleep backs off 100ms * 2^(attempt-1) + deterministic jitter,
// honoring ctx cancellation.
func sleep(ctx context.Context, attempt int) error {
	base := 100 * time.Millisecond
	for i := 1; i < attempt; i++ {
		base *= 2
	}
	t := time.NewTimer(base + time.Duration(attempt*37)*time.Millisecond)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > bodySnippetLimit {
		return s[:bodySnippetLimit]
	}
	return s
}

type openAIRequest struct {
	Input      []string `json:"input"`
	Model      string   `json:"model,omitempty"`
	Dimensions int      `json:"dimensions,omitempty"`
}

type openAIResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}
