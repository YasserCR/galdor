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

func TestClassify_WrapsByKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		kind error
		check func(t *testing.T, err error)
	}{
		{
			name: "rate-limit",
			kind: ErrRateLimited,
			check: func(t *testing.T, err error) {
				var rl *RateLimitError
				if !errors.As(err, &rl) {
					t.Fatalf("errors.As(*RateLimitError) failed: %v", err)
				}
				if rl.RetryAfter != 5 {
					t.Errorf("RetryAfter = %d, want 5", rl.RetryAfter)
				}
			},
		},
		{
			name: "auth",
			kind: ErrAuth,
			check: func(t *testing.T, err error) {
				var ae *AuthError
				if !errors.As(err, &ae) {
					t.Fatalf("errors.As(*AuthError) failed: %v", err)
				}
			},
		},
		{
			name: "invalid-request",
			kind: ErrInvalidRequest,
			check: func(t *testing.T, err error) {
				var ire *InvalidRequestError
				if !errors.As(err, &ire) {
					t.Fatalf("errors.As(*InvalidRequestError) failed: %v", err)
				}
			},
		},
		{
			name: "server-transient",
			kind: ErrServer,
			check: func(t *testing.T, err error) {
				var te *TransientError
				if !errors.As(err, &te) {
					t.Fatalf("errors.As(*TransientError) failed: %v", err)
				}
			},
		},
		{
			name: "context-window",
			kind: ErrContextWindow,
			check: func(t *testing.T, err error) {
				var ce *ContextLengthError
				if !errors.As(err, &ce) {
					t.Fatalf("errors.As(*ContextLengthError) failed: %v", err)
				}
			},
		},
		{
			name: "unsupported",
			kind: ErrUnsupported,
			check: func(t *testing.T, err error) {
				var ue *UnsupportedError
				if !errors.As(err, &ue) {
					t.Fatalf("errors.As(*UnsupportedError) failed: %v", err)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := Classify(&APIError{
				Kind:       c.kind,
				Provider:   "test",
				StatusCode: 429,
				RetryAfter: 5,
				Message:    "boom",
			})
			c.check(t, err)
			// Backward-compat: errors.Is still matches the sentinel.
			if !errors.Is(err, c.kind) {
				t.Errorf("errors.Is sentinel failed for %v", c.kind)
			}
			// Backward-compat: errors.As to *APIError still works.
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Errorf("errors.As(*APIError) failed for %v", c.kind)
			}
		})
	}
}

func TestClassify_NilReturnsNil(t *testing.T) {
	t.Parallel()
	if got := Classify(nil); got != nil {
		t.Errorf("Classify(nil) = %v, want nil", got)
	}
}

func TestClassify_UnknownKindPassThrough(t *testing.T) {
	t.Parallel()
	in := &APIError{Provider: "anthropic"} // Kind is nil
	out := Classify(in)
	if out != in {
		t.Errorf("Classify with nil Kind should return input unchanged; got %T", out)
	}

	unknown := errors.New("custom sentinel not in galdor")
	in2 := &APIError{Kind: unknown, Provider: "openai"}
	out2 := Classify(in2)
	if out2 != in2 {
		t.Errorf("Classify with unknown Kind should return input unchanged; got %T", out2)
	}
}

func TestRateLimitError_ErrorMessageDelegated(t *testing.T) {
	t.Parallel()
	err := Classify(&APIError{
		Kind: ErrRateLimited, Provider: "openai", Message: "too many requests",
	})
	want := "openai: too many requests"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestBadOutputError(t *testing.T) {
	t.Parallel()
	cause := errors.New("unexpected end of JSON input")
	err := &BadOutputError{
		Provider: "google",
		Raw:      "{partial",
		Reason:   "invalid JSON",
		Cause:    cause,
	}
	if got := err.Error(); got != "google: bad output: invalid JSON" {
		t.Errorf("Error() = %q", got)
	}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(err, cause) = false; want true")
	}
}

func TestBadOutputError_DefaultPrefix(t *testing.T) {
	t.Parallel()
	err := &BadOutputError{Reason: "trailing prose"}
	if got := err.Error(); got != "schema: bad output: trailing prose" {
		t.Errorf("Error() = %q", got)
	}
}

func TestBadOutputError_NilSafe(t *testing.T) {
	t.Parallel()
	var e *BadOutputError
	if got := e.Error(); got != "<nil>" {
		t.Errorf("nil BadOutputError.Error() = %q", got)
	}
}

// Regression: the existing Retry middleware classifies by errors.Is on
// the unwrap chain. Wrapping in a typed struct must not break that.
func TestTypedError_RetryClassification(t *testing.T) {
	t.Parallel()
	err := Classify(&APIError{Kind: ErrRateLimited})
	if !IsRetryable(err) {
		t.Errorf("IsRetryable(*RateLimitError) = false; want true")
	}
	err = Classify(&APIError{Kind: ErrServer})
	if !IsRetryable(err) {
		t.Errorf("IsRetryable(*TransientError) = false; want true")
	}
	err = Classify(&APIError{Kind: ErrAuth})
	if IsRetryable(err) {
		t.Errorf("IsRetryable(*AuthError) = true; want false")
	}
}
