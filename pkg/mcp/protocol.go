package mcp

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion is the MCP version negotiated in the initialize
// handshake. We pin to the published 2024-11-05 revision; servers
// that report a newer version are still accepted as long as the
// methods we use (initialize, tools/list, tools/call) keep their
// shape.
const ProtocolVersion = "2024-11-05"

// JSON-RPC 2.0 method names we implement.
const (
	MethodInitialize  = "initialize"
	MethodInitialized = "notifications/initialized"
	MethodToolsList   = "tools/list"
	MethodToolsCall   = "tools/call"
)

// JSON-RPC 2.0 standard error codes.
const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)

// rpcMessage is the union of JSON-RPC request, response and
// notification shapes — distinguishable by which fields are present.
// We decode every incoming frame into this type and route on the
// non-zero fields.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is the JSON-RPC 2.0 error envelope.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("mcp: JSON-RPC %d: %s", e.Code, e.Message)
}

// ClientInfo identifies the calling application in the initialize
// request. Servers can use it for logging and feature gating.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ServerInfo is the server-side counterpart, returned in the
// initialize response.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Capabilities advertises which optional protocol features either
// side supports. We only set ToolsListChanged on the server when we
// have an explicit way to notify clients of tool changes (not yet);
// the field is here so callers can probe future support.
type Capabilities struct {
	// Tools is non-nil when the side supports tool methods.
	// ListChanged signals that the side will notify the peer when
	// the tool list changes (not implemented in Session B).
	Tools *ToolsCapability `json:"tools,omitempty"`
}

// ToolsCapability is the inner shape of Capabilities.Tools.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// initializeParams is the params payload for the `initialize` method.
type initializeParams struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    Capabilities `json:"capabilities"`
	ClientInfo      ClientInfo   `json:"clientInfo"`
}

// initializeResult is the response payload for the `initialize` method.
type initializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    Capabilities `json:"capabilities"`
	ServerInfo      ServerInfo   `json:"serverInfo"`
}

// ToolDef is the MCP-shape tool description. Conceptually identical
// to schema.ToolDef but with the JSON tag layout MCP requires.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// toolsListResult is the response payload for `tools/list`.
type toolsListResult struct {
	Tools []ToolDef `json:"tools"`
}

// toolsCallParams is the params payload for `tools/call`.
type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// toolsCallResult is the response payload for `tools/call`. Content
// is a list of typed parts; we always emit a single text part in this
// implementation, but the field is a slice so future image / resource
// content lands cleanly.
type toolsCallResult struct {
	Content []ContentPart `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ContentPart is one element of a tool-call result's content list.
// MCP defines several types ("text", "image", "resource"); we only
// emit and consume "text" in Session B.
type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
