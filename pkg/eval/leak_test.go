package eval_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain catches goroutine leaks in the parallel runner's worker
// pool. Workers exit when the queue channel closes; this verifies
// the close path is reached on every test exit.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
