package mcp_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/YasserCR/galdor/pkg/mcp"
)

const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
	`{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`

// Regression for audit M30: a browser cross-site request carries an
// Origin header; the Streamable HTTP transport must reject any non-loopback
// Origin (DNS-rebinding protection) instead of serving it.
func TestStreamableHTTP_RejectsForeignOrigin(t *testing.T) {
	base, _, cleanup := startStreamableServer(t)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodPost, base+"/", bytes.NewReader([]byte(initBody)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign Origin must be 403 (regression of M30), got %d", resp.StatusCode)
	}
}

// Regression for audit M28: once a session id has been assigned, a
// non-initialize request that omits the Mcp-Session-Id header must be
// rejected. The old check only validated a *present* id, so an absent one
// bypassed the session entirely.
func TestStreamableHTTP_RejectsMissingSessionAfterInit(t *testing.T) {
	base, _, cleanup := startStreamableServer(t)
	defer cleanup()

	c := newStreamableClient(base)
	status, sid, _ := c.do(t, []byte(initBody))
	if status != http.StatusOK || sid == "" {
		t.Fatalf("initialize: status=%d sid=%q", status, sid)
	}

	// A non-initialize POST with NO session header must be rejected.
	req, _ := http.NewRequest(http.MethodPost, base+"/",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a non-initialize request without the session id must be rejected (regression of M28), got %d", resp.StatusCode)
	}
}

// Regression for audit M31: a transport whose listener fails to bind must
// make Serve return the bind error, not exit silently with nil.
func TestStreamableHTTP_SurfacesBindError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	addr := ln.Addr().String() // already occupied

	tr := mcp.NewStreamableHTTPTransport(addr)
	defer func() { _ = tr.Close() }()
	srv := mcp.NewServer(newTestRegistry(t), mcp.ServerInfo{Name: "t", Version: "1"})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Serve(ctx, tr); err == nil {
		t.Fatal("Serve must surface the listener bind error (regression of M31), got nil")
	}
}

// Regression for audit M27: the SSE transport must require the exact
// sessionId issued in the `endpoint` event. The old check accepted an
// empty/omitted sessionId, so the 128-bit id gave no isolation — anyone
// reaching the port could inject into the active session.
func TestSSE_RejectsMissingSessionID(t *testing.T) {
	base, cleanup := startSSEServer(t)
	defer cleanup()

	c := newSSEClient(t, base) // establishes an active session
	defer c.close()

	// POST to /messages WITHOUT a sessionId must be rejected.
	resp, err := http.Post(base+"/messages", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST without a sessionId must be rejected (regression of M27), got %d", resp.StatusCode)
	}
}

// Regression for audit M30 (SSE path): a foreign Origin is rejected.
func TestSSE_RejectsForeignOrigin(t *testing.T) {
	base, cleanup := startSSEServer(t)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodGet, base+"/sse", nil)
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign Origin on /sse must be 403 (regression of M30), got %d", resp.StatusCode)
	}
}
