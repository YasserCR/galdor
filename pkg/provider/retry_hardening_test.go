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
