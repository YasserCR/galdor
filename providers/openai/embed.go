package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/YasserCR/galdor/pkg/memory"
)

// Compile-time assertion: Embedder satisfies memory.Embedder.
var _ memory.Embedder = (*Embedder)(nil)

// DefaultEmbeddingModel is the model used when EmbedderConfig.Model is
// empty. text-embedding-3-small produces 1536-dim vectors and is the
// most cost-effective option in OpenAI's current lineup.
const DefaultEmbeddingModel = "text-embedding-3-small"

// EmbedderConfig configures an Embedder.
//
// The Embedder reuses the same authentication and transport options
// as the chat Provider, so most callers will share a single set of
// credentials between both. BaseURL also follows the same convention,
// which means any OpenAI-compatible endpoint that exposes
// /v1/embeddings (Mistral, MiniMax, Together, Groq, Azure OpenAI,
// vLLM, ...) works as a drop-in.
type EmbedderConfig struct {
	// APIKey authenticates against the API. Required.
	APIKey string

	// BaseURL overrides the endpoint. Defaults to api.openai.com.
	BaseURL string

	// Model is the embedding model ID (e.g. text-embedding-3-small,
	// text-embedding-3-large, or a provider-specific equivalent for
	// Mistral / MiniMax / Together). Defaults to
	// DefaultEmbeddingModel.
	Model string

	// Dim is the embedding dimensionality reported by Dimensions()
	// and forwarded to the API as the `dimensions` field when > 0.
	// OpenAI's v3 embedding models support truncation to any value
	// below the model's native size; older models ignore it. When
	// zero, the model's native dimensionality is used and Embedder
	// auto-detects it from the first Embed call.
	Dim int

	// Organization is sent as openai-organization when non-empty.
	Organization string

	// Project is sent as openai-project when non-empty.
	Project string

	// HTTPClient is the transport used for requests. Defaults to a
	// client with a 60s timeout.
	HTTPClient *http.Client

	// UserAgent is appended to the default user-agent string when
	// non-empty.
	UserAgent string
}

// Embedder implements memory.Embedder against OpenAI's
// /v1/embeddings endpoint. Safe for concurrent use.
type Embedder struct {
	apiKey  string
	baseURL string
	model   string
	// dim is the embedding dimension: configured up front or learned
	// from the first response. Guarded by atomic access because Embedder
	// is documented as safe for concurrent use, and the auto-detect write
	// would otherwise race concurrent Embed/Dimensions calls.
	dim          atomic.Int64
	organization string
	project      string
	httpClient   *http.Client
	userAgent    string
}

// NewEmbedder constructs an Embedder. Returns an error if APIKey is
// empty.
func NewEmbedder(cfg EmbedderConfig) (*Embedder, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("openai: APIKey is required")
	}
	e := &Embedder{
		apiKey:       cfg.APIKey,
		baseURL:      cfg.BaseURL,
		model:        cfg.Model,
		organization: cfg.Organization,
		project:      cfg.Project,
		httpClient:   cfg.HTTPClient,
		userAgent:    cfg.UserAgent,
	}
	e.dim.Store(int64(cfg.Dim))
	if e.baseURL == "" {
		e.baseURL = defaultBaseURL
	}
	e.baseURL = strings.TrimRight(e.baseURL, "/")
	if e.model == "" {
		e.model = DefaultEmbeddingModel
	}
	if e.httpClient == nil {
		e.httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	return e, nil
}

// Embed implements memory.Embedder. Empty input returns an empty
// slice; otherwise all texts are batched into a single API call.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body := embedRequest{
		Model: e.model,
		Input: texts,
	}
	if d := int(e.dim.Load()); d > 0 {
		body.Dimensions = d
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal embed request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if e.organization != "" {
		req.Header.Set("openai-organization", e.organization)
	}
	if e.project != "" {
		req.Header.Set("openai-project", e.project)
	}
	if e.userAgent != "" {
		req.Header.Set("User-Agent", e.userAgent)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, normalizeEmbedError(resp.StatusCode, raw)
	}
	var out embedResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("openai: decode embed response: %w", err)
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("openai: embed returned %d vectors for %d inputs", len(out.Data), len(texts))
	}
	vecs := make([][]float32, len(out.Data))
	// The API normally returns data ordered by index, but the spec
	// allows arbitrary ordering — honor the index field.
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vecs) {
			return nil, fmt.Errorf("openai: embed response index %d out of bounds", d.Index)
		}
		vecs[d.Index] = d.Embedding
	}
	if len(vecs) > 0 && len(vecs[0]) > 0 {
		// Cache the native dimensionality from the first successful
		// response so Dimensions() doesn't lie. CompareAndSwap keeps a
		// configured Dim authoritative and makes the write race-free.
		e.dim.CompareAndSwap(0, int64(len(vecs[0])))
	}
	return vecs, nil
}

// Dimensions implements memory.Embedder. When the configured Dim is
// zero and no Embed call has succeeded yet, the model's published
// default is returned (1536 for text-embedding-3-small / ada-002,
// 3072 for text-embedding-3-large). Callers that want a hard
// guarantee should set EmbedderConfig.Dim explicitly.
func (e *Embedder) Dimensions() int {
	if d := int(e.dim.Load()); d > 0 {
		return d
	}
	switch e.model {
	case "text-embedding-3-large":
		return 3072
	default:
		return 1536
	}
}

// Model reports the embedding model in use.
func (e *Embedder) Model() string { return e.model }

type embedRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// normalizeEmbedError maps the same error envelope the chat adapter
// uses into a Go error suitable for callers.
func normalizeEmbedError(status int, body []byte) error {
	var env struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	msg := env.Error.Message
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	return fmt.Errorf("openai embed: HTTP %d: %s", status, msg)
}
