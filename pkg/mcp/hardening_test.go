package mcp

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// scriptTransport is a fully caller-controlled Transport: sends are
// captured on `sends`, and frames pushed to `recv` are delivered to the
// client's dispatch loop in order.
type scriptTransport struct {
	sends chan rpcMessage
	recv  chan []byte
	done  chan struct{}
	once  sync.Once
}

func newScriptTransport() *scriptTransport {
	return &scriptTransport{
		sends: make(chan rpcMessage, 8),
		recv:  make(chan []byte, 8),
		done:  make(chan struct{}),
	}
}

func (s *scriptTransport) Send(_ context.Context, msg any) error {
	if m, ok := msg.(rpcMessage); ok {
		s.sends <- m
	}
	return nil
}

func (s *scriptTransport) Recv(ctx context.Context) ([]byte, error) {
	select {
	case b := <-s.recv:
		return b, nil
	case <-s.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *scriptTransport) Close() error {
	s.once.Do(func() { close(s.done) })
	return nil
}

// Regression for audit H15: a server-initiated REQUEST (id + method,
// e.g. a ping) must NOT be delivered to a pending client call as if it
// were the reply. Both sides number ids from 1, so a server ping with
// id 1 arriving while the client awaits its own request 1 was routed to
// the call's channel — and since an empty Result counts as success, the
// call returned a zero-value result. Here Initialize would "succeed"
// with an empty protocol version.
func TestClient_ServerRequestNotTreatedAsReply(t *testing.T) {
	tr := newScriptTransport()
	c := NewClient(tr)
	defer func() { _ = c.Close() }()

	errCh := make(chan error, 1)
	go func() { errCh <- c.Initialize(context.Background()) }()

	// Learn the id the client used for its initialize request.
	var initReq rpcMessage
	select {
	case initReq = <-tr.sends:
	case <-time.After(2 * time.Second):
		t.Fatal("client never sent the initialize request")
	}
	id := string(initReq.ID)

	// 1) Inject a SERVER-INITIATED REQUEST carrying the same id.
	tr.recv <- []byte(`{"jsonrpc":"2.0","id":` + id + `,"method":"ping"}`)
	// 2) Then the genuine initialize reply.
	tr.recv <- []byte(`{"jsonrpc":"2.0","id":` + id +
		`,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"srv","version":"9"},"capabilities":{}}}`)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Initialize: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Initialize did not return")
	}

	if got := c.ProtocolVersion(); got != "2024-11-05" {
		t.Fatalf("server request was misrouted as the reply (regression of H15): protocolVersion=%q, serverInfo=%+v",
			got, c.ServerInfo())
	}
}

// Regression for audit M26: the stdio transport must skip blank lines
// without recursing per line. The old Recv did `return t.Recv(ctx)` on
// every empty line, so a peer streaming newlines drove recursion depth
// linear in the input until the goroutine stack overflowed (an
// unrecoverable process abort). With a loop this is O(1) stack.
func TestStdioTransport_BlankLinesDoNotOverflowStack(t *testing.T) {
	const blanks = 8_000_000
	input := strings.Repeat("\n", blanks) + `{"jsonrpc":"2.0","method":"ping"}` + "\n"
	tr := NewStdioTransport(strings.NewReader(input), io.Discard)

	frame, err := tr.Recv(context.Background())
	if err != nil {
		t.Fatalf("Recv after %d blank lines: %v", blanks, err)
	}
	if !strings.Contains(string(frame), `"method":"ping"`) {
		t.Fatalf("expected the real frame after the blanks, got %q", frame)
	}
}
