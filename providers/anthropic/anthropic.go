package anthropic

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
const providerName = "anthropic"

// defaultBaseURL is the production API endpoint.
const defaultBaseURL = "https://api.anthropic.com"

// defaultAPIVersion is the value sent in the anthropic-version header.
// Bump in lockstep with deliberate adapter upgrades.
const defaultAPIVersion = "2023-06-01"

// Config configures a Provider. APIKey is required; the rest have sensible
// defaults suitable for production use.
type Config struct {
	// APIKey authenticates against the Anthropic API. Required.
	APIKey string

	// BaseURL overrides the API endpoint. Defaults to api.anthropic.com.
	// Useful for mock servers and self-hosted gateways.
	BaseURL string

	// APIVersion is sent as anthropic-version. Defaults to "2023-06-01".
	APIVersion string

	// HTTPClient is the transport used for requests. If nil, a client with
	// a 60s timeout is used. Streaming requests run on the same client but
	// rely on context cancellation rather than the client timeout.
	HTTPClient *http.Client

	// UserAgent is appended to the default user-agent string when non-empty.
	UserAgent string
}

// Provider is the Anthropic adapter. Safe for concurrent use.
type Provider struct {
	apiKey     string
	baseURL    string
	apiVersion string
	httpClient *http.Client
	userAgent  string
}

// New constructs a Provider from cfg. Returns an error if APIKey is empty.
func New(cfg Config) (*Provider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("anthropic: APIKey is required")
	}
	p := &Provider{
		apiKey:     cfg.APIKey,
		baseURL:    cfg.BaseURL,
		apiVersion: cfg.APIVersion,
		httpClient: cfg.HTTPClient,
		userAgent:  cfg.UserAgent,
	}
	if p.baseURL == "" {
		p.baseURL = defaultBaseURL
	}
	p.baseURL = strings.TrimRight(p.baseURL, "/")
	if p.apiVersion == "" {
		p.apiVersion = defaultAPIVersion
	}
	if p.httpClient == nil {
		p.httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	return p, nil
}

// Name implements provider.Provider.
func (p *Provider) Name() string { return providerName }

// Capabilities implements provider.Provider.
func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming:        true,
		ToolCalling:      true,
		StructuredOutput: false, // No JSON-mode parity yet; tools cover the common case.
		PromptCaching:    true,
		VisionInput:      true,
		MaxContextTokens: 200_000,
	}
}

// newRequest builds an authenticated HTTP request with the standard headers.
// body must be a JSON-marshalable struct (or nil for GETs).
func (p *Provider) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	url := p.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", p.apiVersion)
	req.Header.Set("content-type", "application/json")
	ua := "galdor-anthropic/0.1"
	if p.userAgent != "" {
		ua = ua + " " + p.userAgent
	}
	req.Header.Set("user-agent", ua)
	return req, nil
}

// String reports a developer-friendly description without leaking the key.
func (p *Provider) String() string {
	return fmt.Sprintf("anthropic.Provider{baseURL=%q version=%q}", p.baseURL, p.apiVersion)
}

// Sanity: the type must satisfy provider.Provider at compile time.
var _ provider.Provider = (*Provider)(nil)
