package anthropic

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/YasserCR/galdor/pkg/provider"
)

// normalizeHTTPError converts an Anthropic non-2xx response into a galdor
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
		// Anthropic's documented error.type values give us a finer
		// classification than the bare status code; promote when present.
		if k := kindForType(er.Error.Type); k != nil {
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

func kindForType(t string) error {
	switch t {
	case "authentication_error", "permission_error":
		return provider.ErrAuth
	case "rate_limit_error", "overloaded_error":
		return provider.ErrRateLimited
	case "invalid_request_error", "not_found_error":
		return provider.ErrInvalidRequest
	case "api_error":
		return provider.ErrServer
	default:
		return nil
	}
}
