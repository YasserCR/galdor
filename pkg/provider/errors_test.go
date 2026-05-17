package provider

import (
	"errors"
	"testing"
)

func TestAPIError_IsMatchesKind(t *testing.T) {
	t.Parallel()
	err := &APIError{Kind: ErrRateLimited, Provider: "anthropic", StatusCode: 429, RetryAfter: 5}
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("errors.Is(err, ErrRateLimited) = false; want true")
	}
	if errors.Is(err, ErrAuth) {
		t.Errorf("errors.Is(err, ErrAuth) = true; want false")
	}
}

func TestAPIError_ErrorString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  *APIError
		want string
	}{
		{
			name: "message takes precedence",
			err:  &APIError{Provider: "openai", Kind: ErrAuth, Message: "invalid api key"},
			want: "openai: invalid api key",
		},
		{
			name: "falls back to kind",
			err:  &APIError{Provider: "openai", Kind: ErrAuth},
			want: "openai: authentication failed",
		},
		{
			name: "missing provider gets default prefix",
			err:  &APIError{Kind: ErrServer},
			want: "provider: provider server error",
		},
		{
			name: "no kind, no message",
			err:  &APIError{Provider: "anthropic"},
			want: "anthropic: unknown error",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.err.Error(); got != c.want {
				t.Errorf("Error() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAPIError_NilSafe(t *testing.T) {
	t.Parallel()
	var e *APIError
	if got := e.Error(); got != "<nil>" {
		t.Errorf("nil APIError.Error() = %q", got)
	}
}

func TestErrorAs(t *testing.T) {
	t.Parallel()
	var raw error = &APIError{Kind: ErrInvalidRequest, Provider: "openai", StatusCode: 400}
	var apiErr *APIError
	if !errors.As(raw, &apiErr) {
		t.Fatal("errors.As failed")
	}
	if apiErr.StatusCode != 400 {
		t.Errorf("StatusCode = %d", apiErr.StatusCode)
	}
}
