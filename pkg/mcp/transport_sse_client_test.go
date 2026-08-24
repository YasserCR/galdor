package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YasserCR/galdor/pkg/mcp"
)

// TestSSEClientTransport_EndToEnd drives galdor's own SSE server with the
// SSE client and runs the surface AsRegistry depends on: initialize →
// tools/list → tools/call. Until this transport existed the only way to
// reach a remote SSE server was NewSSETransport, which binds a listener
// instead of dialing — it waits for a caller that never comes and the
// handshake times out.
func TestSSEClientTransport_EndToEnd(t *testing.T) {
	t.Parallel()
	baseURL, stop := startSSEServer(t)
	defer stop()

	tr, err := mcp.NewSSEClientTransport(baseURL + "/sse")
	if err != nil {
		t.Fatalf("new client transport: %v", err)
	}
	c := mcp.NewClient(tr, mcp.WithClientInfo(mcp.ClientInfo{Name: "sse-client-test", Version: "0.1"}))
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err = c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if c.ServerInfo().Name != "sse-test" {
		t.Errorf("server name = %q, want sse-test", c.ServerInfo().Name)
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

func TestNewSSEClientTransport_RejectsBadURL(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"", "ftp://x", "://nope", "not a url", "/relative"} {
		if _, err := mcp.NewSSEClientTransport(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

// TestSSEClientTransport_ConstructorOpensNothing pins the laziness: a
// transport that is built and never used must not dial. Otherwise every
// probe of a configured-but-disabled server would cost a connection.
func TestSSEClientTransport_ConstructorOpensNothing(t *testing.T) {
	t.Parallel()

	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
	}))
	defer srv.Close()

	tr, err := mcp.NewSSEClientTransport(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Fatalf("constructor made %d request(s), want none", hits)
	}
}

// TestSSEClientTransport_RelativeEndpoint covers the announcement most
// servers actually send: a path, not an absolute URL. It has to resolve
// against the stream URL or every POST goes nowhere.
func TestSSEClientTransport_RelativeEndpoint(t *testing.T) {
	t.Parallel()

	const reply = `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`

	posted := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: endpoint\ndata: /messages?sessionId=abc\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("/messages", func(w http.ResponseWriter, r *http.Request) {
		select {
		case posted <- r.URL.RequestURI():
		default:
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, reply)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tr, err := mcp.NewSSEClientTransport(srv.URL + "/sse")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case uri := <-posted:
		if uri != "/messages?sessionId=abc" {
			t.Errorf("POST went to %q", uri)
		}
	case <-ctx.Done():
		t.Fatal("no POST arrived")
	}
}

// TestSSEClientTransport_StreamErrorSurfaces makes a server that refuses
// the stream report as an error rather than as a Send that hangs until
// the caller's deadline. A 401 is the common case: the server wants a
// token the client was not given.
func TestSSEClientTransport_StreamErrorSurfaces(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "token required", http.StatusUnauthorized)
	}))
	defer srv.Close()

	tr, err := mcp.NewSSEClientTransport(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err == nil {
		t.Fatal("Send succeeded against a server that refused the stream")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want it to name the status", err)
	}
	if ctx.Err() != nil {
		t.Error("Send waited out the deadline instead of reporting the refusal")
	}
}

// TestSSEClientTransport_CloseUnblocksRecv matches the guarantee the
// Transport contract makes for every other transport.
func TestSSEClientTransport_CloseUnblocksRecv(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	tr, err := mcp.NewSSEClientTransport(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := tr.Recv(context.Background())
		errCh <- err
	}()

	time.Sleep(50 * time.Millisecond)
	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) {
			t.Errorf("Recv = %v, want io.EOF", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not unblock Recv")
	}
}
