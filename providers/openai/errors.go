package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/YasserCR/galdor/pkg/provider"
)

// normalizeHTTPError converts an OpenAI non-2xx response into a galdor
// *provider.APIError. resp.Body is consumed; the caller must not read it
// after this returns.
func normalizeHTTPError(resp *http.Response) error {
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	apiErr := &provider.APIError{
		Provider:   providerName,
		StatusCode: resp.StatusCode,
		Kind:       kindForStatus(resp.StatusCode),
	}

	var er errorResponse
	if len(body) > 0 && json.Unmarshal(body, &er) == nil && er.Error.Message != "" {
		apiErr.Message = er.Error.Message
		if k := kindForType(er.Error.Type, er.Error.Code); k != nil {
			apiErr.Kind = k
		}
	}

	if v, ok := provider.ParseRetryAfter(resp.Header.Get("retry-after"), time.Now()); ok {
		apiErr.RetryAfter = v
	}
	return provider.Classify(apiErr)
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
