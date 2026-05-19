package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/YasserCR/galdor/internal/jsonschema"
	"github.com/YasserCR/galdor/pkg/tool"
)

// Client speaks MCP to a single remote server. The zero value is not
// usable; call NewClient.
//
// A Client multiplexes outstanding requests over the transport: many
// Initialize / List / Call calls can be in flight concurrently. Each
// request gets a unique numeric id; the dispatcher routes the
// matching reply back to the awaiting goroutine.
type Client struct {
	t        Transport
	clientID atomic.Int64
	pending  sync.Map // map[int64]chan rpcMessage

	closeOnce sync.Once
	closed    chan struct{}
	recvErr   atomic.Value // error

	info      ClientInfo
	serverCap Capabilities
	serverInf ServerInfo
}

// ClientOption configures NewClient.
type ClientOption func(*Client)

// WithClientInfo overrides the default name/version reported during
// the initialize handshake.
func WithClientInfo(info ClientInfo) ClientOption {
	return func(c *Client) { c.info = info }
}

// NewClient constructs a Client over t. It does NOT block on the
// transport; call Initialize to perform the handshake.
//
// The Client starts a goroutine that pumps incoming frames into the
// pending-request map. The goroutine exits when Close is called or
// when Recv returns io.EOF.
func NewClient(t Transport, opts ...ClientOption) *Client {
	c := &Client{
		t:      t,
		closed: make(chan struct{}),
		info:   ClientInfo{Name: "galdor", Version: "0"},
	}
	for _, opt := range opts {
		opt(c)
	}
	go c.dispatchLoop()
	return c
}

// Close shuts down the dispatcher and closes the underlying
// transport. Pending requests are awakened with a context-canceled-
// style error.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		err = c.t.Close()
		close(c.closed)
	})
	return err
}

// Initialize performs the MCP handshake: sends an `initialize`
// request, awaits the response, and sends the `notifications/initialized`
// follow-up the spec requires.
func (c *Client) Initialize(ctx context.Context) error {
	params := initializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    Capabilities{Tools: &ToolsCapability{}},
		ClientInfo:      c.info,
	}
	var out initializeResult
	if err := c.call(ctx, MethodInitialize, params, &out); err != nil {
		return err
	}
	c.serverCap = out.Capabilities
	c.serverInf = out.ServerInfo
	// Spec: clients MUST send `notifications/initialized` after a
	// successful initialize.
	return c.notify(ctx, MethodInitialized, struct{}{})
}

// ServerInfo returns the name/version the server reported during
// initialize. Empty before Initialize completes.
func (c *Client) ServerInfo() ServerInfo { return c.serverInf }

// ListTools fetches the tool catalog from the server.
func (c *Client) ListTools(ctx context.Context) ([]ToolDef, error) {
	var out toolsListResult
	if err := c.call(ctx, MethodToolsList, struct{}{}, &out); err != nil {
		return nil, err
	}
	return out.Tools, nil
}

// CallTool invokes a remote tool with the given JSON arguments and
// returns the concatenated text content of the reply.
//
// When the server marks the result as `isError`, the text content is
// returned as the error message so callers see what went wrong.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	params := toolsCallParams{Name: name, Arguments: args}
	var out toolsCallResult
	if err := c.call(ctx, MethodToolsCall, params, &out); err != nil {
		return "", err
	}
	text := concatText(out.Content)
	if out.IsError {
		if text == "" {
			text = "(server returned isError with no text content)"
		}
		return text, fmt.Errorf("mcp: tool %q returned error: %s", name, text)
	}
	return text, nil
}

// AsRegistry converts every tool advertised by the server into a
// galdor tool.AnyTool and returns a Registry holding them. The
// caller can pass that Registry straight to pkg/agent.Config.Tools.
//
// Each adapter tool's ExecuteJSON proxies to CallTool, so invocations
// flow over the transport transparently.
func (c *Client) AsRegistry(ctx context.Context) (*tool.Registry, error) {
	defs, err := c.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	tools := make([]tool.AnyTool, 0, len(defs))
	for _, d := range defs {
		schema, err := jsonschema.FromRaw(d.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("mcp: parse schema for %q: %w", d.Name, err)
		}
		tools = append(tools, &mcpTool{
			client:      c,
			name:        d.Name,
			description: d.Description,
			schema:      schema,
		})
	}
	return tool.NewRegistry(tools...)
}

// mcpTool is the tool.AnyTool adapter wrapping a remote MCP tool.
// Implements only the type-erased interface; users that need
// generics-typed access can wrap mcpTool in their own struct.
type mcpTool struct {
	client      *Client
	name        string
	description string
	schema      *jsonschema.Schema
}

func (t *mcpTool) Name() string               { return t.name }
func (t *mcpTool) Description() string        { return t.description }
func (t *mcpTool) Schema() *jsonschema.Schema { return t.schema }
func (t *mcpTool) ExecuteJSON(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	text, err := t.client.CallTool(ctx, t.name, input)
	if err != nil {
		// Return the error verbatim — the executor will surface it
		// as a tool-result error message to the model.
		return nil, err
	}
	// MCP tool results are free-form text; we wrap them in a JSON
	// object so downstream consumers always get valid JSON.
	out, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// concatText joins all "text" parts into a single string.
func concatText(parts []ContentPart) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		if parts[0].Type == "text" {
			return parts[0].Text
		}
		return ""
	}
	var out string
	for i, p := range parts {
		if p.Type != "text" {
			continue
		}
		if i > 0 && out != "" {
			out += "\n"
		}
		out += p.Text
	}
	return out
}

// ----------- request plumbing -----------

// call sends a request and blocks until the matching reply arrives
// or ctx is cancelled.
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	id := c.clientID.Add(1)
	idBytes, _ := json.Marshal(id)
	rawParams, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("mcp: encode params: %w", err)
	}
	req := rpcMessage{
		JSONRPC: "2.0",
		ID:      idBytes,
		Method:  method,
		Params:  rawParams,
	}
	ch := make(chan rpcMessage, 1)
	c.pending.Store(id, ch)
	defer c.pending.Delete(id)

	if err := c.t.Send(ctx, req); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		if e, ok := c.recvErr.Load().(error); ok && e != nil {
			return fmt.Errorf("mcp: connection closed: %w", e)
		}
		return errors.New("mcp: connection closed")
	case msg := <-ch:
		if msg.Error != nil {
			return msg.Error
		}
		if out == nil || len(msg.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(msg.Result, out); err != nil {
			return fmt.Errorf("mcp: decode %s result: %w", method, err)
		}
		return nil
	}
}

// notify sends a request with no id (notification per JSON-RPC) and
// returns immediately. Notifications never have replies.
func (c *Client) notify(ctx context.Context, method string, params any) error {
	rawParams, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("mcp: encode params: %w", err)
	}
	return c.t.Send(ctx, rpcMessage{JSONRPC: "2.0", Method: method, Params: rawParams})
}

// dispatchLoop pumps incoming frames into the pending-request map.
// Notifications and orphan replies are dropped silently — Session B
// only models a request/reply client; future revisions can add
// notification handlers here.
func (c *Client) dispatchLoop() {
	for {
		select {
		case <-c.closed:
			return
		default:
		}
		frame, err := c.t.Recv(context.Background())
		if err != nil {
			c.recvErr.Store(err)
			c.closeOnce.Do(func() { close(c.closed); _ = c.t.Close() })
			return
		}
		var msg rpcMessage
		if err := json.Unmarshal(frame, &msg); err != nil {
			// Malformed frame — drop and keep going. The server is
			// allowed to send notifications we don't recognize.
			continue
		}
		if len(msg.ID) == 0 {
			continue // notification from server, ignored for now
		}
		var id int64
		if err := json.Unmarshal(msg.ID, &id); err != nil {
			continue
		}
		if chAny, ok := c.pending.LoadAndDelete(id); ok {
			chAny.(chan rpcMessage) <- msg
		}
	}
}
