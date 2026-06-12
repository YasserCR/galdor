package mcp_test

import (
	"context"
	"encoding/json"
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
