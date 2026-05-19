package provider_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// scriptedRetryProv returns the configured responses in order. When
// the response is an error, that error is returned (and the
// corresponding response slot is ignored). Calls increment a counter
// so tests can assert how many attempts the retry wrapper made.
type scriptedRetryProv struct {
	responses []scriptedResponse
	calls     atomic.Int32
}

type scriptedResponse struct {
	resp *provider.Response
	err  error
}

func (*scriptedRetryProv) Name() string                       { return "scripted-retry" }
func (*scriptedRetryProv) Capabilities() provider.Capabilities { return provider.Capabilities{} }
func (*scriptedRetryProv) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}

func (p *scriptedRetryProv) Generate(_ context.Context, _ provider.Request) (*provider.Response, error) {
	i := int(p.calls.Add(1)) - 1
	if i >= len(p.responses) {
		return nil, errors.New("scripted: plan exhausted")
	}
	return p.responses[i].resp, p.responses[i].err
}

func TestRetry_SucceedsOnFirstAttempt(t *testing.T) {
	t.Parallel()
	p := &scriptedRetryProv{responses: []scriptedResponse{
		{resp: &provider.Response{Message: schema.AssistantMessage("ok"), StopReason: schema.StopReasonEndTurn}},
	}}
	wrapped := provider.Retry(p, provider.RetryConfig{MaxAttempts: 3, InitialDelay: time.Millisecond})
	resp, err := wrapped.Generate(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resp.Message.Text() != "ok" {
		t.Errorf("response = %q", resp.Message.Text())
	}
	if got := p.calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
}

func TestRetry_RetriesOnRateLimitedThenSucceeds(t *testing.T) {
	t.Parallel()
	p := &scriptedRetryProv{responses: []scriptedResponse{
		{err: &provider.APIError{Kind: provider.ErrRateLimited, Message: "slow down", RetryAfter: 0}},
		{err: &provider.APIError{Kind: provider.ErrRateLimited, Message: "slow down"}},
		{resp: &provider.Response{Message: schema.AssistantMessage("finally"), StopReason: schema.StopReasonEndTurn}},
	}}
	wrapped := provider.Retry(p, provider.RetryConfig{
		MaxAttempts:  3,
		InitialDelay: time.Millisecond, // tiny — tests should be fast
		Multiplier:   1.0,              // skip exponential growth so the test stays predictable
		Jitter:       0,
	})
	resp, err := wrapped.Generate(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resp.Message.Text() != "finally" {
		t.Errorf("response = %q", resp.Message.Text())
	}
	if got := p.calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3 (two retries + success)", got)
	}
}

func TestRetry_RetriesOnServerError(t *testing.T) {
	t.Parallel()
	p := &scriptedRetryProv{responses: []scriptedResponse{
		{err: &provider.APIError{Kind: provider.ErrServer, StatusCode: 502, Message: "bad gateway"}},
		{resp: &provider.Response{Message: schema.AssistantMessage("recovered")}},
	}}
	wrapped := provider.Retry(p, provider.RetryConfig{MaxAttempts: 3, InitialDelay: time.Millisecond, Multiplier: 1, Jitter: 0})
	resp, err := wrapped.Generate(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resp.Message.Text() != "recovered" {
		t.Errorf("response = %q", resp.Message.Text())
	}
	if got := p.calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
}

func TestRetry_DoesNotRetryAuth(t *testing.T) {
	t.Parallel()
	p := &scriptedRetryProv{responses: []scriptedResponse{
		{err: &provider.APIError{Kind: provider.ErrAuth, StatusCode: 401, Message: "bad key"}},
	}}
	wrapped := provider.Retry(p, provider.RetryConfig{MaxAttempts: 5, InitialDelay: time.Millisecond})
	_, err := wrapped.Generate(context.Background(), provider.Request{})
	if !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if got := p.calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1 (auth errors must not retry)", got)
	}
}

func TestRetry_DoesNotRetryInvalidRequest(t *testing.T) {
	t.Parallel()
	p := &scriptedRetryProv{responses: []scriptedResponse{
		{err: &provider.APIError{Kind: provider.ErrInvalidRequest, Message: "model not found"}},
	}}
	wrapped := provider.Retry(p, provider.RetryConfig{MaxAttempts: 5, InitialDelay: time.Millisecond})
	_, err := wrapped.Generate(context.Background(), provider.Request{})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
	if got := p.calls.Load(); got != 1 {
		t.Errorf("calls = %d", got)
	}
}

func TestRetry_ExhaustsAttemptsThenFails(t *testing.T) {
	t.Parallel()
	plan := []scriptedResponse{}
	for i := 0; i < 5; i++ {
		plan = append(plan, scriptedResponse{err: &provider.APIError{Kind: provider.ErrServer}})
	}
	p := &scriptedRetryProv{responses: plan}
	wrapped := provider.Retry(p, provider.RetryConfig{MaxAttempts: 3, InitialDelay: time.Millisecond, Multiplier: 1, Jitter: 0})
	_, err := wrapped.Generate(context.Background(), provider.Request{})
	if err == nil {
		t.Fatal("expected error after MaxAttempts")
	}
	if !errors.Is(err, provider.ErrServer) {
		t.Errorf("err should still classify as ErrServer: %v", err)
	}
	if got := p.calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3 (MaxAttempts)", got)
	}
}

func TestRetry_RespectsRetryAfterHeader(t *testing.T) {
	t.Parallel()
	// Server says: wait 2 seconds. Our InitialDelay is 1ms, so if
	// we ignore RetryAfter we'd finish in <100ms. Verify we slept
	// ~2s by measuring elapsed time.
	p := &scriptedRetryProv{responses: []scriptedResponse{
		{err: &provider.APIError{Kind: provider.ErrRateLimited, RetryAfter: 1}}, // 1 second
		{resp: &provider.Response{Message: schema.AssistantMessage("ok")}},
	}}
	wrapped := provider.Retry(p, provider.RetryConfig{
		MaxAttempts:  3,
		InitialDelay: time.Millisecond, // would be way too short without RetryAfter
		Multiplier:   1,
		Jitter:       0, // exact match for the assertion
	})
	start := time.Now()
	_, err := wrapped.Generate(context.Background(), provider.Request{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("elapsed = %v, expected ~1s (RetryAfter=1 should override InitialDelay=1ms)", elapsed)
	}
}

func TestRetry_AbortsOnContextCancellation(t *testing.T) {
	t.Parallel()
	p := &scriptedRetryProv{responses: []scriptedResponse{
		{err: &provider.APIError{Kind: provider.ErrServer}},
		{err: &provider.APIError{Kind: provider.ErrServer}},
		{err: &provider.APIError{Kind: provider.ErrServer}},
	}}
	wrapped := provider.Retry(p, provider.RetryConfig{
		MaxAttempts:  10,
		InitialDelay: 100 * time.Millisecond,
		Multiplier:   1,
		Jitter:       0,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := wrapped.Generate(ctx, provider.Request{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
}

func TestRetry_OnRetryCallbackFires(t *testing.T) {
	t.Parallel()
	p := &scriptedRetryProv{responses: []scriptedResponse{
		{err: &provider.APIError{Kind: provider.ErrServer}},
		{err: &provider.APIError{Kind: provider.ErrServer}},
		{resp: &provider.Response{Message: schema.AssistantMessage("ok")}},
	}}
	var seen atomic.Int32
	wrapped := provider.Retry(p, provider.RetryConfig{
		MaxAttempts:  3,
		InitialDelay: time.Millisecond,
		Multiplier:   1,
		Jitter:       0,
		OnRetry: func(attempt int, delay time.Duration, err error) {
			seen.Add(1)
			if attempt < 2 || attempt > 3 {
				t.Errorf("unexpected attempt number: %d", attempt)
			}
			if err == nil {
				t.Error("OnRetry should receive a non-nil error")
			}
		},
	})
	_, _ = wrapped.Generate(context.Background(), provider.Request{})
	if got := seen.Load(); got != 2 {
		t.Errorf("OnRetry fired %d times, want 2", got)
	}
}

func TestIsRetryable(t *testing.T) {
	t.Parallel()
	cases := map[error]bool{
		nil:                                                 false,
		provider.ErrRateLimited:                             true,
		provider.ErrServer:                                  true,
		provider.ErrAuth:                                    false,
		provider.ErrInvalidRequest:                          false,
		provider.ErrUnsupported:                             false,
		provider.ErrContextWindow:                           false,
		&provider.APIError{Kind: provider.ErrRateLimited}:   true,
		&provider.APIError{Kind: provider.ErrServer}:        true,
		&provider.APIError{Kind: provider.ErrAuth}:          false,
		errors.New("totally unrelated"):                     false,
	}
	for err, want := range cases {
		if got := provider.IsRetryable(err); got != want {
			t.Errorf("IsRetryable(%v) = %v, want %v", err, got, want)
		}
	}
}

func TestRetryConfig_DefaultsAreSane(t *testing.T) {
	t.Parallel()
	// Indirect test: build a Retry with zero config, ensure it
	// doesn't panic and uses something reasonable. A zero-value
	// MaxAttempts means "use the default", which is at least 1
	// so a single successful call returns straight through.
	p := &scriptedRetryProv{responses: []scriptedResponse{
		{resp: &provider.Response{Message: schema.AssistantMessage("ok")}},
	}}
	wrapped := provider.Retry(p, provider.RetryConfig{})
	_, err := wrapped.Generate(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("zero-value config should be usable: %v", err)
	}
}

func TestRetry_PanicOnNilProvider(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil inner Provider")
		}
	}()
	provider.Retry(nil, provider.RetryConfig{})
}
