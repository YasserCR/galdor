package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"github.com/YasserCR/galdor/pkg/memory"
)

// Compile-time assertion: Embedder satisfies memory.Embedder.
var _ memory.Embedder = (*Embedder)(nil)

// DefaultEmbeddingModel is used when EmbedderConfig.Model is empty.
// Titan Text Embeddings v2 returns 1024-dim vectors by default and is the
// most cost-effective first-party option on Bedrock.
const DefaultEmbeddingModel = "amazon.titan-embed-text-v2:0"

// defaultEmbeddingDim is the vector size assumed when Dim is unset. It matches
// Titan v2's default and Cohere Embed v3's fixed size.
const defaultEmbeddingDim = 1024

// defaultCohereInputType is sent as `input_type` for Cohere models. Use
// "search_document" when embedding stored chunks and "search_query" when
// embedding a query; "search_document" is the safe default for ingestion.
const defaultCohereInputType = "search_document"

// EmbedderConfig configures an Embedder. Like the chat Provider, AWS carries
// credentials and region (build it with config.LoadDefaultConfig).
type EmbedderConfig struct {
	// AWS is the SDK configuration. Region must be set.
	AWS aws.Config

	// Model is the embedding model ID. Supported families:
	//   - amazon.titan-embed-text-v2:0  (and other amazon.titan-embed* models)
	//   - cohere.embed-multilingual-v3  / cohere.embed-english-v3
	// Defaults to DefaultEmbeddingModel.
	Model string

	// Dim is the vector size reported by Dimensions(). For Titan v2 it is also
	// forwarded as the `dimensions` request field (256, 512 or 1024). Cohere v3
	// is fixed at 1024 and ignores this. Defaults to defaultEmbeddingDim.
	Dim int

	// CohereInputType sets `input_type` for Cohere models ("search_document" or
	// "search_query"). Ignored by Titan. Defaults to "search_document".
	CohereInputType string

	// ClientOptions tweaks bedrockruntime.Options after construction (custom
	// endpoint, retryer, HTTP client). Applied last.
	ClientOptions []func(*bedrockruntime.Options)
}

// Embedder implements memory.Embedder against Amazon Bedrock embedding models
// (Titan v2 and Cohere Embed v3). Safe for concurrent use.
type Embedder struct {
	client          *bedrockruntime.Client
	model           string
	dim             int
	family          string // "titan" | "cohere"
	cohereInputType string
}

// NewEmbedder constructs an Embedder. Returns an error if AWS.Region is empty
// or the model is not a supported embedding family.
func NewEmbedder(cfg EmbedderConfig) (*Embedder, error) {
	if cfg.AWS.Region == "" {
		return nil, errors.New("bedrock: AWS.Region is required")
	}
	model := cfg.Model
	if model == "" {
		model = DefaultEmbeddingModel
	}
	family, err := embeddingFamily(model)
	if err != nil {
		return nil, err
	}
	dim := cfg.Dim
	if dim <= 0 {
		dim = defaultEmbeddingDim
	}
	inputType := cfg.CohereInputType
	if inputType == "" {
		inputType = defaultCohereInputType
	}
	return &Embedder{
		client:          bedrockruntime.NewFromConfig(cfg.AWS, cfg.ClientOptions...),
		model:           model,
		dim:             dim,
		family:          family,
		cohereInputType: inputType,
	}, nil
}

// Model returns the embedding model ID in use.
func (e *Embedder) Model() string { return e.model }

// Dimensions implements memory.Embedder.
func (e *Embedder) Dimensions() int { return e.dim }

// Embed implements memory.Embedder. Empty input returns nil.
//
// Titan accepts a single input per request, so texts are embedded one call at a
// time; Cohere accepts a batch and is embedded in a single call.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if e.family == "cohere" {
		return e.embedCohere(ctx, texts)
	}
	return e.embedTitan(ctx, texts)
}

func (e *Embedder) embedTitan(ctx context.Context, texts []string) ([][]float32, error) {
	v1 := isTitanV1(e.model)
	out := make([][]float32, len(texts))
	for i, t := range texts {
		body, err := titanRequest(t, e.dim, v1)
		if err != nil {
			return nil, err
		}
		raw, err := e.invoke(ctx, body)
		if err != nil {
			return nil, err
		}
		vec, err := parseTitanResponse(raw)
		if err != nil {
			return nil, err
		}
		if len(vec) != e.dim {
			return nil, fmt.Errorf("bedrock: titan returned %d dims, want %d (adjust EmbedderConfig.Dim)", len(vec), e.dim)
		}
		out[i] = vec
	}
	return out, nil
}

// maxCohereBatch is Cohere Embed v3's per-request limit on the number of
// input texts. Larger inputs must be split across calls or the API rejects
// the request.
const maxCohereBatch = 96

func (e *Embedder) embedCohere(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, batch := range chunkStrings(texts) {
		vecs, err := e.embedCohereBatch(ctx, batch)
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// chunkStrings splits s into consecutive sub-slices of at most maxCohereBatch
// elements. The sub-slices share s's backing array; callers must not mutate
// them.
func chunkStrings(s []string) [][]string {
	var chunks [][]string
	for start := 0; start < len(s); start += maxCohereBatch {
		end := start + maxCohereBatch
		if end > len(s) {
			end = len(s)
		}
		chunks = append(chunks, s[start:end])
	}
	return chunks
}

func (e *Embedder) embedCohereBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := cohereRequest(texts, e.cohereInputType)
	if err != nil {
		return nil, err
	}
	raw, err := e.invoke(ctx, body)
	if err != nil {
		return nil, err
	}
	vecs, err := parseCohereResponse(raw)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(texts) {
		return nil, fmt.Errorf("bedrock: cohere returned %d vectors for %d texts", len(vecs), len(texts))
	}
	return vecs, nil
}

func (e *Embedder) invoke(ctx context.Context, body []byte) ([]byte, error) {
	out, err := e.client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(e.model),
		ContentType: aws.String("application/json"),
		Accept:      aws.String("application/json"),
		Body:        body,
	})
	if err != nil {
		return nil, fmt.Errorf("bedrock: invoke %s: %w", e.model, err)
	}
	return out.Body, nil
}

// embeddingFamily maps a model ID to its request/response schema family.
func embeddingFamily(model string) (string, error) {
	switch {
	case strings.HasPrefix(model, "cohere.embed"):
		return "cohere", nil
	case strings.HasPrefix(model, "amazon.titan-embed"):
		return "titan", nil
	default:
		return "", fmt.Errorf("bedrock: unsupported embedding model %q (want amazon.titan-embed* or cohere.embed*)", model)
	}
}

// --- Titan v2 wire format ---

type titanEmbedRequest struct {
	InputText  string `json:"inputText"`
	Dimensions int    `json:"dimensions,omitempty"`
	Normalize  bool   `json:"normalize"`
}

// titanV1Request is the Titan v1 wire shape: v1 only understands inputText
// and REJECTS the v2-only `dimensions` / `normalize` fields, so they must be
// absent entirely (not just zero-valued).
type titanV1Request struct {
	InputText string `json:"inputText"`
}

type titanEmbedResponse struct {
	Embedding []float32 `json:"embedding"`
}

// isTitanV1 reports whether a Titan embedding model is the v1 generation,
// which doesn't support the dimensions/normalize request fields.
func isTitanV1(model string) bool {
	return strings.Contains(model, "embed-text-v1") || strings.Contains(model, "embed-image-v1")
}

func titanRequest(text string, dim int, v1 bool) ([]byte, error) {
	var (
		b   []byte
		err error
	)
	if v1 {
		// v1: only inputText; sending dimensions/normalize errors the API.
		b, err = json.Marshal(titanV1Request{InputText: text})
	} else {
		b, err = json.Marshal(titanEmbedRequest{InputText: text, Dimensions: dim, Normalize: true})
	}
	if err != nil {
		return nil, fmt.Errorf("bedrock: encode titan request: %w", err)
	}
	return b, nil
}

func parseTitanResponse(b []byte) ([]float32, error) {
	var r titanEmbedResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("bedrock: decode titan response: %w", err)
	}
	if len(r.Embedding) == 0 {
		return nil, errors.New("bedrock: titan response had no embedding")
	}
	return r.Embedding, nil
}

// --- Cohere Embed v3 wire format ---

type cohereEmbedRequest struct {
	Texts     []string `json:"texts"`
	InputType string   `json:"input_type"`
}

type cohereEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func cohereRequest(texts []string, inputType string) ([]byte, error) {
	b, err := json.Marshal(cohereEmbedRequest{Texts: texts, InputType: inputType})
	if err != nil {
		return nil, fmt.Errorf("bedrock: encode cohere request: %w", err)
	}
	return b, nil
}

func parseCohereResponse(b []byte) ([][]float32, error) {
	var r cohereEmbedResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("bedrock: decode cohere response: %w", err)
	}
	if len(r.Embeddings) == 0 {
		return nil, errors.New("bedrock: cohere response had no embeddings")
	}
	return r.Embeddings, nil
}
