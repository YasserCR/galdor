package google

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/YasserCR/galdor/pkg/provider"
)

// normalizeHTTPError converts a Gemini non-2xx response into a galdor
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

	// Google APIs return errors as either a JSON object {"error":{...}}
	// or, when the failure is sufficiently early, a JSON array containing
	// the same shape. Attempt the object form first.
	var er errorResponse
	parsed := false
	if len(body) > 0 && json.Unmarshal(body, &er) == nil && er.Error.Message != "" {
		parsed = true
	} else if len(body) > 0 && body[0] == '[' {
		var arr []errorResponse
		if json.Unmarshal(body, &arr) == nil && len(arr) > 0 && arr[0].Error.Message != "" {
			er = arr[0]
			parsed = true
		}
	}

	if parsed {
		apiErr.Message = er.Error.Message
		// Order matters: details[].reason is more specific than status,
		// which is more specific than the bare HTTP code.
		if k := kindForReason(er.Error.Details); k != nil {
			apiErr.Kind = k
		} else if k := kindForStatusName(er.Error.Status); k != nil {
			apiErr.Kind = k
		}
	}

	if ra := resp.Header.Get("retry-after"); ra != "" {
		if v, err := strconv.Atoi(ra); err == nil {
			apiErr.RetryAfter = v
		}
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

// kindForReason classifies by the canonical google.rpc.ErrorInfo.reason
// strings that appear in error.details[]. These are documented per
// service; this list covers the common cross-service reasons plus the
// ones the Gemini API uses for auth and quota failures.
//
// Promotion via reason is important because Google returns HTTP 400 +
// status=INVALID_ARGUMENT for invalid API keys; only the reason field
// reveals that the underlying issue is authentication.
func kindForReason(details []wireErrorDetail) error {
	for _, d := range details {
		switch d.Reason {
		case "API_KEY_INVALID", "API_KEY_EXPIRED", "API_KEY_MISSING",
			"CREDENTIALS_MISSING", "ACCESS_TOKEN_EXPIRED",
			"CONSUMER_INVALID", "CONSUMER_SUSPENDED":
			return provider.ErrAuth
		case "RATE_LIMIT_EXCEEDED", "QUOTA_EXCEEDED",
			"USER_PROJECT_DENIED":
			return provider.ErrRateLimited
		case "API_DISABLED", "BILLING_DISABLED",
			"SERVICE_DISABLED":
			return provider.ErrAuth
		}
	}
	return nil
}

// kindForStatusName classifies by Google's canonical error.status string.
// See https://cloud.google.com/apis/design/errors#http_mapping.
func kindForStatusName(s string) error {
	switch strings.ToUpper(s) {
	case "UNAUTHENTICATED", "PERMISSION_DENIED":
		return provider.ErrAuth
	case "RESOURCE_EXHAUSTED":
		return provider.ErrRateLimited
	case "INVALID_ARGUMENT", "FAILED_PRECONDITION", "NOT_FOUND", "OUT_OF_RANGE":
		return provider.ErrInvalidRequest
	case "INTERNAL", "UNAVAILABLE", "DEADLINE_EXCEEDED", "UNKNOWN":
		return provider.ErrServer
	}
	return nil
}
