package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/YasserCR/galdor/pkg/mcp"
	"github.com/YasserCR/galdor/pkg/tool"
)

// pairedTransports wires two MCP transports together via two
// io.Pipe pairs, simulating a stdio child-process connection without
// spawning anything. Returns (clientSide, serverSide).
func pairedTransports() (mcp.Transport, mcp.Transport, func()) {
	// Client reads serverWriter, writes clientWriter (which serverReader reads).
	cr, cw := io.Pipe()
	sr, sw := io.Pipe()
	clientT := mcp.NewStdioTransport(sr, cw) // client reads from server-out, writes to client-out
	serverT := mcp.NewStdioTransport(cr, sw) // server reads from client-out, writes to server-out
	closer := func() {
		_ = clientT.Close()
		_ = serverT.Close()
		_ = cr.Close()
		_ = cw.Close()
		_ = sr.Close()
		_ = sw.Close()
	}
	return clientT, serverT, closer
}

func newTestRegistry(t *testing.T) *tool.Registry {
	t.Helper()
	type addIn struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	type addOut struct {
		Sum int `json:"sum"`
	}
	add, err := tool.NewTool("add", "Add two ints",
		func(_ context.Context, in addIn) (addOut, error) {
			return addOut{Sum: in.A + in.B}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	type echoIn struct {
		Text string `json:"text"`
	}
	type echoOut struct {
		Text string `json:"text"`
	}
	echo, err := tool.NewTool("echo", "Echo the input",
		func(_ context.Context, in echoIn) (echoOut, error) {
			return echoOut(in), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := tool.NewRegistry(add, echo)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// runServer starts a server in a goroutine and returns a function to
// wait for it to exit.
func runServer(t *testing.T, srv *mcp.Server, transport mcp.Transport) (cancelFn func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(ctx, transport)
		close(done)
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("server did not stop within 1s")
		}
	}
}

func TestClientServer_InitializeAndListTools(t *testing.T) {
	t.Parallel()
	clientT, serverT, cleanup := pairedTransports()
	defer cleanup()

	srv := mcp.NewServer(newTestRegistry(t), mcp.ServerInfo{Name: "test", Version: "1.0"})
	stop := runServer(t, srv, serverT)
	defer stop()

	c := mcp.NewClient(clientT, mcp.WithClientInfo(mcp.ClientInfo{Name: "test-client", Version: "0.1"}))
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	info := c.ServerInfo()
	if info.Name != "test" || info.Version != "1.0" {
		t.Errorf("ServerInfo = %+v", info)
	}

	defs, err := c.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 2 {
		t.Fatalf("tools = %d, want 2 (add, echo)", len(defs))
	}
	names := map[string]bool{defs[0].Name: true, defs[1].Name: true}
	if !names["add"] || !names["echo"] {
		t.Errorf("missing tool: %+v", defs)
	}
}

func TestClientServer_CallTool(t *testing.T) {
	t.Parallel()
	clientT, serverT, cleanup := pairedTransports()
	defer cleanup()
	srv := mcp.NewServer(newTestRegistry(t), mcp.ServerInfo{Name: "test"})
	stop := runServer(t, srv, serverT)
	defer stop()

	c := mcp.NewClient(clientT)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	out, err := c.CallTool(ctx, "add", json.RawMessage(`{"a":2,"b":3}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !strings.Contains(out, `"sum":5`) {
		t.Errorf("result = %q, want JSON containing sum:5", out)
	}
}

func TestClientServer_UnknownToolReturnsRPCError(t *testing.T) {
	t.Parallel()
	clientT, serverT, cleanup := pairedTransports()
	defer cleanup()
	srv := mcp.NewServer(newTestRegistry(t), mcp.ServerInfo{Name: "test"})
	stop := runServer(t, srv, serverT)
	defer stop()

	c := mcp.NewClient(clientT)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.Initialize(ctx)

	_, err := c.CallTool(ctx, "ghost", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestClientServer_AsRegistryRoundtrip(t *testing.T) {
	t.Parallel()
	clientT, serverT, cleanup := pairedTransports()
	defer cleanup()
	srv := mcp.NewServer(newTestRegistry(t), mcp.ServerInfo{Name: "test"})
	stop := runServer(t, srv, serverT)
	defer stop()

	c := mcp.NewClient(clientT)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	reg, err := c.AsRegistry(ctx)
	if err != nil {
		t.Fatal(err)
	}
	addTool, ok := reg.Get("add")
	if !ok {
		t.Fatal("add not in registry")
	}
	if addTool.Description() != "Add two ints" {
		t.Errorf("description = %q", addTool.Description())
	}
	out, err := addTool.ExecuteJSON(ctx, json.RawMessage(`{"a":10,"b":32}`))
	if err != nil {
		t.Fatal(err)
	}
	// Adapter wraps the underlying tool result in {"text": "..."}.
	var wrap struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(out, &wrap); err != nil {
		t.Fatalf("wrap decode: %v -- raw %s", err, out)
	}
	if !strings.Contains(wrap.Text, `"sum":42`) {
		t.Errorf("nested text = %q, want sum:42", wrap.Text)
	}
}

func TestClientServer_StrictRejectsBeforeInitialize(t *testing.T) {
	t.Parallel()
	clientT, serverT, cleanup := pairedTransports()
	defer cleanup()
	srv := mcp.NewServer(newTestRegistry(t), mcp.ServerInfo{Name: "test"})
	srv.Strict = true
	stop := runServer(t, srv, serverT)
	defer stop()

	c := mcp.NewClient(clientT)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Skip Initialize on purpose; ListTools must be rejected.
	_, err := c.ListTools(ctx)
	if err == nil {
		t.Fatal("expected JSON-RPC error for pre-initialize tools/list")
	}
}

func TestClientServer_ServerNotificationsIgnored(t *testing.T) {
	t.Parallel()
	// A server that emits a stray notification before any client
	// request must not crash or hang the client dispatcher.
	clientT, serverT, cleanup := pairedTransports()
	defer cleanup()

	// Push a bogus notification onto the wire from the "server" side
	// before any client request — the dispatcher should swallow it.
	go func() {
		_ = serverT.Send(context.Background(), map[string]any{
			"jsonrpc": "2.0",
			"method":  "notifications/random_event",
			"params":  map[string]any{"x": 1},
		})
	}()
	// Spin up a real server too so Initialize can complete.
	srv := mcp.NewServer(newTestRegistry(t), mcp.ServerInfo{Name: "test"})
	stop := runServer(t, srv, serverT)
	defer stop()

	c := mcp.NewClient(clientT)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}
