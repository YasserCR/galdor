package providerset

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/providers/anthropic"
	"github.com/YasserCR/galdor/providers/bedrock"
	"github.com/YasserCR/galdor/providers/google"
	"github.com/YasserCR/galdor/providers/openai"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// Config selects and configures a Provider at runtime. The zero value
// is invalid: Provider must be set.
type Config struct {
	// Provider names the backend to construct. Native adapters:
	//
	//   "anthropic" | "openai" | "google" | "bedrock"
	//
	// OpenAI-compatible aliases (resolve to providers/openai with a
	// preset BaseURL):
	//
	//   "groq" | "together" | "mistral" | "minimax" | "deepseek"
	//   | "vllm" | "ollama"
	//
	// The value is case-insensitive and trimmed.
	Provider string

	// APIKey authenticates against the chosen backend. Required for
	// anthropic, openai, google and the OpenAI-compatible hosted
	// aliases. Ignored by bedrock (which uses the AWS credential
	// chain) and by vllm/ollama when blank (a placeholder is
	// substituted so providers/openai's required-key check passes).
	APIKey string

	// BaseURL overrides the endpoint the adapter talks to. Honoured
	// by every provider except bedrock. For OpenAI-compatible
	// aliases, an explicit BaseURL wins over the table default.
	BaseURL string

	// HTTPClient is the transport handed to the underlying adapter.
	// Nil falls back to the adapter's default (60s timeout).
	HTTPClient *http.Client
}

// New returns the Provider matching cfg. Unknown Provider strings
// return a descriptive error. Errors never include APIKey or
// HTTPClient state.
func New(cfg Config) (provider.Provider, error) {
	name := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if name == "" {
		return nil, errors.New("providerset: Provider is required")
	}

	switch name {
	case "anthropic":
		return anthropic.New(anthropic.Config{
			APIKey:     cfg.APIKey,
			BaseURL:    cfg.BaseURL,
			HTTPClient: cfg.HTTPClient,
		})

	case "openai":
		return openai.New(openai.Config{
			APIKey:     cfg.APIKey,
			BaseURL:    cfg.BaseURL,
			HTTPClient: cfg.HTTPClient,
		})

	case "google":
		return google.New(google.Config{
			APIKey:     cfg.APIKey,
			BaseURL:    cfg.BaseURL,
			HTTPClient: cfg.HTTPClient,
		})

	case "bedrock":
		// Bedrock pulls credentials and region from the standard
		// AWS chain (env, ~/.aws, IAM role, SSO, IMDS). cfg.APIKey
		// and cfg.BaseURL are intentionally ignored — Bedrock has
		// no global endpoint and no static API key concept.
		awsCfg, err := awsconfig.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("providerset: load AWS config for bedrock: %w", err)
		}
		return bedrock.New(bedrock.Config{AWS: awsCfg})
	}

	if baseURL, ok := openAICompatBaseURLs[name]; ok {
		resolved := cfg.BaseURL
		if resolved == "" {
			resolved = baseURL
		}
		apiKey := cfg.APIKey
		if apiKey == "" && openAICompatNoAuth[name] {
			// providers/openai rejects an empty APIKey. Self-hosted
			// vLLM and ollama do not need one; substitute a stable
			// placeholder so the adapter constructs cleanly.
			apiKey = "no-key"
		}
		return openai.New(openai.Config{
			APIKey:     apiKey,
			BaseURL:    resolved,
			HTTPClient: cfg.HTTPClient,
		})
	}

	return nil, fmt.Errorf("providerset: unknown Provider %q", name)
}

// FromEnv reads LLM_PROVIDER, LLM_API_KEY, LLM_BASE_URL and
// LLM_HTTP_TIMEOUT and returns the configured Provider. Returns an
// error when LLM_PROVIDER is unset or unknown.
//
// LLM_HTTP_TIMEOUT accepts any time.ParseDuration value (e.g. "90s",
// "2m"); when set it installs a *http.Client with that timeout.
// Otherwise the adapter's own default applies.
func FromEnv() (provider.Provider, error) {
	name := strings.TrimSpace(os.Getenv("LLM_PROVIDER"))
	if name == "" {
		return nil, errors.New("providerset: LLM_PROVIDER is not set")
	}

	cfg := Config{
		Provider: name,
		APIKey:   os.Getenv("LLM_API_KEY"),
		BaseURL:  strings.TrimSpace(os.Getenv("LLM_BASE_URL")),
	}

	if raw := strings.TrimSpace(os.Getenv("LLM_HTTP_TIMEOUT")); raw != "" {
		d, err := parseTimeout(raw)
		if err != nil {
			return nil, fmt.Errorf("providerset: invalid LLM_HTTP_TIMEOUT: %w", err)
		}
		cfg.HTTPClient = &http.Client{Timeout: d}
	}

	return New(cfg)
}

// parseTimeout accepts a duration string (preferred) or a bare integer
// interpreted as seconds for compatibility with shell scripts that
// export raw numbers.
func parseTimeout(raw string) (time.Duration, error) {
	if d, err := time.ParseDuration(raw); err == nil {
		if d <= 0 {
			return 0, fmt.Errorf("must be positive, got %q", raw)
		}
		return d, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("expected duration (e.g. 60s) or integer seconds, got %q", raw)
	}
	if n <= 0 {
		return 0, fmt.Errorf("must be positive, got %q", raw)
	}
	return time.Duration(n) * time.Second, nil
}
