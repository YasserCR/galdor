package mcp_test

import (
	"bufio"
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

// sseClient is a tiny test-only HTTP client that speaks the MCP SSE
// transport against a server bound to baseURL. It opens the GET /sse
// stream, reads the `endpoint` event, then POSTs to that endpoint and
// reads the `message` events back.
type sseClient struct {
	baseURL  string
	endpoint string
	resp     *http.Response
	reader   *bufio.Reader
	events   chan sseEvent
	done     chan struct{}
}

type sseEvent struct {
	event string
	data  string
}

func newSSEClient(t *testing.T, baseURL string) *sseClient {
	t.Helper()
	c := &sseClient{
		baseURL: baseURL,
		events:  make(chan sseEvent, 16),
		done:    make(chan struct{}),
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/sse", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /sse: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sse: status %d", resp.StatusCode)
	}
	c.resp = resp
	c.reader = bufio.NewReader(resp.Body)

	// Read the first endpoint event synchronously so callers can POST.
	ev, err := readSSEEvent(c.reader)
	if err != nil {
		t.Fatalf("read endpoint event: %v", err)
	}
	if ev.event != "endpoint" {
		t.Fatalf("first event = %q, want endpoint", ev.event)
	}
	c.endpoint = baseURL + ev.data

	go c.pump()
	return c
}

func (c *sseClient) pump() {
	defer close(c.done)
	defer c.resp.Body.Close()
	for {
		ev, err := readSSEEvent(c.reader)
		if err != nil {
			return
		}
		select {
		case c.events <- ev:
		default:
			// drop if nobody's listening
		}
	}
}

func (c *sseClient) post(t *testing.T, body []byte) {
	t.Helper()
	resp, err := http.Post(c.endpoint, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST status = %d", resp.StatusCode)
	}
}

func (c *sseClient) postRaw(body []byte) (int, error) {
	resp, err := http.Post(c.endpoint, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func (c *sseClient) nextMessage(t *testing.T, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-c.events:
			if ev.event == "message" {
				return ev.data
			}
		case <-deadline:
			t.Fatalf("timeout waiting for message event")
			return ""
		}
	}
}

func (c *sseClient) close() {
	_ = c.resp.Body.Close()
	<-c.done
}

// readSSEEvent reads one SSE event block (terminated by a blank line)
// and returns its event name + concatenated data lines.
func readSSEEvent(r *bufio.Reader) (sseEvent, error) {
	var ev sseEvent
	var dataB strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF && len(line) == 0 && ev.event == "" && dataB.Len() == 0 {
				return ev, err
			}
			if err != io.EOF {
				return ev, err
			}
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			ev.data = dataB.String()
			return ev, nil
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			ev.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if dataB.Len() > 0 {
				dataB.WriteByte('\n')
			}
			dataB.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		default:
			// ignore comments / id / retry
		}
	}
}

// startSSEServer is a helper: it constructs an SSE transport bound to
// :0, starts a Server.Serve goroutine, and returns the base URL plus
// a cleanup func.
func startSSEServer(t *testing.T) (string, func()) {
	t.Helper()
	transport := mcp.NewSSETransport("127.0.0.1:0")
	addr, ok := mcp.SSETransportAddr(transport)
	if !ok {
		t.Fatal("transport is not *sseTransport")
	}
	srv := mcp.NewServer(newTestRegistry(t), mcp.ServerInfo{Name: "sse-test", Version: "1.0"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(ctx, transport)
		close(done)
	}()

	baseURL := "http://" + addr
	// poll until listener is ready (it should already be on return,
	// but be defensive against scheduler delay)
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
	return baseURL, cleanup
}

func TestSSE_InitializeAndCallTool(t *testing.T) {
	t.Parallel()
	baseURL, stop := startSSEServer(t)
	defer stop()

	c := newSSEClient(t, baseURL)
	defer c.close()

	// initialize
	c.post(t, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	got := c.nextMessage(t, 2*time.Second)
	var msg map[string]any
	if err := json.Unmarshal([]byte(got), &msg); err != nil {
		t.Fatalf("decode init reply: %v -- %s", err, got)
	}
	if msg["id"].(float64) != 1 {
		t.Fatalf("id mismatch: %v", msg["id"])
	}

	// initialized notification
	c.post(t, []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))

	// tools/list
	c.post(t, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	got = c.nextMessage(t, 2*time.Second)
	if !strings.Contains(got, `"add"`) || !strings.Contains(got, `"echo"`) {
		t.Fatalf("tools/list reply missing tools: %s", got)
	}

	// tools/call add
	c.post(t, []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"add","arguments":{"a":2,"b":40}}}`))
	got = c.nextMessage(t, 2*time.Second)
	if !strings.Contains(got, `sum`) || !strings.Contains(got, `42`) {
		t.Fatalf("add reply = %s, want sum:42", got)
	}
}

func TestSSE_SecondClientSupersedesFirst(t *testing.T) {
	t.Parallel()
	baseURL, stop := startSSEServer(t)
	defer stop()

	c1 := newSSEClient(t, baseURL)
	defer c1.close()

	c2 := newSSEClient(t, baseURL)
	defer c2.close()

	// c2 is now the active session. A POST against c1's stale
	// endpoint must fail with 404 because c1's session id no longer
	// matches the active session.
	status, err := c1.postRaw([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusNotFound {
		t.Fatalf("stale-session POST status = %d, want 404", status)
	}

	// c2 works normally.
	c2.post(t, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"c2","version":"0"}}}`))
	got := c2.nextMessage(t, 2*time.Second)
	if !strings.Contains(got, `"id":1`) {
		t.Fatalf("c2 init reply unexpected: %s", got)
	}
}

func TestSSE_ContextCancelStopsServer(t *testing.T) {
	t.Parallel()
	transport := mcp.NewSSETransport("127.0.0.1:0")
	addr, _ := mcp.SSETransportAddr(transport)
	srv := mcp.NewServer(newTestRegistry(t), mcp.ServerInfo{Name: "t"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(ctx, transport)
		close(done)
	}()

	// Connect once so there's a live session, then cancel.
	c := newSSEClient(t, "http://"+addr)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not exit after cancel")
	}
	c.close()
}

func TestSSE_CloseUnblocksRecv(t *testing.T) {
	t.Parallel()
	transport := mcp.NewSSETransport("127.0.0.1:0")
	// Don't connect a client; Recv should block until Close.
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
			t.Fatal("Recv returned nil error after Close; want io.EOF")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Recv did not unblock after Close")
	}
}

func TestSSE_ConcurrentRequestsAllAnswered(t *testing.T) {
	t.Parallel()
	baseURL, stop := startSSEServer(t)
	defer stop()

	c := newSSEClient(t, baseURL)
	defer c.close()

	c.post(t, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	_ = c.nextMessage(t, 2*time.Second)
	c.post(t, []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))

	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi-%d"}}}`, i+10, i)
			_, err := c.postRaw([]byte(body))
			if err != nil {
				t.Errorf("POST %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	// Collect n replies.
	seen := map[float64]bool{}
	deadline := time.Now().Add(3 * time.Second)
	for len(seen) < n && time.Now().Before(deadline) {
		raw := c.nextMessage(t, 2*time.Second)
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			continue
		}
		if id, ok := m["id"].(float64); ok {
			seen[id] = true
		}
	}
	if len(seen) != n {
		t.Fatalf("got %d replies, want %d", len(seen), n)
	}
}
