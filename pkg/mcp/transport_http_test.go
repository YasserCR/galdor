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
	"github.com/YasserCR/galdor/pkg/tool"
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

// slowRegistry returns a registry whose single tool sleeps before
// replying, so two overlapping same-session calls are genuinely
// in-flight at the same time.
func slowRegistry(t *testing.T) *tool.Registry {
	t.Helper()
	type in struct {
		Text string `json:"text"`
		Ms   int    `json:"ms"`
	}
	type out struct {
		Text string `json:"text"`
	}
	slow, err := tool.NewTool("slow", "Echo after a delay",
		func(_ context.Context, i in) (out, error) {
			time.Sleep(time.Duration(i.Ms) * time.Millisecond)
			return out{Text: i.Text}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := tool.NewRegistry(slow)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// startStreamableServerReg is startStreamableServer with a caller-
// supplied registry.
func startStreamableServerReg(t *testing.T, reg *tool.Registry) (string, func()) {
	t.Helper()
	transport := mcp.NewStreamableHTTPTransport("127.0.0.1:0")
	addr, ok := mcp.StreamableHTTPTransportAddr(transport)
	if !ok {
		t.Fatal("not a streamable http transport")
	}
	srv := mcp.NewServer(reg, mcp.ServerInfo{Name: "http-test", Version: "1.0"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(ctx, transport)
		close(done)
	}()
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
	return "http://" + addr, cleanup
}

// TestStreamableHTTP_ConcurrentSameSession fires two overlapping
// requests in a single session. With the per-id reply map each POST
// must get its own correct reply (no drop, no cross-talk). The first
// request is slower so the replies would arrive out of order under a
// single-slot design.
func TestStreamableHTTP_ConcurrentSameSession(t *testing.T) {
	t.Parallel()
	baseURL, stop := startStreamableServerReg(t, slowRegistry(t))
	defer stop()

	// Initialize to mint a session id, then share it across goroutines.
	c := newStreamableClient(baseURL)
	status, _, _ := c.do(t, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	if status != http.StatusOK {
		t.Fatalf("init status = %d", status)
	}
	_, _, _ = c.do(t, []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	sid := c.sessionID

	// post sends a tools/call within the established session and returns
	// the decoded reply id plus the echoed text.
	post := func(id int, text string, ms int) (int, string, error) {
		body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"slow","arguments":{"text":%q,"ms":%d}}}`, id, text, ms)
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/", bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Mcp-Session-Id", sid)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return 0, "", fmt.Errorf("status %d: %s", resp.StatusCode, data)
		}
		var msg struct {
			ID     int `json:"id"`
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			return 0, "", fmt.Errorf("decode %s: %w", data, err)
		}
		var txt string
		if len(msg.Result.Content) > 0 {
			txt = msg.Result.Content[0].Text
		}
		return msg.ID, txt, nil
	}

	type res struct {
		id   int
		text string
		err  error
	}
	results := make(chan res, 2)
	// Request 100 is slow; request 200 is fast. Both overlap.
	go func() { id, txt, err := post(100, "slow-100", 300); results <- res{id, txt, err} }()
	time.Sleep(20 * time.Millisecond) // ensure 100 is in flight first
	go func() { id, txt, err := post(200, "fast-200", 0); results <- res{id, txt, err} }()

	got := map[int]string{}
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r.err != nil {
				t.Fatalf("request: %v", r.err)
			}
			got[r.id] = r.text
		case <-time.After(5 * time.Second):
			t.Fatal("a concurrent request never got its reply (dropped slot)")
		}
	}
	if got[100] != `{"text":"slow-100"}` {
		t.Errorf("id 100 text = %q, want slow-100 result", got[100])
	}
	if got[200] != `{"text":"fast-200"}` {
		t.Errorf("id 200 text = %q, want fast-200 result", got[200])
	}
}

// TestStreamableHTTP_OversizeBodyRejected confirms a body larger than
// the cap is rejected rather than fully buffered.
func TestStreamableHTTP_OversizeBodyRejected(t *testing.T) {
	t.Parallel()
	baseURL, _, stop := startStreamableServer(t)
	defer stop()

	// Build a >4 MiB JSON-RPC body (oversized string argument).
	big := strings.Repeat("a", (5 << 20))
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":%q}}}`, big)
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("oversize body accepted with 200; want rejection")
	}
}

func TestStreamableHTTP_InitializeEchoesClientVersion(t *testing.T) {
	t.Parallel()
	baseURL, _, stop := startStreamableServer(t)
	defer stop()

	c := newStreamableClient(baseURL)
	// Request a non-default protocol version; the server should echo it.
	status, _, body := c.do(t, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2099-01-01","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	if status != http.StatusOK {
		t.Fatalf("init status = %d", status)
	}
	var msg struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("decode: %v -- %s", err, body)
	}
	if msg.Result.ProtocolVersion != "2099-01-01" {
		t.Errorf("echoed version = %q, want 2099-01-01", msg.Result.ProtocolVersion)
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
