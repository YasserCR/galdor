package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// NewSSETransport returns a Transport that runs an HTTP server bound
// to addr and speaks the MCP "HTTP+SSE" transport (the default before
// the 2024-11-05 spec revision; still the only thing many clients
// understand). Two routes are mounted:
//
//   - GET  /sse       — clients open a Server-Sent Events stream here.
//     The first frame is an `endpoint` event whose data is the URL
//     clients must POST requests to. Subsequent frames are `message`
//     events whose data is a JSON-RPC reply.
//   - POST /messages  — clients POST JSON-RPC request bodies. The
//     server acknowledges with 202 Accepted and pushes the response
//     down the SSE stream tagged with the same session id.
//
// Sessions are identified by a `sessionId` query parameter on the
// POST endpoint; the value is assigned by the server and echoed in
// the `endpoint` event on the GET stream.
//
// One Transport instance owns one listener and demuxes across N
// concurrent sessions, but to fit the request/response Transport
// interface the Transport only surfaces one session at a time to its
// host (the Server.Serve loop). When a new session opens it becomes
// the active one; the previous session's stream is closed cleanly.
// In practice MCP clients open exactly one session per server so this
// is invisible.
//
// Close shuts down the HTTP server and unblocks any in-flight Recv.
// Safe to call multiple times.
//
// Spec: https://modelcontextprotocol.io/specification/2024-11-05/basic/transports
func NewSSETransport(addr string) Transport {
	return newSSETransport(addr)
}

func newSSETransport(addr string) *sseTransport {
	t := &sseTransport{
		incoming: make(chan []byte, 16),
		done:     make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", t.handleSSE)
	mux.HandleFunc("/messages", t.handlePost)
	t.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	t.startErr = make(chan error, 1)
	t.listen()
	return t
}

type sseTransport struct {
	srv      *http.Server
	ln       net.Listener
	addr     string
	startErr chan error

	mu      sync.Mutex
	session *sseSession // current active session (at most one)

	incoming chan []byte // POST bodies arriving from any session

	closeOnce sync.Once
	done      chan struct{}
}

// newSessionID returns a random hex string for use as an opaque
// session identifier. 16 bytes ≈ 128 bits of entropy, plenty for
// the lifetime of a process.
func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failures are essentially impossible on the
		// platforms Go supports; fall back to a process-monotonic
		// counter would change error semantics so we panic.
		panic(fmt.Sprintf("mcp: rand: %v", err))
	}
	return hex.EncodeToString(b[:])
}

// sseSession is one open GET /sse stream.
type sseSession struct {
	id      string
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex // serializes writes to w
	closed  chan struct{}
	once    sync.Once
}

func (s *sseSession) close() {
	s.once.Do(func() { close(s.closed) })
}

// listen binds the listener synchronously so Addr() / errors surface
// immediately, then runs Serve in a goroutine.
func (t *sseTransport) listen() {
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
			// Server died on its own; mark transport closed so Recv unblocks.
			t.closeOnce.Do(func() { close(t.done) })
		}
	}()
}

// Addr returns the bound network address. Useful in tests that pass
// ":0" to NewSSETransport.
func (t *sseTransport) Addr() string { return t.addr }

// StartError returns the listener bind error, if any. nil on success.
func (t *sseTransport) StartError() error {
	select {
	case err := <-t.startErr:
		return err
	default:
		return nil
	}
}

func (t *sseTransport) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	id := newSessionID()
	sess := &sseSession{
		id:      id,
		w:       w,
		flusher: flusher,
		closed:  make(chan struct{}),
	}

	// Swap in as the active session. Drop the previous one — its SSE
	// stream will end and its client will reconnect if it wants to.
	t.mu.Lock()
	prev := t.session
	t.session = sess
	t.mu.Unlock()
	if prev != nil {
		prev.close()
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// First event: tell the client where to POST.
	endpoint := fmt.Sprintf("/messages?sessionId=%s", id)
	sess.mu.Lock()
	_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpoint)
	flusher.Flush()
	sess.mu.Unlock()

	// Hold the stream open until the client disconnects, this session
	// is superseded, or the transport closes.
	select {
	case <-r.Context().Done():
	case <-sess.closed:
	case <-t.done:
	}

	// Clear active-session pointer if we're still it.
	t.mu.Lock()
	if t.session == sess {
		t.session = nil
	}
	t.mu.Unlock()
	sess.close()

	// Take the session lock once more so any concurrent Send finishes
	// before the handler returns — net/http reclaims the
	// ResponseWriter on return and races would corrupt its internal
	// buffer.
	sess.mu.Lock()
	sess.mu.Unlock() //nolint:staticcheck // intentional barrier
}

func (t *sseTransport) handlePost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sid := r.URL.Query().Get("sessionId")
	t.mu.Lock()
	sess := t.session
	t.mu.Unlock()
	if sess == nil || (sid != "" && sid != sess.id) {
		http.Error(w, "no active session", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	// Hand the body to Recv. If Recv is not consuming fast enough we
	// block the POST until it does — back-pressure.
	select {
	case t.incoming <- body:
	case <-t.done:
		http.Error(w, "transport closed", http.StatusServiceUnavailable)
		return
	case <-r.Context().Done():
		http.Error(w, "request cancelled", http.StatusRequestTimeout)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// Send writes msg as a `message` SSE event on the active session's
// stream. Returns an error if no session is connected.
func (t *sseTransport) Send(ctx context.Context, msg any) error {
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
	t.mu.Lock()
	sess := t.session
	t.mu.Unlock()
	if sess == nil {
		return errors.New("mcp: sse: no active session")
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	select {
	case <-sess.closed:
		return errors.New("mcp: sse: session closed")
	default:
	}
	if _, err := fmt.Fprintf(sess.w, "event: message\ndata: %s\n\n", buf); err != nil {
		return fmt.Errorf("mcp: sse write: %w", err)
	}
	sess.flusher.Flush()
	return nil
}

// Recv blocks until the next POST body arrives, the context is
// cancelled, or the transport closes. Returns io.EOF on clean close.
func (t *sseTransport) Recv(ctx context.Context) ([]byte, error) {
	select {
	case b := <-t.incoming:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		// Drain any buffered frame first so a Close + Recv race doesn't
		// silently swallow the last message.
		select {
		case b := <-t.incoming:
			return b, nil
		default:
		}
		return nil, io.EOF
	}
}

// Close shuts the HTTP server down. Pending Send / Recv calls are
// unblocked with an error / io.EOF. Idempotent.
//
// Active SSE streams are torn down via http.Server.Close (not
// Shutdown) because a long-lived SSE GET handler never returns on its
// own — the spec intends those streams to live as long as the server
// does. Shutdown would block on them until its own timeout expires.
func (t *sseTransport) Close() error {
	var err error
	t.closeOnce.Do(func() {
		close(t.done)
		t.mu.Lock()
		sess := t.session
		t.session = nil
		t.mu.Unlock()
		if sess != nil {
			sess.close()
		}
		err = t.srv.Close()
	})
	return err
}
