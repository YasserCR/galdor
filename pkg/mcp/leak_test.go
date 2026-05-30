package mcp_test

import (
	"io"
	"testing"
	"time"

	"github.com/YasserCR/galdor/pkg/mcp"
	"go.uber.org/goleak"
)

// TestMain catches goroutine leaks in the MCP client (dispatch
// loop) and server (per-request worker goroutines). Either side
// must clean up when the transport closes or the parent ctx
// cancels; this gate enforces that.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// blockingReadCloser blocks in Read until Close is called, then
// returns io.EOF — modelling os.Stdin, which never unblocks on its
// own but does on close.
type blockingReadCloser struct{ done chan struct{} }

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{done: make(chan struct{})}
}

func (b *blockingReadCloser) Read(_ []byte) (int, error) {
	<-b.done
	return 0, io.EOF
}

func (b *blockingReadCloser) Close() error {
	select {
	case <-b.done:
	default:
		close(b.done)
	}
	return nil
}

// TestClient_CloseUnblocksBlockingReader ensures Close on a client
// over NewStdioTransport(blockingReader, writer) tears down the
// dispatch goroutine even though the reader is parked in Read. The
// package-wide goleak gate (TestMain) fails the run if it leaks.
func TestClient_CloseUnblocksBlockingReader(t *testing.T) {
	t.Parallel()
	r := newBlockingReadCloser()
	w := io.Discard
	c := mcp.NewClient(mcp.NewStdioTransport(r, w))
	// Give the dispatch loop a moment to park in Read.
	time.Sleep(50 * time.Millisecond)
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// The dispatch goroutine must have exited by now; goleak verifies.
	time.Sleep(50 * time.Millisecond)
}
