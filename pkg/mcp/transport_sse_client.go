package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// NewSSEClientTransport dials an MCP "HTTP+SSE" server at rawURL and
// returns a client-side Transport. It is the counterpart to
// NewSSETransport, which is the server: that one binds a listener on the
// address it is given, so handing it a remote server's URL connects to
// nobody — it waits for a caller that never arrives and the handshake
// times out. Reaching a remote SSE server needs this side.
//
// The protocol, which predates Streamable HTTP and is still the only one
// many deployed servers speak:
//
//   - GET rawURL opens the event stream. The first event is `endpoint`,
//     whose data is the URL that requests must be POSTed to — absolute,
//     or relative to the stream URL.
//   - Every JSON-RPC reply then arrives as a `message` event on that same
//     stream. The POST itself answers 202 Accepted with no body.
//
// The stream is opened on first use rather than here, so a transport that
// is constructed and never used holds no connection and starts no
// goroutine. Close shuts the stream down and unblocks a waiting Recv.
//
// Scope matches the Streamable HTTP client: requests and their replies,
// which is what Client.Initialize / ListTools / CallTool / AsRegistry
// need. A server that pushes sampling/createMessage or roots/list to the
// client is out of scope.
//
// rawURL must be an absolute http or https URL. Authentication goes
// through WithSSEHTTPClient, whose client can carry a RoundTripper that
// sets the headers the server asks for.
//
// Spec: https://modelcontextprotocol.io/specification/2024-11-05/basic/transports
func NewSSEClientTransport(rawURL string, opts ...SSEClientOption) (Transport, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("mcp: parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("mcp: url scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("mcp: url has no host: %q", rawURL)
	}
	t := &sseClientTransport{
		streamURL: rawURL,
		// No Timeout on the client: the stream's GET lives as long as the
		// session, and a client deadline would cut it off mid-session.
		// Per-request deadlines come from the caller's context.
		client:     &http.Client{},
		endpointCh: make(chan struct{}),
		messages:   make(chan []byte, maxBufferedReplies),
		done:       make(chan struct{}),
	}
	for _, opt := range opts {
		opt(t)
	}
	return t, nil
}

// SSEClientOption configures NewSSEClientTransport.
type SSEClientOption func(*sseClientTransport)

// WithSSEHTTPClient overrides the *http.Client used for the stream and
// for the POSTs (proxy, TLS, a header-setting RoundTripper, or a test
// transport). Leave its Timeout at zero: it applies to the stream's GET,
// which is meant to stay open for the whole session.
func WithSSEHTTPClient(c *http.Client) SSEClientOption {
	return func(t *sseClientTransport) {
		if c != nil {
			t.client = c
		}
	}
}

// sseClientTransport implements Transport against a remote HTTP+SSE MCP
// server. One goroutine owns the stream and feeds `messages`; Send POSTs
// to the endpoint the stream announced; Recv pops the next message.
// Because the galdor Client correlates replies by JSON-RPC id, queue
// order does not need to match request order.
type sseClientTransport struct {
	streamURL string
	client    *http.Client

	// startOnce opens the stream on first use, so constructing a
	// transport costs nothing until something is actually sent or read.
	startOnce sync.Once

	// The endpoint is only known once the first event arrives. Sends
	// before that wait rather than fail: the Client sends `initialize`
	// straight away and would otherwise beat the announcement.
	endpointOnce sync.Once
	endpointCh   chan struct{}
	endpointMu   sync.Mutex
	endpoint     string
	endpointErr  error

	messages  chan []byte
	closeOnce sync.Once
	done      chan struct{}
}

// start opens the stream once.
func (t *sseClientTransport) start() {
	t.startOnce.Do(func() { go t.readStream() })
}

// readStream keeps the GET open and dispatches what arrives on it.
//
// Its context is deliberately not any caller's: the stream outlives every
// single request, and tying it to the deadline of whichever call happened
// to be first would end the session as soon as that call returned. Close
// is what ends it, through done.
func (t *sseClientTransport) readStream() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-t.done:
			cancel()
		case <-ctx.Done():
		}
	}()

	// However this returns, nobody may be left waiting on the endpoint.
	defer t.endpointOnce.Do(func() { close(t.endpointCh) })

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.streamURL, nil)
	if err != nil {
		t.failEndpoint(err)
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := t.client.Do(req)
	if err != nil {
		t.failEndpoint(err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.failEndpoint(fmt.Errorf("server returned HTTP %d opening the stream: %s",
			resp.StatusCode, strings.TrimSpace(string(body))))
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)

	var event string
	var data bytes.Buffer

	flush := func() {
		defer func() {
			event = ""
			data.Reset()
		}()
		payload := strings.TrimSpace(data.String())
		if payload == "" {
			return
		}
		if event == "endpoint" {
			t.setEndpoint(payload)
			return
		}
		// "message" is the default event type in SSE, so a server that
		// omits the event: line is still correct and still means this.
		select {
		case t.messages <- []byte(payload):
		case <-t.done:
		case <-ctx.Done():
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, ":"):
			// A comment: what servers send to keep a proxy from closing
			// the connection for inactivity.
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	// An event that was never terminated by a blank line still counts.
	flush()
	t.failEndpoint(errors.New("the server closed the stream"))
	_ = t.Close()
}

// setEndpoint records where requests go. The announced value may be
// relative, so it is resolved against the stream URL.
func (t *sseClientTransport) setEndpoint(raw string) {
	base, err := url.Parse(t.streamURL)
	if err != nil {
		t.failEndpoint(err)
		return
	}
	ref, err := url.Parse(raw)
	if err != nil {
		t.failEndpoint(fmt.Errorf("server announced an invalid endpoint %q: %w", raw, err))
		return
	}
	t.endpointMu.Lock()
	t.endpoint = base.ResolveReference(ref).String()
	t.endpointMu.Unlock()
	t.endpointOnce.Do(func() { close(t.endpointCh) })
}

// failEndpoint records why the endpoint will never arrive and releases
// everyone waiting for it. The first reason wins; a later one is noise
// from the same failure unwinding.
func (t *sseClientTransport) failEndpoint(err error) {
	t.endpointMu.Lock()
	if t.endpointErr == nil && t.endpoint == "" {
		t.endpointErr = err
	}
	t.endpointMu.Unlock()
	t.endpointOnce.Do(func() { close(t.endpointCh) })
}

// awaitEndpoint blocks until the POST target is known, the transport is
// closed, or ctx is done.
func (t *sseClientTransport) awaitEndpoint(ctx context.Context) (string, error) {
	select {
	case <-t.endpointCh:
	case <-ctx.Done():
		return "", ctx.Err()
	case <-t.done:
		return "", errors.New("mcp: transport closed")
	}
	t.endpointMu.Lock()
	defer t.endpointMu.Unlock()
	if t.endpoint == "" {
		if t.endpointErr != nil {
			return "", fmt.Errorf("mcp: %w", t.endpointErr)
		}
		return "", errors.New("mcp: server never announced its message endpoint")
	}
	return t.endpoint, nil
}

// Send marshals msg and POSTs it to the endpoint the stream announced.
// The reply travels back over the stream, not over this response.
func (t *sseClientTransport) Send(ctx context.Context, msg any) error {
	select {
	case <-t.done:
		return errors.New("mcp: transport closed")
	default:
	}
	t.start()

	endpoint, err := t.awaitEndpoint(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mcp: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxMessageBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := strings.TrimSpace(string(raw))
		if detail == "" {
			detail = resp.Status
		}
		return fmt.Errorf("mcp: server returned HTTP %d: %s", resp.StatusCode, detail)
	}
	// A server that answers in the POST body anyway is accepted rather
	// than argued with: it costs a few lines here and saves depending on
	// which revision of the transport the peer implemented.
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && json.Valid(trimmed) {
		select {
		case t.messages <- trimmed:
		case <-t.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Recv returns the next message from the stream, or io.EOF once the
// transport is closed and the queue is drained.
func (t *sseClientTransport) Recv(ctx context.Context) ([]byte, error) {
	t.start()
	select {
	case b := <-t.messages:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		// Drain what already arrived before reporting EOF.
		select {
		case b := <-t.messages:
			return b, nil
		default:
			return nil, io.EOF
		}
	}
}

// Close shuts the stream down and unblocks anyone waiting. Idempotent.
func (t *sseClientTransport) Close() error {
	t.closeOnce.Do(func() { close(t.done) })
	return nil
}
