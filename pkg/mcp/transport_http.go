package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// NewStreamableHTTPTransport returns a Transport that runs an HTTP
// server bound to addr and speaks the MCP "Streamable HTTP" transport
// introduced in the 2024-11-05 spec revision. One endpoint is mounted:
//
//   - POST /  — clients POST JSON-RPC requests. The server replies
//     with the matching JSON-RPC response either as a single
//     `application/json` body, or as a `text/event-stream` body of
//     SSE-framed frames when the response needs to be streamed
//     (long-running tools, notifications). Clients must accept both
//     content types via `Accept: application/json, text/event-stream`.
//
// Session id is propagated via the `Mcp-Session-Id` HTTP header. The
// server assigns one on the response to `initialize` and the client
// must echo it back on every subsequent request in the same session.
//
// Concurrency: the host (Server.Serve) dispatches each request in its
// own goroutine and loops straight back to Recv, so several POSTs in
// one session can be in flight at once. Each in-flight request's reply
// channel is tracked in a map keyed by its JSON-RPC id; Send routes a
// reply to the matching waiter by the id of the message it is sending,
// the same way the client correlates replies. This keeps overlapping
// same-session requests from clobbering each other.
//
// Close shuts the HTTP server down. Idempotent.
//
// Spec: https://modelcontextprotocol.io/specification/2025-03-26/basic/transports#streamable-http
func NewStreamableHTTPTransport(addr string) Transport {
	return newStreamableHTTPTransport(addr)
}

func newStreamableHTTPTransport(addr string) *streamableHTTPTransport {
	t := &streamableHTTPTransport{
		incoming: make(chan httpRequest),
		pending:  make(map[string]*httpRequest),
		done:     make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", t.handle)
	t.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	t.startErr = make(chan error, 1)
	t.listen()
	return t
}

// httpRequest carries one POST body to Recv and provides a one-shot
// reply channel back to the HTTP handler. id is the JSON-RPC request
// id (raw bytes, as a string) used to correlate the eventual Send.
type httpRequest struct {
	id    string
	body  []byte
	reply chan []byte // send the JSON-RPC reply bytes here, or close to abandon
	ctx   context.Context
}

type streamableHTTPTransport struct {
	srv      *http.Server
	ln       net.Listener
	addr     string
	startErr chan error

	mu        sync.Mutex
	sessionID string // assigned on the response to `initialize`
	// pending maps a JSON-RPC request id (its raw JSON bytes, as a
	// string) to the in-flight request awaiting a reply. Keyed by id so
	// overlapping same-session requests are correlated correctly rather
	// than sharing a single slot.
	pending map[string]*httpRequest

	incoming chan httpRequest

	closeOnce sync.Once
	done      chan struct{}
}

func (t *streamableHTTPTransport) listen() {
	ln, err := net.Listen("tcp", t.srv.Addr)
	if err != nil {
		t.startErr <- err
		close(t.startErr)
		t.closeOnce.Do(func() { close(t.done) })
		return
	}
	t.ln = ln
	t.addr = ln.Addr().String()
	close(t.startErr)
	go func() {
		err := t.srv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.closeOnce.Do(func() { close(t.done) })
		}
	}()
}

// originAllowed implements the spec's DNS-rebinding protection: a browser
// includes an Origin header on cross-site fetches, so a request whose
// Origin resolves to anything other than loopback is rejected. Requests
// with no Origin (ordinary non-browser MCP clients) are allowed.
func originAllowed(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

// Addr returns the bound network address. Useful in tests passing ":0".
func (t *streamableHTTPTransport) Addr() string { return t.addr }

// StartError returns the listener bind error, if any.
func (t *streamableHTTPTransport) StartError() error {
	select {
	case err := <-t.startErr:
		return err
	default:
		return nil
	}
}

func (t *streamableHTTPTransport) handle(w http.ResponseWriter, r *http.Request) {
	// DNS-rebinding guard (per the MCP spec): a browser attaches an
	// Origin header on cross-site requests, so reject any Origin that
	// isn't loopback. Non-browser MCP clients send no Origin and pass.
	if !originAllowed(r) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodPost:
		t.handlePost(w, r)
	case http.MethodGet:
		// Some clients optionally open a GET for server-initiated
		// notifications; we don't push any yet, so 405 is the honest
		// answer. Returning 200 with an empty SSE stream would be
		// equally spec-legal.
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	case http.MethodDelete:
		// Spec allows DELETE to terminate a session. We accept any
		// matching id and clear it; the next POST starts fresh.
		sid := r.Header.Get("Mcp-Session-Id")
		t.mu.Lock()
		if sid != "" && sid == t.sessionID {
			t.sessionID = ""
		}
		t.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (t *streamableHTTPTransport) handlePost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxMessageBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	defer func() { _ = r.Body.Close() }()

	// Peek the body to know whether the client expects a reply (request)
	// or this is a notification (no id field).
	var probe rpcMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		http.Error(w, "parse JSON-RPC: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Session-id validation: anything other than `initialize` must
	// echo the assigned id. We do this leniently — if no id has been
	// assigned yet we accept the request and let the host produce an
	// id during initialize.
	sid := r.Header.Get("Mcp-Session-Id")
	t.mu.Lock()
	have := t.sessionID
	t.mu.Unlock()
	// Once a session id has been assigned, every non-initialize request
	// must echo it exactly — including not omitting it. The old check
	// skipped validation when the header was absent, so any request
	// without the header bypassed the session entirely.
	if have != "" && probe.Method != MethodInitialize && sid != have {
		http.Error(w, "missing or unknown session id", http.StatusNotFound)
		return
	}

	isNotification := len(probe.ID) == 0

	req := httpRequest{
		id:    string(probe.ID),
		body:  body,
		reply: make(chan []byte, 1),
		ctx:   r.Context(),
	}

	// Register the pending request (requests only) BEFORE handing off, so
	// the check-and-insert is atomic: two concurrent POSTs reusing one
	// JSON-RPC id can't clobber each other's reply slot — the second is
	// rejected rather than orphaning the first.
	if !isNotification {
		t.mu.Lock()
		if _, dup := t.pending[req.id]; dup {
			t.mu.Unlock()
			http.Error(w, "duplicate in-flight request id", http.StatusConflict)
			return
		}
		t.pending[req.id] = &req
		t.mu.Unlock()
		// Clean the slot up on every return path below (handoff failure,
		// cancellation, or after the reply lands), so a request that
		// gives up never leaks its pending entry.
		defer func() {
			t.mu.Lock()
			delete(t.pending, req.id)
			t.mu.Unlock()
		}()
	}

	// Hand off to Recv.
	select {
	case t.incoming <- req:
	case <-t.done:
		http.Error(w, "transport closed", http.StatusServiceUnavailable)
		return
	case <-r.Context().Done():
		http.Error(w, "request cancelled", http.StatusRequestTimeout)
		return
	}

	if isNotification {
		// No reply expected; the host's Server loop ignores
		// notifications.
		close(req.reply)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Wait for exactly one reply from Send.
	select {
	case reply, ok := <-req.reply:
		if !ok {
			http.Error(w, "no reply", http.StatusInternalServerError)
			return
		}
		// If this was an `initialize` response, mint a session id and
		// pin it on the transport so the client can echo it.
		if probe.Method == MethodInitialize {
			t.mu.Lock()
			if t.sessionID == "" {
				t.sessionID = newSessionID()
			}
			id := t.sessionID
			t.mu.Unlock()
			w.Header().Set("Mcp-Session-Id", id)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(reply)
	case <-t.done:
		http.Error(w, "transport closed", http.StatusServiceUnavailable)
	case <-r.Context().Done():
		http.Error(w, "request cancelled", http.StatusRequestTimeout)
	}
}

// Recv pops the next POST body off the queue and, for requests
// (non-empty id), registers its reply channel keyed by id so the
// matching Send can fulfil it. Returns io.EOF on close.
func (t *streamableHTTPTransport) Recv(ctx context.Context) ([]byte, error) {
	// Pending registration happens in handlePost (atomically, before the
	// handoff) so duplicate in-flight ids are rejected; Recv just pops the
	// next body off the queue.
	select {
	case req := <-t.incoming:
		return req.body, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		// Drain any in-flight request first.
		select {
		case req := <-t.incoming:
			return req.body, nil
		default:
		}
		return nil, io.EOF
	}
}

// Send routes msg back to the pending POST handler whose request id
// matches the id of the message being sent. Replies for unknown ids
// (e.g. server-initiated notifications) are dropped — on Streamable
// HTTP without a long-lived GET stream there is nowhere to push them.
func (t *streamableHTTPTransport) Send(ctx context.Context, msg any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-t.done:
		return errors.New("mcp: transport closed")
	default:
	}
	buf, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mcp: encode frame: %w", err)
	}
	// Skip leading/trailing whitespace just to keep the body compact.
	buf = bytes.TrimSpace(buf)

	// Extract the JSON-RPC id of the reply so we can match it to the
	// waiting request. Notifications and id-less frames have no waiter.
	var idHolder struct {
		ID json.RawMessage `json:"id,omitempty"`
	}
	_ = json.Unmarshal(buf, &idHolder)
	key := string(idHolder.ID)

	t.mu.Lock()
	req := t.pending[key]
	if req != nil {
		delete(t.pending, key)
	}
	t.mu.Unlock()
	if key == "" || req == nil {
		// No waiter for this id (notification, or a reply whose request
		// already gave up). Drop it; a future revision can buffer for a
		// GET subscriber.
		return nil
	}
	select {
	case req.reply <- buf:
		return nil
	case <-req.ctx.Done():
		return req.ctx.Err()
	case <-t.done:
		return errors.New("mcp: transport closed")
	}
}

// Close shuts the HTTP server down and unblocks any in-flight
// handlers. Idempotent.
func (t *streamableHTTPTransport) Close() error {
	var err error
	t.closeOnce.Do(func() {
		close(t.done)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err = t.srv.Shutdown(ctx)
	})
	return err
}
