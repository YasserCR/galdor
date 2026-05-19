package a2a

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Handler processes one task. It receives the task in its current
// state (with the user's incoming message already appended to
// History) and is expected to mutate it: append assistant turns,
// set Artifacts, transition Status.State to a terminal value
// (Completed / Failed / Canceled) or to InputRequired if the agent
// needs more input from the user.
//
// Returning an error transitions the task to TaskFailed with the
// error's message attached to Status.Message.
type Handler interface {
	Handle(ctx context.Context, t *Task) error
}

// HandlerFunc adapts a plain function to the Handler interface.
type HandlerFunc func(ctx context.Context, t *Task) error

// Handle implements Handler.
func (f HandlerFunc) Handle(ctx context.Context, t *Task) error { return f(ctx, t) }

// Server exposes an Agent over the A2A HTTP surface. It implements
// http.Handler so it slots into any net/http stack:
//
//	srv := a2a.NewServer(card, handler)
//	http.ListenAndServe(":8080", srv)
//
// Two routes are served:
//
//   - GET  /.well-known/agent.json  → the Agent Card
//   - POST <any other path>         → JSON-RPC (tasks/send, tasks/get)
//
// Tasks are kept in an in-memory store keyed by ID. This is enough
// for single-process deployments and tests; a future Session can
// add a pluggable persistent store (the SQLite backend in
// internal/store would be a natural fit).
type Server struct {
	card    AgentCard
	handler Handler

	mu    sync.Mutex
	tasks map[string]*Task
}

// NewServer constructs a Server. The card is served verbatim at the
// well-known path; the handler processes every incoming task.
func NewServer(card AgentCard, handler Handler) *Server {
	if handler == nil {
		handler = HandlerFunc(func(_ context.Context, t *Task) error {
			t.Status.State = TaskCompleted
			return nil
		})
	}
	return &Server{
		card:    card,
		handler: handler,
		tasks:   map[string]*Task{},
	}
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == AgentCardPath {
		s.serveAgentCard(w)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.serveJSONRPC(w, r)
}

func (s *Server) serveAgentCard(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(s.card)
}

func (s *Server) serveJSONRPC(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()
	var msg rpcMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		writeJSONRPC(w, errorReply(nil, ErrCodeParseError, "parse error", err.Error()))
		return
	}
	if msg.JSONRPC != "2.0" {
		writeJSONRPC(w, errorReply(msg.ID, ErrCodeInvalidRequest, "jsonrpc must be \"2.0\"", msg.JSONRPC))
		return
	}
	var reply rpcMessage
	switch msg.Method {
	case MethodTasksSend:
		reply = s.handleTasksSend(r.Context(), msg)
	case MethodTasksGet:
		reply = s.handleTasksGet(msg)
	default:
		reply = errorReply(msg.ID, ErrCodeMethodNotFound, "method not found", msg.Method)
	}
	writeJSONRPC(w, reply)
}

func (s *Server) handleTasksSend(ctx context.Context, req rpcMessage) rpcMessage {
	var p tasksSendParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errorReply(req.ID, ErrCodeInvalidParams, "decode params", err.Error())
	}
	if len(p.Message.Parts) == 0 {
		return errorReply(req.ID, ErrCodeInvalidParams, "message.parts is empty", "")
	}

	// Look up or create the task. Clients may reuse IDs across calls
	// in a multi-turn conversation (e.g., to satisfy an
	// input-required state).
	s.mu.Lock()
	t, ok := s.tasks[p.ID]
	if !ok {
		id := p.ID
		if id == "" {
			id = uuid.NewString()
		}
		t = &Task{
			ID:        id,
			SessionID: p.SessionID,
			Status:    TaskStatus{State: TaskSubmitted, Timestamp: time.Now().UTC()},
			Metadata:  p.Metadata,
		}
		s.tasks[id] = t
	}
	// Append the new user message and flip to working.
	t.Append(p.Message)
	t.Status.State = TaskWorking
	t.Status.Timestamp = time.Now().UTC()
	s.mu.Unlock()

	// Run the handler outside the lock so a slow agent doesn't
	// block tasks/get.
	if err := s.handler.Handle(ctx, t); err != nil {
		s.mu.Lock()
		t.Status.State = TaskFailed
		t.Status.Message = &Message{Role: RoleAgent, Parts: []Part{TextPart(err.Error())}}
		t.Status.Timestamp = time.Now().UTC()
		s.mu.Unlock()
	} else if !isTerminalState(t.Status.State) && t.Status.State != TaskInputRequired {
		// Handler returned cleanly but forgot to set a terminal
		// state; assume completion.
		s.mu.Lock()
		t.Status.State = TaskCompleted
		t.Status.Timestamp = time.Now().UTC()
		s.mu.Unlock()
	}

	return successReply(req.ID, t)
}

func (s *Server) handleTasksGet(req rpcMessage) rpcMessage {
	var p tasksGetParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errorReply(req.ID, ErrCodeInvalidParams, "decode params", err.Error())
	}
	if p.ID == "" {
		return errorReply(req.ID, ErrCodeInvalidParams, "id is required", "")
	}
	s.mu.Lock()
	t, ok := s.tasks[p.ID]
	s.mu.Unlock()
	if !ok {
		return errorReply(req.ID, ErrCodeTaskNotFound, "task not found", p.ID)
	}
	// Return a copy with optionally truncated history so callers
	// can mutate the result without racing on the in-memory store.
	copyT := *t
	if p.HistoryLength > 0 && len(copyT.History) > p.HistoryLength {
		copyT.History = copyT.History[len(copyT.History)-p.HistoryLength:]
	}
	return successReply(req.ID, copyT)
}

// AgentCard returns the card the server publishes. Exposed so the
// caller can inspect or extend it before passing the server to
// http.Server.
func (s *Server) AgentCard() AgentCard { return s.card }

// isTerminalState reports whether state is a final state per the
// A2A spec.
func isTerminalState(state TaskState) bool {
	switch state {
	case TaskCompleted, TaskFailed, TaskCanceled:
		return true
	}
	return false
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

func writeJSONRPC(w http.ResponseWriter, msg rpcMessage) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// JSON-RPC over HTTP always returns 200 even for protocol-level
	// errors — the error lives in the response envelope.
	_ = json.NewEncoder(w).Encode(msg)
}

// Compile-time interface assertion.
var (
	_ http.Handler = (*Server)(nil)
	_ Handler      = HandlerFunc(nil)
)
