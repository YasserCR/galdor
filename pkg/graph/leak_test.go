package graph_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs every test in this package, then verifies that no
// goroutines leaked. graph.Runnable.Stream spawns a goroutine per
// call; this gate catches the case where a test consumer abandons
// the channel without cancelling the context.
//
// Failures here show the leaked goroutine's stack — usually that's
// enough to identify which test left it dangling.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
