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

// maxMessageBytes caps the size of a single inbound JSON-RPC frame
// across every transport. It bounds memory a peer can force us to
// allocate per message (a crude but effective DoS guard). 4 MiB is far
// larger than any legitimate tool call yet small enough to stay cheap.
const maxMessageBytes = 4 << 20

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
		r:   bufio.NewReader(r),
		raw: r,
		w:   w,
	}
}

type stdioTransport struct {
	mu     sync.Mutex
	r      *bufio.Reader
	raw    io.Reader // underlying reader, closed in Close if it's an io.Closer
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
	// Loop (not recurse) over blank lines: a peer streaming newlines must
	// not grow the goroutine stack one frame per blank line — that path
	// overflowed the stack (an unrecoverable abort) under a hostile or
	// chatty peer.
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, err := t.readFrame()
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
			// Empty line — skip it. Most MCP servers don't emit blank
			// lines but a few do for human readability.
			continue
		}
		return line, nil
	}
}

// readFrame reads a single newline-delimited frame from the buffered
// reader, refusing to buffer more than maxMessageBytes so a peer
// (compromised or buggy) can't drive unbounded memory growth with a
// frame that never terminates. The returned bytes still include the
// trailing delimiter when present; the caller trims it.
func (t *stdioTransport) readFrame() ([]byte, error) {
	var buf []byte
	for {
		chunk, err := t.r.ReadSlice('\n')
		if len(buf)+len(chunk) > maxMessageBytes {
			return nil, fmt.Errorf("mcp: frame exceeds %d bytes", maxMessageBytes)
		}
		// ReadSlice returns a slice into the reader's buffer; copy it
		// out before the next read reuses that memory.
		buf = append(buf, chunk...)
		if err == nil {
			return buf, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			// Delimiter not yet seen; keep accumulating (bounded above).
			continue
		}
		return buf, err
	}
}

// Close marks the transport as closed and best-effort closes both the
// underlying writer and reader if they implement io.Closer. Closing
// the reader is what unblocks a dispatch loop parked in ReadBytes on a
// blocking source (e.g. NewStdioTransport(os.Stdin, os.Stdout)); a
// writer-only close would leak that goroutine.
func (t *stdioTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	var rerr error
	if c, ok := t.raw.(io.Closer); ok {
		rerr = c.Close()
	}
	if c, ok := t.w.(io.Closer); ok {
		if err := c.Close(); err != nil {
			return err
		}
	}
	return rerr
}
