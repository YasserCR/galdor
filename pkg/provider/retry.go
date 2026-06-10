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
	// A Multiplier below 1 is nonsensical for backoff: negatives make the
	// delay flip sign each step (a sign-flipped Duration sleeps 0 → hot
	// retry loop) and a fraction shrinks the delay toward zero. Clamp to
	// the fixed-interval floor (1.0); callers wanting fixed backoff pass 1.0.
	if cfg.Multiplier < 1 {
		cfg.Multiplier = 1
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

// Name implements Provider. The wrapper is transparent: it returns the
// inner provider's name verbatim so trace consumers see the underlying
// provider, not the retry decorator.
func (r *retryProvider) Name() string { return r.inner.Name() }

// Capabilities passes through unchanged.
func (r *retryProvider) Capabilities() Capabilities { return r.inner.Capabilities() }

// Generate retries on transient errors. A non-retryable error is returned
// immediately with whatever response the inner provider produced. When all
// attempts are exhausted it returns a nil response and an error wrapping the
// last transient failure (errors.Is/As against the underlying error still
// work). It also gives up early — before exhausting attempts — when a
// server's Retry-After exceeds MaxDelay (see nextDelay).
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
		delay, ok := r.nextDelay(attempt, err)
		if !ok {
			break
		}
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
		delay, ok := r.nextDelay(attempt, err)
		if !ok {
			break
		}
		if r.cfg.OnRetry != nil {
			r.cfg.OnRetry(attempt+1, delay, err)
		}
		if err := sleepCtx(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("provider: exhausted %d attempts: %w", r.cfg.MaxAttempts, lastErr)
}

// nextDelay computes the delay for the next attempt, and reports whether
// to retry at all. A server's Retry-After takes precedence over the
// exponential schedule: it is honored within MaxDelay (jittered upward
// only, so a retry never lands before the server's window), and if it
// exceeds MaxDelay the caller gives up (ok=false) rather than retry early
// or block for the full hint. The exponential path jitters both ways and
// is capped at MaxDelay.
func (r *retryProvider) nextDelay(attempt int, err error) (time.Duration, bool) {
	// Server-mandated backoff is the source of truth for WHEN to retry.
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		secs := apiErr.RetryAfter
		if secs > maxRetryAfterSeconds {
			secs = maxRetryAfterSeconds // guard against overflow / hostile values
		}
		d := time.Duration(secs) * time.Second
		// If the server wants a longer wait than the caller tolerates
		// (MaxDelay), give up: truncating to MaxDelay would retry before
		// the server's window (another guaranteed 429), and blocking for
		// the full hint exceeds the caller's patience.
		if d > r.cfg.MaxDelay {
			return 0, false
		}
		// Honor it as a FLOOR — jitter only upward (never retry early),
		// bounded by MaxDelay so it stays within the caller's tolerance.
		jittered := applyJitterUp(d, r.cfg.Jitter)
		if jittered > r.cfg.MaxDelay {
			jittered = r.cfg.MaxDelay
		}
		return jittered, true
	}
	// Exponential schedule: jitter both ways, cap at MaxDelay. Saturate at
	// MaxDelay during the climb so a large attempt count can't overflow the
	// float→Duration conversion into a negative value (which would sleep 0
	// and spin). Multiplier is clamped >= 1 in withDefaults, so f only grows.
	maxF := float64(r.cfg.MaxDelay)
	f := float64(r.cfg.InitialDelay)
	for i := 1; i < attempt; i++ {
		f *= r.cfg.Multiplier
		if f >= maxF {
			f = maxF
			break
		}
	}
	d := applyJitter(time.Duration(f), r.cfg.Jitter)
	if d < 0 || d > r.cfg.MaxDelay {
		d = r.cfg.MaxDelay
	}
	return d, true
}

// maxRetryAfterSeconds caps a server's Retry-After so a hostile or buggy
// value can't overflow the time.Duration multiplication. 24h is far beyond
// any legitimate hint (and far beyond any sane MaxDelay).
const maxRetryAfterSeconds = 24 * 60 * 60

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

// applyJitterUp adds jitter in [0, j] only — the result is never below d.
// Used for server-mandated Retry-After, which must not be undershot.
func applyJitterUp(d time.Duration, j float64) time.Duration {
	if j <= 0 || d <= 0 {
		return d
	}
	if j > 1 {
		j = 1
	}
	// factor in [1, 1+j] — never below d.
	factor := 1 + rand.Float64()*j // #nosec G404 -- jitter for thundering-herd avoidance; not a security primitive
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
