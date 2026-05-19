package tool_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain catches goroutine leaks from tool.ExecuteCalls, which
// fans out one goroutine per call. The wg.Wait guarantees they all
// finish before ExecuteCalls returns; this verifies that.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
