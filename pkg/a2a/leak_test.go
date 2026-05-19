package a2a_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain catches goroutine leaks from a2a.Server's per-task
// handler goroutines and from the httptest servers spun up by
// each test. The handler doesn't spawn goroutines itself but
// httptest does; this gate keeps the surface honest.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
