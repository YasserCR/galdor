package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YasserCR/galdor/pkg/mcp"
)

// TestHTTPClientTransport_EndToEnd dials a real Streamable HTTP server
// with the client transport and runs the full request/response surface
// the debugging CLI and AsRegistry rely on: initialize → tools/list →
// tools/call. This is the symmetric counterpart of the server transport
// (NewStreamableHTTPClientTransport ↔ NewStreamableHTTPTransport).
func TestHTTPClientTransport_EndToEnd(t *testing.T) {
	t.Parallel()
	baseURL, stop := startStreamableServerReg(t, newTestRegistry(t))
	defer stop()

	tr, err := mcp.NewStreamableHTTPClientTransport(baseURL)
	if err != nil {
		t.Fatalf("new client transport: %v", err)
	}
	c := mcp.NewClient(tr, mcp.WithClientInfo(mcp.ClientInfo{Name: "http-client-test", Version: "0.1"}))
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err = c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if c.ServerInfo().Name != "http-test" {
		t.Errorf("server name = %q, want http-test", c.ServerInfo().Name)
	}

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := map[string]bool{}
	for _, td := range tools {
		names[td.Name] = true
	}
	if !names["add"] || !names["echo"] {
		t.Fatalf("expected add+echo tools, got %v", names)
	}

	out, err := c.CallTool(ctx, "add", json.RawMessage(`{"a":2,"b":3}`))
	if err != nil {
		t.Fatalf("call add: %v", err)
	}
	if out != `{"sum":5}` {
		t.Errorf("add result = %q, want {\"sum\":5}", out)
	}
}

// TestHTTPClientTransport_SessionEcho verifies the session id minted on
// initialize is echoed on subsequent requests: the galdor server rejects
// a non-initialize request that omits the assigned session id with 404,
// so a successful tools/call after initialize proves the echo works.
func TestHTTPClientTransport_SessionEcho(t *testing.T) {
	t.Parallel()
	baseURL, stop := startStreamableServerReg(t, newTestRegistry(t))
	defer stop()

	tr, err := mcp.NewStreamableHTTPClientTransport(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	c := mcp.NewClient(tr)
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	// Without the session echo this call would get HTTP 404 and fail.
	if _, err := c.ListTools(ctx); err != nil {
		t.Fatalf("list after init (session echo broken?): %v", err)
	}
}

func TestNewStreamableHTTPClientTransport_RejectsBadURL(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"", "ftp://x", "://nope", "not a url", "/relative"} {
		if _, err := mcp.NewStreamableHTTPClientTransport(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

// TestHTTPClientTransport_SSEFramedReply dials a server that answers the
// POST with an event stream whose first line is `event:`, which is what
// two of the public Streamable HTTP servers do. Before the framing was
// recognised past the first line the reply never reached the dispatch
// loop and the caller waited out its whole deadline.
func TestHTTPClientTransport_SSEFramedReply(t *testing.T) {
	t.Parallel()

	const reply = `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Mcp-Session-Id", "s-1")
		_, _ = io.WriteString(w, "event: message\r\ndata: "+reply+"\r\n\r\n")
	}))
	defer srv.Close()

	tr, err := mcp.NewStreamableHTTPClientTransport(srv.URL)
	if err != nil {
		t.Fatalf("NewStreamableHTTPClientTransport: %v", err)
	}
	defer func() { _ = tr.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, err := tr.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if string(got) != reply {
		t.Fatalf("Recv = %q, want %q", got, reply)
	}
}
