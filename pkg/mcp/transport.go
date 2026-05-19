package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Transport is the wire-level abstraction MCP messages flow through.
// Implementations frame messages however the underlying medium
// requires; callers exchange decoded JSON-RPC messages with the peer
// via Send and Recv.
//
// All methods must be safe to call from different goroutines.
type Transport interface {
	// Send serializes a message and writes one frame to the peer.
	// Returns an error if the medium is closed or the encoding fails.
	Send(ctx context.Context, msg any) error

	// Recv blocks until the next frame is available, decodes it
	// into raw JSON, and returns the bytes. Callers unmarshal into
	// the appropriate message type.
	//
	// Returns io.EOF when the peer closes the medium cleanly.
	Recv(ctx context.Context) ([]byte, error)

	// Close releases medium-owned resources. Subsequent Send / Recv
	// calls return an error. Safe to call multiple times.
	Close() error
}

// NewStdioTransport returns a Transport that reads newline-delimited
// JSON from r and writes newline-delimited JSON to w. This is the
// transport Claude Desktop and most MCP servers use when launched as
// child processes.
//
// r and w may be the same underlying connection (an io.ReadWriter)
// when used over a duplex pipe or socket; the Transport synchronizes
// writes with an internal mutex so concurrent Send calls are safe.
func NewStdioTransport(r io.Reader, w io.Writer) Transport {
	return &stdioTransport{
		r: bufio.NewReader(r),
		w: w,
	}
}

type stdioTransport struct {
	mu     sync.Mutex
	r      *bufio.Reader
	w      io.Writer
	closed bool
}

// Send serializes msg as JSON, appends a newline, and writes it
// atomically (under a mutex so interleaved frames are impossible).
func (t *stdioTransport) Send(ctx context.Context, msg any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	buf, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mcp: encode frame: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errors.New("mcp: transport closed")
	}
	if _, err := t.w.Write(append(buf, '\n')); err != nil {
		return fmt.Errorf("mcp: write frame: %w", err)
	}
	return nil
}

// Recv reads one newline-delimited frame. The context is honored
// best-effort: if r is a *bufio.Reader wrapping a blocking source
// (os.Stdin), the read won't unblock on ctx cancellation. Callers
// that need hard deadlines should wrap r in something that does.
func (t *stdioTransport) Recv(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	line, err := t.r.ReadBytes('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && len(line) == 0 {
			return nil, io.EOF
		}
		if errors.Is(err, io.EOF) {
			// Last line had no trailing newline; treat it as a valid frame.
			return line, nil
		}
		return nil, fmt.Errorf("mcp: read frame: %w", err)
	}
	// Trim trailing newline (and \r if running against Windows pipes).
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	if len(line) == 0 {
		// Empty line — recurse to skip it. Most MCP servers don't
		// emit blank lines but a few do for human readability.
		return t.Recv(ctx)
	}
	return line, nil
}

// Close marks the transport as closed and best-effort closes the
// underlying writer if it implements io.Closer.
func (t *stdioTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	if c, ok := t.w.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
