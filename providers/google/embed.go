package google

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

// Compile-time assertion: Embedder satisfies memory.Embedder.
var _ memory.Embedder = (*Embedder)(nil)

// DefaultEmbeddingModel is the model used when EmbedderConfig.Model
// is empty. text-embedding-004 produces 768-dim vectors and is the
// current general-purpose Gemini embedding model.
const DefaultEmbeddingModel = "text-embedding-004"

// EmbedderConfig configures an Embedder against Google's Generative
// Language (AI Studio) API. For Vertex AI, override BaseURL and
// provide a custom HTTPClient that injects an OAuth2 access token.
type EmbedderConfig struct {
	// APIKey authenticates against the AI Studio API. Required for
	// the default BaseURL; ignored when the caller's HTTPClient
	// handles auth itself.
	APIKey string

	// BaseURL overrides the API endpoint. Defaults to
	// generativelanguage.googleapis.com/v1beta.
	BaseURL string

	// Model is the embedding model ID, e.g. text-embedding-004 or
	// gemini-embedding-001. Defaults to DefaultEmbeddingModel. The
	// `models/` prefix is added automatically; both forms accepted.
	Model string

	// Dim is the embedding dimensionality reported by Dimensions()
	// and forwarded to the API as `outputDimensionality` when > 0.
	// When zero, the model's native dimensionality is used and
	// Embedder caches it from the first successful Embed call.
	Dim int

	// HTTPClient is the transport used for requests. Defaults to a
	// client with a 60s timeout.
	HTTPClient *http.Client

	// UserAgent is appended to the default user-agent string when
	// non-empty.
	UserAgent string
}

// Embedder implements memory.Embedder against Google's
// batchEmbedContents endpoint. Safe for concurrent use.
type Embedder struct {
	apiKey     string
	baseURL    string
	model      string
	dim        int
	httpClient *http.Client
	userAgent  string
}

// NewEmbedder constructs an Embedder. Returns an error if APIKey is
// empty AND no custom HTTPClient was supplied (Vertex callers handle
// auth via the client and may legitimately leave APIKey blank).
func NewEmbedder(cfg EmbedderConfig) (*Embedder, error) {
	if strings.TrimSpace(cfg.APIKey) == "" && cfg.HTTPClient == nil {
		return nil, errors.New("google: APIKey or HTTPClient is required")
	}
	e := &Embedder{
		apiKey:     cfg.APIKey,
		baseURL:    cfg.BaseURL,
		model:      cfg.Model,
		dim:        cfg.Dim,
		httpClient: cfg.HTTPClient,
		userAgent:  cfg.UserAgent,
	}
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
// slice; otherwise all texts are batched into a single
// batchEmbedContents call.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	modelPath := e.modelPath()
	body := embedBatchRequest{
		Requests: make([]embedSingleRequest, len(texts)),
	}
	for i, t := range texts {
		body.Requests[i] = embedSingleRequest{
			Model:   modelPath,
			Content: embedContent{Parts: []embedPart{{Text: t}}},
		}
		if e.dim > 0 {
			body.Requests[i].OutputDimensionality = e.dim
		}
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("google: marshal embed request: %w", err)
	}
	url := fmt.Sprintf("%s/%s:batchEmbedContents", e.baseURL, modelPath)
	if e.apiKey != "" {
		url += "?key=" + e.apiKey
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if e.userAgent != "" {
		req.Header.Set("User-Agent", e.userAgent)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, normalizeEmbedError(resp.StatusCode, raw)
	}
	var out embedBatchResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("google: decode embed response: %w", err)
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("google: embed returned %d vectors for %d inputs", len(out.Embeddings), len(texts))
	}
	vecs := make([][]float32, len(out.Embeddings))
	for i, em := range out.Embeddings {
		vecs[i] = em.Values
	}
	if e.dim == 0 && len(vecs) > 0 && len(vecs[0]) > 0 {
		e.dim = len(vecs[0])
	}
	return vecs, nil
}

// Dimensions implements memory.Embedder. When the configured Dim is
// zero and no Embed call has succeeded yet, the model's published
// default is returned (768 for text-embedding-004,
// gemini-embedding-001 returns variable sizes — set Dim explicitly
// for those).
func (e *Embedder) Dimensions() int {
	if e.dim > 0 {
		return e.dim
	}
	return 768
}

// Model reports the embedding model in use.
func (e *Embedder) Model() string { return e.model }

func (e *Embedder) modelPath() string {
	if strings.HasPrefix(e.model, "models/") {
		return e.model
	}
	return "models/" + e.model
}

type embedBatchRequest struct {
	Requests []embedSingleRequest `json:"requests"`
}

type embedSingleRequest struct {
	Model                string       `json:"model"`
	Content              embedContent `json:"content"`
	OutputDimensionality int          `json:"outputDimensionality,omitempty"`
}

type embedContent struct {
	Parts []embedPart `json:"parts"`
}

type embedPart struct {
	Text string `json:"text"`
}

type embedBatchResponse struct {
	Embeddings []struct {
		Values []float32 `json:"values"`
	} `json:"embeddings"`
}

func normalizeEmbedError(status int, body []byte) error {
	var env struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	msg := env.Error.Message
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	return fmt.Errorf("google embed: HTTP %d: %s", status, msg)
}
