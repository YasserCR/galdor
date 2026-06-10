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

// Inbound-request and store limits. The A2A server accepts unauthenticated
// network input, so every growth vector is bounded.
const (
	// maxRequestBytes caps the JSON-RPC request body. A peer cannot drive
	// unbounded allocation with a giant history/metadata blob.
	maxRequestBytes = 4 << 20 // 4 MiB

	// maxTaskIDLen caps a client-supplied task ID so the store can't be
	// bloated with pathologically long keys.
	maxTaskIDLen = 512

	// defaultMaxTasks bounds the in-memory task store. At the cap a new
	// task evicts the oldest terminal task; if every task is still
	// active the send is rejected rather than growing without bound.
	defaultMaxTasks = 4096
)

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
	card     AgentCard
	handler  Handler
	maxTasks int

	mu    sync.Mutex
	tasks map[string]*taskEntry
}

// taskEntry wraps a stored Task. procMu serializes tasks/send for this
// id (so concurrent same-id sends don't interleave); mu guards the task
// pointer and is held only briefly, so tasks/get can read the task's
// current state (e.g. "working") while a long handler runs — it is NOT
// held across the handler. updated is the last mutation time, used to
// evict the oldest entries when the store is full.
type taskEntry struct {
	procMu  sync.Mutex
	mu      sync.Mutex
	task    *Task
	updated time.Time
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
		card:     card,
		handler:  handler,
		maxTasks: defaultMaxTasks,
		tasks:    map[string]*taskEntry{},
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
	// Bound the request body: an unauthenticated peer must not be able to
	// drive unbounded allocation with a multi-gigabyte POST.
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
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
	if len(p.ID) > maxTaskIDLen {
		return errorReply(req.ID, ErrCodeInvalidParams, "id too long", "")
	}

	// Look up or create the task entry under the store lock. Clients may
	// reuse IDs across calls in a multi-turn conversation (to continue an
	// input-required task). A new task is subject to the store cap.
	s.mu.Lock()
	e, ok := s.tasks[p.ID]
	if !ok {
		if len(s.tasks) >= s.maxTasks && !s.evictOneLocked() {
			s.mu.Unlock()
			return errorReply(req.ID, ErrCodeInternalError, "task store is full", "")
		}
		id := p.ID
		if id == "" {
			id = uuid.NewString()
		}
		e = &taskEntry{
			task: &Task{
				ID:        id,
				SessionID: p.SessionID,
				Status:    TaskStatus{State: TaskSubmitted, Timestamp: time.Now().UTC()},
				Metadata:  p.Metadata,
			},
			updated: time.Now().UTC(),
		}
		s.tasks[id] = e
	}
	s.mu.Unlock()

	// procMu serializes same-id sends. Crucially we do NOT hold the data
	// lock (e.mu) across the handler — that's what let tasks/get hang for
	// the whole handler duration.
	e.procMu.Lock()
	defer e.procMu.Unlock()

	// Phase 1 (brief data lock): reject a terminal task, append the user
	// message, flip to working, and detach an independent copy for the
	// handler. After this the stored task shows "working" to tasks/get.
	e.mu.Lock()
	if isTerminalState(e.task.Status.State) {
		state := e.task.Status.State
		e.mu.Unlock()
		return errorReply(req.ID, ErrCodeInvalidTaskState,
			"task is in a terminal state and cannot be continued", string(state))
	}
	e.task.Append(p.Message)
	// Honor an updated SessionID / Metadata sent with a continuing message —
	// on REUSE these were previously dropped (only the creation-time values
	// survived). Metadata is merged (new keys win); SessionID is updated when
	// the client supplies one.
	if p.SessionID != "" {
		e.task.SessionID = p.SessionID
	}
	if len(p.Metadata) > 0 {
		if e.task.Metadata == nil {
			e.task.Metadata = make(map[string]any, len(p.Metadata))
		}
		for k, v := range p.Metadata {
			e.task.Metadata[k] = v
		}
	}
	e.task.Status.State = TaskWorking
	e.task.Status.Timestamp = time.Now().UTC()
	e.updated = time.Now().UTC()
	wc := e.task.deepCopy()
	e.mu.Unlock()

	// Phase 2: run the handler on the detached copy with NO lock held, so
	// a concurrent tasks/get returns the "working" snapshot promptly.
	if err := s.handler.Handle(ctx, wc); err != nil {
		wc.Status.State = TaskFailed
		wc.Status.Message = &Message{Role: RoleAgent, Parts: []Part{TextPart(err.Error())}}
		wc.Status.Timestamp = time.Now().UTC()
	} else if !isTerminalState(wc.Status.State) && wc.Status.State != TaskInputRequired {
		// Handler returned cleanly but forgot to set a terminal state.
		wc.Status.State = TaskCompleted
		wc.Status.Timestamp = time.Now().UTC()
	}

	// Phase 3 (brief data lock): commit the result and snapshot for the
	// reply.
	e.mu.Lock()
	e.task = wc
	e.updated = time.Now().UTC()
	snap := e.task.snapshot()
	e.mu.Unlock()
	return successReply(req.ID, snap)
}

// evictOneLocked removes the oldest terminal (completed/failed/canceled)
// task to make room when the store is at capacity. It returns false when
// every task is still active, so the caller rejects the new task rather
// than dropping in-flight work. Caller must hold s.mu.
func (s *Server) evictOneLocked() bool {
	var oldestID string
	var oldest time.Time
	for id, e := range s.tasks {
		e.mu.Lock()
		terminal := isTerminalState(e.task.Status.State)
		upd := e.updated
		e.mu.Unlock()
		if !terminal {
			continue
		}
		if oldestID == "" || upd.Before(oldest) {
			oldestID, oldest = id, upd
		}
	}
	if oldestID == "" {
		return false
	}
	delete(s.tasks, oldestID)
	return true
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
	e, ok := s.tasks[p.ID]
	s.mu.Unlock()
	if !ok {
		return errorReply(req.ID, ErrCodeTaskNotFound, "task not found", p.ID)
	}
	// Snapshot under the per-task lock so we never read History/Status
	// while a concurrent send is mutating them.
	e.mu.Lock()
	copyT := e.task.snapshot()
	e.mu.Unlock()
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
