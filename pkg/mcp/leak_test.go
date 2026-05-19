package mcp_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain catches goroutine leaks in the MCP client (dispatch
// loop) and server (per-request worker goroutines). Either side
// must clean up when the transport closes or the parent ctx
// cancels; this gate enforces that.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
