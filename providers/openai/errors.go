package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/YasserCR/galdor/pkg/provider"
)

// normalizeHTTPError converts an OpenAI non-2xx response into a galdor
// *provider.APIError. resp.Body is consumed; the caller must not read it
// after this returns.
func (p *Provider) normalizeHTTPError(resp *http.Response) error {
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	apiErr := &provider.APIError{
		Provider:   p.name,
		StatusCode: resp.StatusCode,
		Kind:       kindForStatus(resp.StatusCode),
	}

	var er errorResponse
	// The decode error is deliberately ignored rather than used as a
	// gate. encoding/json fills in every field it could before reporting
	// a type mismatch, so discarding the result over one cosmetic field
	// cost the caller the only human-readable account of the failure.
	_ = json.Unmarshal(body, &er)
	if er.Error.Message != "" {
		apiErr.Message = er.Error.Message
		if k := kindForType(er.Error.Type, string(er.Error.Code)); k != nil {
			apiErr.Kind = k
		}
	}
	// Gateways that route to a third-party upstream answer with a generic
	// top-level message ("Provider returned error") and put the actual
	// cause in error.metadata. Append it so the informative part of the
	// envelope survives.
	if meta := formatErrorMetadata(er.Error.Metadata); meta != "" {
		if apiErr.Message != "" {
			apiErr.Message += " (" + meta + ")"
		} else {
			apiErr.Message = meta
		}
	}
	// A body that did not parse into the expected envelope is still the
	// only account of the failure there is — keep it, truncated, rather
	// than reporting a bare status code.
	if apiErr.Message == "" {
		if b := bytes.TrimSpace(body); len(b) > 0 {
			apiErr.Message = truncateBody(string(b))
		}
	}

	if v, ok := provider.ParseRetryAfter(resp.Header.Get("retry-after"), time.Now()); ok {
		apiErr.RetryAfter = v
	}
	return provider.Classify(apiErr)
}

// errorMetadata is the error.metadata shape OpenRouter documents for
// upstream failures: the upstream's raw response plus its name. Other
// gateways may put anything here, so the typed decode is best-effort and
// the raw JSON is the fallback.
type errorMetadata struct {
	ProviderName string          `json:"provider_name"`
	Raw          json.RawMessage `json:"raw"`
}

// formatErrorMetadata renders error.metadata for the APIError message.
// An empty result means there was nothing worth carrying.
func formatErrorMetadata(meta json.RawMessage) string {
	m := bytes.TrimSpace(meta)
	if len(m) == 0 || bytes.Equal(m, []byte("null")) || bytes.Equal(m, []byte("{}")) {
		return ""
	}
	var em errorMetadata
	if err := json.Unmarshal(m, &em); err == nil && (em.ProviderName != "" || len(em.Raw) > 0) {
		raw := strings.TrimSpace(rawToText(em.Raw))
		switch {
		case em.ProviderName != "" && raw != "":
			return "upstream " + em.ProviderName + ": " + truncateBody(raw)
		case em.ProviderName != "":
			return "upstream " + em.ProviderName
		default:
			return "upstream: " + truncateBody(raw)
		}
	}
	return "metadata: " + truncateBody(string(m))
}

// rawToText unquotes a JSON string, and otherwise returns the literal
// JSON text — error.metadata.raw is documented as a string holding the
// upstream body, but nothing stops a gateway from inlining an object.
func rawToText(raw json.RawMessage) string {
	b := bytes.TrimSpace(raw)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return ""
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err == nil {
			return s
		}
	}
	return string(b)
}

// maxErrorBodyLen caps verbatim body fragments carried inside an error
// message, so a gateway's HTML error page doesn't flood logs.
const maxErrorBodyLen = 512

func truncateBody(s string) string {
	if len(s) <= maxErrorBodyLen {
		return s
	}
	return strings.ToValidUTF8(s[:maxErrorBodyLen], "") + "... (truncated)"
}

func kindForStatus(code int) error {
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return provider.ErrAuth
	case code == http.StatusTooManyRequests:
		return provider.ErrRateLimited
	case code >= 500:
		return provider.ErrServer
	case code >= 400:
		return provider.ErrInvalidRequest
	default:
		return nil
	}
}

// kindForType classifies by OpenAI's error.type and error.code fields when
// the bare status code is ambiguous (some OpenAI-compatible providers
// reuse 400 for context-window errors, for instance).
func kindForType(t, code string) error {
	switch t {
	case "invalid_request_error":
		if code == "context_length_exceeded" {
			return provider.ErrContextWindow
		}
		return provider.ErrInvalidRequest
	case "authentication_error", "permission_error":
		return provider.ErrAuth
	case "rate_limit_error", "tokens_exceeded":
		return provider.ErrRateLimited
	case "server_error", "internal_server_error":
		return provider.ErrServer
	}
	switch code {
	case "context_length_exceeded":
		return provider.ErrContextWindow
	case "rate_limit_exceeded":
		return provider.ErrRateLimited
	case "invalid_api_key":
		return provider.ErrAuth
	}
	return nil
}
