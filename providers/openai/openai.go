package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/YasserCR/galdor/pkg/provider"
)

// providerName is the stable adapter identifier reported by Name().
const providerName = "openai"

// defaultBaseURL is the production API endpoint. It already includes the
// /v1 path segment so the adapter only needs to append /chat/completions
// — this matches the convention used by OpenAI's own client libraries
// and by every OpenAI-compatible provider's documentation.
const defaultBaseURL = "https://api.openai.com/v1"

// Config configures a Provider. APIKey is required; the rest have sensible
// defaults suitable for production use.
type Config struct {
	// APIKey authenticates against the OpenAI API. Required.
	APIKey string

	// BaseURL overrides the API endpoint. Defaults to api.openai.com.
	// Set this to point at an OpenAI-compatible provider (Groq, Together,
	// MiniMax, Mistral, etc.) — see README for known good values.
	BaseURL string

	// Organization is sent as openai-organization when non-empty.
	Organization string

	// Project is sent as openai-project when non-empty.
	Project string

	// HTTPClient is the transport used for requests. If nil, a client with
	// a 60s timeout is used. Streaming requests run on the same client but
	// rely on context cancellation rather than the client timeout.
	HTTPClient *http.Client

	// UserAgent is appended to the default user-agent string when non-empty.
	UserAgent string
}

// Provider is the OpenAI Chat Completions adapter. Safe for concurrent use.
type Provider struct {
	apiKey       string
	baseURL      string
	organization string
	project      string
	httpClient   *http.Client
	userAgent    string
}

// New constructs a Provider from cfg. Returns an error if APIKey is empty.
func New(cfg Config) (*Provider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("openai: APIKey is required")
	}
	p := &Provider{
		apiKey:       cfg.APIKey,
		baseURL:      cfg.BaseURL,
		organization: cfg.Organization,
		project:      cfg.Project,
		httpClient:   cfg.HTTPClient,
		userAgent:    cfg.UserAgent,
	}
	if p.baseURL == "" {
		p.baseURL = defaultBaseURL
	}
	p.baseURL = strings.TrimRight(p.baseURL, "/")
	if p.httpClient == nil {
		p.httpClient = streamSafeHTTPClient(defaultResponseHeaderTimeout)
	}
	return p, nil
}

// Name implements provider.Provider.
func (p *Provider) Name() string { return providerName }

// Capabilities implements provider.Provider.
//
// PromptCaching is reported as false because OpenAI's caching is automatic
// and does not honor schema.CacheControl hints — they are silently ignored.
// MaxContextTokens varies by model; the reported value matches the long
// context tier of gpt-4o-class models. Callers that need an exact context
// window should consult the model card directly.
func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming:        true,
		ToolCalling:      true,
		StructuredOutput: true,
		PromptCaching:    false,
		VisionInput:      true,
		Reasoning:        true,
		MaxContextTokens: 128_000,
	}
}

// newRequest builds an authenticated HTTP request with the standard headers.
// body may be nil for GETs.
func (p *Provider) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	url := p.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("authorization", "Bearer "+p.apiKey)
	req.Header.Set("content-type", "application/json")
	if p.organization != "" {
		req.Header.Set("openai-organization", p.organization)
	}
	if p.project != "" {
		req.Header.Set("openai-project", p.project)
	}
	ua := "galdor-openai/0.1"
	if p.userAgent != "" {
		ua = ua + " " + p.userAgent
	}
	req.Header.Set("user-agent", ua)
	return req, nil
}

// String reports a developer-friendly description without leaking the key.
func (p *Provider) String() string {
	return fmt.Sprintf("openai.Provider{baseURL=%q}", p.baseURL)
}

// Sanity: the type must satisfy provider.Provider at compile time.
var _ provider.Provider = (*Provider)(nil)
