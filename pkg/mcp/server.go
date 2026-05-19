package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/YasserCR/galdor/pkg/tool"
)

// Server exposes a tool.Registry over the MCP wire. It serves one
// peer at a time over the configured Transport; spawn one Server
// instance per inbound connection if you need to support multiple
// clients (the stdio transport is single-peer by definition).
//
// Servers are safe to construct; Serve is what binds them to a
// transport and starts handling frames.
type Server struct {
	reg  *tool.Registry
	info ServerInfo

	// Strict, when true, rejects requests received before the
	// initialize handshake completes. Most clients send initialize
	// first anyway; turning Strict on catches misbehaving clients
	// early.
	Strict bool
}

// NewServer constructs a Server backed by reg. info populates the
// serverInfo block returned during the initialize handshake.
func NewServer(reg *tool.Registry, info ServerInfo) *Server {
	if info.Name == "" {
		info.Name = "galdor-mcp"
	}
	if info.Version == "" {
		info.Version = "0"
	}
	return &Server{reg: reg, info: info}
}

// Serve handles MCP frames over t until ctx is cancelled, t reports
// io.EOF, or a fatal transport error occurs. Returns nil on clean
// shutdown, the transport error otherwise.
//
// Concurrency: requests are dispatched in goroutines so a slow tool
// doesn't block the receive loop. Send is synchronized inside the
// Transport, so concurrent replies stay frame-aligned.
func (s *Server) Serve(ctx context.Context, t Transport) error {
	defer func() { _ = t.Close() }()

	var (
		mu          sync.Mutex
		initialized bool
		wg          sync.WaitGroup
	)

	for {
		if err := ctx.Err(); err != nil {
			wg.Wait()
			return nil
		}
		frame, err := t.Recv(ctx)
		if errors.Is(err, io.EOF) {
			wg.Wait()
			return nil
		}
		if err != nil {
			wg.Wait()
			return err
		}
		var msg rpcMessage
		if err := json.Unmarshal(frame, &msg); err != nil {
			// Per JSON-RPC: malformed frames should get a parse-error
			// reply when they look like requests. We can't recover an
			// id from an unparseable frame, so reply with id=null.
			_ = t.Send(ctx, errorReply(nil, ErrCodeParseError, "parse error", err.Error()))
			continue
		}
		// Notifications don't get replies; just route by method.
		if len(msg.ID) == 0 {
			if msg.Method == MethodInitialized {
				mu.Lock()
				initialized = true
				mu.Unlock()
			}
			continue
		}

		mu.Lock()
		isInit := initialized
		mu.Unlock()
		if s.Strict && !isInit && msg.Method != MethodInitialize {
			_ = t.Send(ctx, errorReply(msg.ID, ErrCodeInvalidRequest, "server not initialized", "send initialize first"))
			continue
		}

		wg.Add(1)
		go func(req rpcMessage) {
			defer wg.Done()
			reply := s.dispatch(ctx, req)
			if err := t.Send(ctx, reply); err != nil {
				// Best-effort: nothing we can do if the peer's
				// gone. Log when we add a logger.
				_ = err
			}
		}(msg)
	}
}

// dispatch handles one inbound request and returns the response
// frame (success or error) to send back. ctx is the connection's
// context; per-call cancellation could be added later by deriving a
// child context here.
func (s *Server) dispatch(ctx context.Context, req rpcMessage) rpcMessage {
	switch req.Method {
	case MethodInitialize:
		return s.handleInitialize(req)
	case MethodToolsList:
		return s.handleToolsList(req)
	case MethodToolsCall:
		return s.handleToolsCall(ctx, req)
	default:
		return errorReply(req.ID, ErrCodeMethodNotFound, "method not found", req.Method)
	}
}

func (s *Server) handleInitialize(req rpcMessage) rpcMessage {
	// We accept the client's protocol version verbatim so newer
	// clients can negotiate up. If the client's version is older
	// than ours, returning our own version signals "downgrade me to
	// your level" per the spec.
	var params initializeParams
	if len(req.Params) > 0 {
		_ = json.Unmarshal(req.Params, &params)
	}
	resp := initializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    Capabilities{Tools: &ToolsCapability{}},
		ServerInfo:      s.info,
	}
	return successReply(req.ID, resp)
}

func (s *Server) handleToolsList(req rpcMessage) rpcMessage {
	if s.reg == nil {
		return successReply(req.ID, toolsListResult{Tools: nil})
	}
	tools := s.reg.Tools()
	out := toolsListResult{Tools: make([]ToolDef, 0, len(tools))}
	for _, tl := range tools {
		raw, err := json.Marshal(tl.Schema())
		if err != nil {
			return errorReply(req.ID, ErrCodeInternalError, "encode schema", err.Error())
		}
		out.Tools = append(out.Tools, ToolDef{
			Name:        tl.Name(),
			Description: tl.Description(),
			InputSchema: raw,
		})
	}
	return successReply(req.ID, out)
}

func (s *Server) handleToolsCall(ctx context.Context, req rpcMessage) rpcMessage {
	var params toolsCallParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errorReply(req.ID, ErrCodeInvalidParams, "decode tools/call params", err.Error())
		}
	}
	if s.reg == nil {
		return errorReply(req.ID, ErrCodeInternalError, "no tool registry", "")
	}
	t, ok := s.reg.Get(params.Name)
	if !ok {
		return errorReply(req.ID, ErrCodeMethodNotFound, "tool not found", params.Name)
	}
	out, err := t.ExecuteJSON(ctx, params.Arguments)
	if err != nil {
		// Tool errors are returned as a regular response with
		// isError=true so the model can see them, not as JSON-RPC
		// errors (which would be transport-level failures).
		return successReply(req.ID, toolsCallResult{
			Content: []ContentPart{{Type: "text", Text: err.Error()}},
			IsError: true,
		})
	}
	return successReply(req.ID, toolsCallResult{
		Content: []ContentPart{{Type: "text", Text: string(out)}},
	})
}

func successReply(id json.RawMessage, result any) rpcMessage {
	raw, err := json.Marshal(result)
	if err != nil {
		return errorReply(id, ErrCodeInternalError, "encode result", err.Error())
	}
	return rpcMessage{JSONRPC: "2.0", ID: id, Result: raw}
}

func errorReply(id json.RawMessage, code int, message, detail string) rpcMessage {
	e := &rpcError{Code: code, Message: message}
	if detail != "" {
		e.Data, _ = json.Marshal(map[string]string{"detail": detail})
	}
	if id == nil {
		id = json.RawMessage("null")
	}
	return rpcMessage{JSONRPC: "2.0", ID: id, Error: e}
}

