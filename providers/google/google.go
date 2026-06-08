package google

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/YasserCR/galdor/pkg/provider"
)

// providerName is the stable adapter identifier reported by Name().
const providerName = "google"

// defaultBaseURL is the AI Studio / Generative Language API endpoint.
// Includes the version segment so request paths only need the
// model-specific suffix.
const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// Config configures a Provider. APIKey is required; the rest have sensible
// defaults suitable for production use.
type Config struct {
	// APIKey authenticates against the AI Studio API. Required. AI Studio
	// keys begin with "AIza" and are passed via the x-goog-api-key header.
	APIKey string

	// BaseURL overrides the API endpoint. Defaults to
	// generativelanguage.googleapis.com/v1beta. Set this to point at a
	// proxy or a Vertex AI-style gateway.
	BaseURL string

	// HTTPClient is the transport used for requests. If nil, a client
	// with a 60s timeout is used. Streaming requests run on the same
	// client but rely on context cancellation rather than the client
	// timeout.
	HTTPClient *http.Client

	// UserAgent is appended to the default user-agent string when non-empty.
	UserAgent string
}

// Provider is the Google Gemini adapter. Safe for concurrent use.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	userAgent  string
}

// New constructs a Provider from cfg. Returns an error if APIKey is empty.
func New(cfg Config) (*Provider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("google: APIKey is required")
	}
	p := &Provider{
		apiKey:     cfg.APIKey,
		baseURL:    cfg.BaseURL,
		httpClient: cfg.HTTPClient,
		userAgent:  cfg.UserAgent,
	}
	if p.baseURL == "" {
		p.baseURL = defaultBaseURL
	}
	p.baseURL = strings.TrimRight(p.baseURL, "/")
	if p.httpClient == nil {
		p.httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	return p, nil
}

// Name implements provider.Provider.
func (p *Provider) Name() string { return providerName }

// Capabilities implements provider.Provider.
//
// PromptCaching is reported false: although Gemini's CachedContent
// feature lets callers reuse long prompt prefixes, this adapter does
// not yet wire schema.CacheControl hints into a CachedContent resource.
// Reporting it honestly lets callers fall back rather than silently
// getting an uncached prompt; users that need caching today can
// construct a CachedContent out-of-band and reference it via metadata.
// Flip this to true only alongside the wiring.
// MaxContextTokens reflects Gemini 2.5-class context windows.
func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming:        true,
		ToolCalling:      true,
		StructuredOutput: true,
		PromptCaching:    false,
		VisionInput:      true,
		Reasoning:        true,
		MaxContextTokens: 1_048_576,
	}
}

// newRequest builds an authenticated HTTP request with the standard headers.
// path should be relative (e.g. "/models/gemini-2.5-flash:generateContent").
func (p *Provider) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	url := p.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-goog-api-key", p.apiKey)
	req.Header.Set("content-type", "application/json")
	ua := "galdor-google/0.1"
	if p.userAgent != "" {
		ua = ua + " " + p.userAgent
	}
	req.Header.Set("user-agent", ua)
	return req, nil
}

// modelPath returns the URL path for an operation on a specific model.
func modelPath(model, op string) string {
	return fmt.Sprintf("/models/%s:%s", model, op)
}

// String reports a developer-friendly description without leaking the key.
func (p *Provider) String() string {
	return fmt.Sprintf("google.Provider{baseURL=%q}", p.baseURL)
}

// Sanity: the type must satisfy provider.Provider at compile time.
var _ provider.Provider = (*Provider)(nil)
