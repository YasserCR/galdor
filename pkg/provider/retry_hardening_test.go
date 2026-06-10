package provider

import (
	"testing"
	"time"
)

// Regression for audit M6: a server-mandated Retry-After that fits within
// MaxDelay must be honored as a floor — never jittered BELOW the server
// value (retrying early just earns another 429) and never above MaxDelay.
func TestRetry_RetryAfterHonoredAsFloorWithinMaxDelay(t *testing.T) {
	rp := &retryProvider{cfg: RetryConfig{Jitter: 0.5, MaxDelay: 30 * time.Second}.withDefaults()}
	raErr := &APIError{RetryAfter: 5} // server says wait 5s; well under MaxDelay 30s
	for i := 0; i < 200; i++ {
		d, ok := rp.nextDelay(1, raErr)
		if !ok {
			t.Fatalf("Retry-After (5s) is within MaxDelay (30s); must retry, got give-up")
		}
		if d < 5*time.Second {
			t.Fatalf("Retry-After (5s) must not be jittered below the server value (regression of M6): got %v", d)
		}
		if d > 30*time.Second {
			t.Fatalf("delay %v exceeds MaxDelay 30s", d)
		}
	}
}

// Regression: a negative Multiplier used to flip the delay's sign each step,
// producing a negative Duration that sleeps 0 — a hot retry loop. withDefaults
// must clamp Multiplier to the fixed-interval floor (1.0) so every delay is
// the non-negative InitialDelay.
func TestRetry_NegativeMultiplierDoesNotProduceNegativeDelay(t *testing.T) {
	rp := &retryProvider{cfg: RetryConfig{
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     time.Second,
		Multiplier:   -2, // hostile/buggy
		Jitter:       -1, // deterministic
	}.withDefaults()}
	for attempt := 1; attempt <= 6; attempt++ {
		d, ok := rp.nextDelay(attempt, &APIError{Kind: ErrServer})
		if !ok || d < 0 {
			t.Fatalf("attempt %d: delay = %v (ok=%v); want non-negative (Multiplier must be clamped to >= 1)", attempt, d, ok)
		}
	}
}

// Regression: a large attempt count must not overflow the float→Duration
// conversion into a negative delay; the exponential schedule saturates at
// MaxDelay instead.
func TestRetry_HugeAttemptSaturatesAtMaxDelay(t *testing.T) {
	rp := &retryProvider{cfg: RetryConfig{
		InitialDelay: time.Second,
		MaxDelay:     30 * time.Second,
		Multiplier:   2,
		Jitter:       -1,
	}.withDefaults()}
	d, ok := rp.nextDelay(300, &APIError{Kind: ErrServer}) // 2^299 overflows float64→Duration
	if !ok || d <= 0 || d > 30*time.Second {
		t.Fatalf("delay = %v (ok=%v); want a positive delay capped at MaxDelay 30s", d, ok)
	}
}

// A Retry-After that exceeds MaxDelay can't be honored without either
// retrying early (truncate) or out-waiting the caller (block). The wrapper
// resolves this by giving up — nextDelay reports ok=false.
func TestRetry_RetryAfterAboveMaxDelayReportsGiveUp(t *testing.T) {
	rp := &retryProvider{cfg: RetryConfig{Jitter: 0.5, MaxDelay: 30 * time.Second}.withDefaults()}
	raErr := &APIError{RetryAfter: 60} // server wants 60s; MaxDelay is 30s
	if _, ok := rp.nextDelay(1, raErr); ok {
		t.Fatal("Retry-After (60s) exceeds MaxDelay (30s); nextDelay must report give-up (ok=false)")
	}
}
