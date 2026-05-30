package provider

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"
)

// RetryConfig configures the Retry wrapper.
//
// Defaults are sensible for chat-completion workloads: 3 attempts,
// exponential backoff starting at 1s, capped at 30s, ±25% jitter.
// Override individual fields; zero values for any field other than
// MaxAttempts and Multiplier fall back to the default.
//
// Also available under the RetryPolicy alias for readers who prefer
// the "policy" framing; both names refer to the same struct.
type RetryConfig struct {
	// MaxAttempts is the total number of tries (NOT extra retries).
	// MaxAttempts=1 disables retry; MaxAttempts=3 means up to two
	// retries after the initial call. Default 3.
	MaxAttempts int

	// InitialDelay is the wait before the second attempt. Default 1s.
	InitialDelay time.Duration

	// MaxDelay caps the per-attempt wait. Default 30s.
	MaxDelay time.Duration

	// Multiplier scales the delay between attempts (exponential
	// backoff). Default 2.0; pass 1.0 for fixed-interval retry.
	Multiplier float64

	// Jitter is the fractional ±randomness applied to each delay.
	// The zero value uses the default 0.25 (each delay multiplied by a
	// random factor in [0.75, 1.25]). Set a NEGATIVE value to disable
	// jitter entirely for deterministic backoff — the zero value cannot
	// mean "off" without also breaking the sensible default for callers
	// who never set the field.
	//
	// Jitter prevents synchronized retries from a fleet of clients after
	// a shared rate-limit window, so disabling it is rarely what you want
	// outside tests.
	Jitter float64

	// OnRetry, when non-nil, is called before each retry sleep with
	// the upcoming attempt number, the planned delay, and the error
	// that triggered the retry. Useful for structured logging.
	OnRetry func(attempt int, delay time.Duration, err error)

	// Now is the clock function. Defaults to time.Now; tests inject
	// a fake to advance virtual time. Sleep is unrelated.
	Now func() time.Time
}

// withDefaults returns a copy of cfg with zero-value fields filled
// from the package defaults.
func (cfg RetryConfig) withDefaults() RetryConfig {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.InitialDelay <= 0 {
		cfg.InitialDelay = time.Second
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 30 * time.Second
	}
	if cfg.Multiplier == 0 {
		cfg.Multiplier = 2.0
	}
	if cfg.Jitter == 0 {
		cfg.Jitter = 0.25
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return cfg
}

// RetryPolicy is an alias for RetryConfig. Both names are exported
// so callers can pick whichever reads better in their code:
//
//	p := provider.Retry(inner, provider.RetryPolicy{MaxAttempts: 5})
//	p := provider.Retry(inner, provider.RetryConfig{MaxAttempts: 5})
//
// The alias is a Go type alias (not a separate type), so values are
// fully interchangeable in every position.
type RetryPolicy = RetryConfig

// WithDefaultRetry wraps inner with Retry using the package defaults
// (3 attempts, 1s→30s exponential backoff, ±25% jitter). It is
// shorthand for provider.Retry(inner, provider.RetryPolicy{}) and is
// intended to make the "I just want sensible retry on production
// 429s" case a one-liner.
func WithDefaultRetry(inner Provider) Provider {
	return Retry(inner, RetryPolicy{})
}

// Retry returns a Provider that wraps inner with automatic retries
// for transient errors. Transient kinds:
//
//   - ErrRateLimited — respects APIError.RetryAfter when set;
//     otherwise falls back to the exponential schedule.
//   - ErrServer       — 5xx-class failures.
//
// Non-transient kinds (ErrAuth, ErrInvalidRequest, ErrUnsupported,
// ErrContextWindow) and context cancellation are returned
// immediately without sleeping.
//
// Streaming requests are also retried, but only the construction
// step — once Stream returns a StreamReader, individual frame
// errors are NOT retried (the stream is stateful; the safe thing
// is to surface them to the caller).
//
// The wrapper is safe for concurrent use.
func Retry(inner Provider, cfg RetryConfig) Provider {
	if inner == nil {
		panic("provider: Retry inner cannot be nil")
	}
	return &retryProvider{inner: inner, cfg: cfg.withDefaults()}
}

type retryProvider struct {
	inner Provider
	cfg   RetryConfig
}

// Name implements Provider. Prefix added so trace consumers can
// distinguish a wrapped provider from the raw one.
func (r *retryProvider) Name() string { return r.inner.Name() }

// Capabilities passes through unchanged.
func (r *retryProvider) Capabilities() Capabilities { return r.inner.Capabilities() }

// Generate retries on transient errors. Returns the last response /
// error pair seen when attempts are exhausted.
func (r *retryProvider) Generate(ctx context.Context, req Request) (*Response, error) {
	var lastErr error
	for attempt := 1; attempt <= r.cfg.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resp, err := r.inner.Generate(ctx, req)
		if err == nil {
			return resp, nil
		}
		if !IsRetryable(err) {
			return resp, err
		}
		lastErr = err
		if attempt >= r.cfg.MaxAttempts {
			break
		}
		delay := r.nextDelay(attempt, err)
		if r.cfg.OnRetry != nil {
			r.cfg.OnRetry(attempt+1, delay, err)
		}
		if err := sleepCtx(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("provider: exhausted %d attempts: %w", r.cfg.MaxAttempts, lastErr)
}

// Stream retries on transient errors only at construction time.
// Errors observed while consuming the returned StreamReader are
// surfaced to the caller verbatim.
func (r *retryProvider) Stream(ctx context.Context, req Request) (StreamReader, error) {
	var lastErr error
	for attempt := 1; attempt <= r.cfg.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sr, err := r.inner.Stream(ctx, req)
		if err == nil {
			return sr, nil
		}
		if !IsRetryable(err) {
			return sr, err
		}
		lastErr = err
		if attempt >= r.cfg.MaxAttempts {
			break
		}
		delay := r.nextDelay(attempt, err)
		if r.cfg.OnRetry != nil {
			r.cfg.OnRetry(attempt+1, delay, err)
		}
		if err := sleepCtx(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("provider: exhausted %d attempts: %w", r.cfg.MaxAttempts, lastErr)
}

// nextDelay computes the delay for the next attempt. ErrRateLimited
// with a positive APIError.RetryAfter takes precedence over the
// exponential schedule — servers know better than our heuristic.
// Jitter is applied to either source, and MaxDelay is enforced last as
// a hard ceiling so neither exponential growth, a hostile/buggy
// Retry-After, nor jitter can exceed the caller's configured cap.
func (r *retryProvider) nextDelay(attempt int, err error) time.Duration {
	var d time.Duration
	// Server-suggested backoff wins over the exponential schedule.
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		d = time.Duration(apiErr.RetryAfter) * time.Second
	} else {
		f := float64(r.cfg.InitialDelay)
		for i := 1; i < attempt; i++ {
			f *= r.cfg.Multiplier
		}
		d = time.Duration(f)
	}
	d = applyJitter(d, r.cfg.Jitter)
	if d > r.cfg.MaxDelay {
		d = r.cfg.MaxDelay
	}
	return d
}

// IsRetryable reports whether err is a transient failure worth
// retrying. Network-level errors (context.DeadlineExceeded on a
// per-attempt timeout) are NOT considered retryable here — the
// transport layer below us should already handle TCP retries.
//
// Exported so callers writing custom retry middleware can reuse
// galdor's classification.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrRateLimited) {
		return true
	}
	if errors.Is(err, ErrServer) {
		return true
	}
	// Explicit non-retryable: bad inputs, auth, capability gaps,
	// context window — none of these get better by sleeping.
	if errors.Is(err, ErrAuth) ||
		errors.Is(err, ErrInvalidRequest) ||
		errors.Is(err, ErrUnsupported) ||
		errors.Is(err, ErrContextWindow) {
		return false
	}
	return false
}

// applyJitter multiplies d by a random factor in [1-j, 1+j].
// Returns d unchanged when j <= 0.
func applyJitter(d time.Duration, j float64) time.Duration {
	if j <= 0 || d <= 0 {
		return d
	}
	if j > 1 {
		j = 1
	}
	factor := 1 + (rand.Float64()*2-1)*j // #nosec G404 -- jitter for thundering-herd avoidance; not a security primitive
	return time.Duration(float64(d) * factor)
}

// sleepCtx sleeps for d or until ctx is cancelled, whichever comes
// first. Returns ctx.Err() if cancelled, nil if the sleep completed.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Compile-time interface assertion.
var _ Provider = (*retryProvider)(nil)
