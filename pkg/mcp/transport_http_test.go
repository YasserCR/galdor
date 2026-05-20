package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YasserCR/galdor/pkg/mcp"
)

// streamableClient is a small test-only HTTP client for the MCP
// Streamable HTTP transport. It POSTs JSON-RPC bodies and reads JSON
// replies; the Mcp-Session-Id header is round-tripped automatically.
type streamableClient struct {
	baseURL   string
	sessionID string
}

func newStreamableClient(baseURL string) *streamableClient {
	return &streamableClient{baseURL: baseURL}
}

// do POSTs body and returns (status, sessionId from response, body bytes).
func (c *streamableClient) do(t *testing.T, body []byte) (int, string, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	sid := resp.Header.Get("Mcp-Session-Id")
	if sid != "" {
		c.sessionID = sid
	}
	return resp.StatusCode, sid, data
}

func startStreamableServer(t *testing.T) (string, mcp.Transport, func()) {
	t.Helper()
	transport := mcp.NewStreamableHTTPTransport("127.0.0.1:0")
	addr, ok := mcp.StreamableHTTPTransportAddr(transport)
	if !ok {
		t.Fatal("not a streamable http transport")
	}
	srv := mcp.NewServer(newTestRegistry(t), mcp.ServerInfo{Name: "http-test", Version: "1.0"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(ctx, transport)
		close(done)
	}()

	// Wait briefly for the listener to actually accept.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cleanup := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("server did not stop within 5s")
		}
	}
	return "http://" + addr, transport, cleanup
}

func TestStreamableHTTP_InitializeAssignsSessionID(t *testing.T) {
	t.Parallel()
	baseURL, _, stop := startStreamableServer(t)
	defer stop()

	c := newStreamableClient(baseURL)
	status, sid, body := c.do(t, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	if status != http.StatusOK {
		t.Fatalf("init status = %d", status)
	}
	if sid == "" {
		t.Fatal("init response missing Mcp-Session-Id header")
	}
	var msg map[string]any
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("decode init body: %v -- %s", err, body)
	}
	if msg["id"].(float64) != 1 {
		t.Fatalf("id mismatch: %v", msg["id"])
	}
}

func TestStreamableHTTP_FullSession(t *testing.T) {
	t.Parallel()
	baseURL, _, stop := startStreamableServer(t)
	defer stop()

	c := newStreamableClient(baseURL)
	status, _, _ := c.do(t, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	if status != http.StatusOK {
		t.Fatalf("init status = %d", status)
	}
	// notification, no reply expected
	status, _, _ = c.do(t, []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if status != http.StatusAccepted {
		t.Fatalf("notification status = %d, want 202", status)
	}
	// tools/list
	status, _, body := c.do(t, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	if status != http.StatusOK || !strings.Contains(string(body), `"add"`) {
		t.Fatalf("tools/list: status=%d body=%s", status, body)
	}
	// tools/call
	status, _, body = c.do(t, []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"add","arguments":{"a":1,"b":2}}}`))
	if status != http.StatusOK {
		t.Fatalf("tools/call status = %d", status)
	}
	if !strings.Contains(string(body), `sum`) || !strings.Contains(string(body), `3`) {
		t.Fatalf("tools/call body = %s", body)
	}
}

func TestStreamableHTTP_WrongSessionIDRejected(t *testing.T) {
	t.Parallel()
	baseURL, _, stop := startStreamableServer(t)
	defer stop()

	c := newStreamableClient(baseURL)
	_, _, _ = c.do(t, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	if c.sessionID == "" {
		t.Fatal("expected sessionID after initialize")
	}
	// Tamper with the session id; the next request must be rejected.
	c.sessionID = "deadbeef-not-real"
	status, _, _ := c.do(t, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	if status != http.StatusNotFound {
		t.Fatalf("bad-session status = %d, want 404", status)
	}
}

func TestStreamableHTTP_DeleteSession(t *testing.T) {
	t.Parallel()
	baseURL, _, stop := startStreamableServer(t)
	defer stop()

	c := newStreamableClient(baseURL)
	_, _, _ = c.do(t, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	if c.sessionID == "" {
		t.Fatal("expected sessionID")
	}
	req, _ := http.NewRequest(http.MethodDelete, baseURL+"/", nil)
	req.Header.Set("Mcp-Session-Id", c.sessionID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", resp.StatusCode)
	}
}

func TestStreamableHTTP_CloseUnblocksRecv(t *testing.T) {
	t.Parallel()
	transport := mcp.NewStreamableHTTPTransport("127.0.0.1:0")
	recvDone := make(chan error, 1)
	go func() {
		_, err := transport.Recv(context.Background())
		recvDone <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := transport.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-recvDone:
		if err == nil {
			t.Fatal("Recv returned nil; want io.EOF")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Recv did not unblock after Close")
	}
}

func TestStreamableHTTP_ContextCancelStopsServer(t *testing.T) {
	t.Parallel()
	transport := mcp.NewStreamableHTTPTransport("127.0.0.1:0")
	srv := mcp.NewServer(newTestRegistry(t), mcp.ServerInfo{Name: "t"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(ctx, transport)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit after cancel")
	}
}

func TestStreamableHTTP_SequentialCalls(t *testing.T) {
	t.Parallel()
	baseURL, _, stop := startStreamableServer(t)
	defer stop()

	c := newStreamableClient(baseURL)
	_, _, _ = c.do(t, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	_, _, _ = c.do(t, []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))

	// MCP HTTP clients send requests sequentially per the spec; do that.
	const n = 5
	for i := 0; i < n; i++ {
		body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi-%d"}}}`, i+10, i)
		status, _, replyBody := c.do(t, []byte(body))
		if status != http.StatusOK {
			t.Fatalf("call %d status = %d", i, status)
		}
		var msg map[string]any
		if err := json.Unmarshal(replyBody, &msg); err != nil {
			t.Fatalf("decode reply %d: %v -- %s", i, err, replyBody)
		}
		if int(msg["id"].(float64)) != i+10 {
			t.Fatalf("reply %d id = %v", i, msg["id"])
		}
	}
}

// TestStreamableHTTP_ParallelClientsViaSerialization confirms that
// many independent test clients with their own session ids can still
// be served — the transport surfaces one session at a time, but the
// HTTP server can in principle accept overlapping POSTs; sequential
// per-session usage is the spec.
func TestStreamableHTTP_ParallelClientsViaSerialization(t *testing.T) {
	t.Parallel()
	baseURL, _, stop := startStreamableServer(t)
	defer stop()

	const n = 4
	var wg sync.WaitGroup
	errs := make(chan error, n)
	// Serialize the clients with a mutex — Streamable HTTP transports
	// in galdor surface one in-flight call at a time per process.
	var serial sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			serial.Lock()
			defer serial.Unlock()
			c := newStreamableClient(baseURL)
			status, _, _ := c.do(t, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
			if status != http.StatusOK {
				errs <- fmt.Errorf("client %d init = %d", i, status)
				return
			}
			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"text":"c-%d"}}}`, i)
			status, _, replyBody := c.do(t, []byte(body))
			if status != http.StatusOK {
				errs <- fmt.Errorf("client %d call = %d", i, status)
				return
			}
			if !strings.Contains(string(replyBody), fmt.Sprintf("c-%d", i)) {
				errs <- fmt.Errorf("client %d body = %s", i, replyBody)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
