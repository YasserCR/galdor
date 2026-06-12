package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// NewStreamableHTTPClientTransport dials an MCP "Streamable HTTP" server
// at rawURL and returns a client-side Transport. It is the counterpart to
// NewStreamableHTTPTransport (the server): each outbound frame is POSTed
// to the endpoint, and the JSON-RPC reply body is queued for the next
// Recv. The Mcp-Session-Id the server returns on the initialize response
// is captured and echoed on every subsequent request, as the spec
// requires.
//
// Scope: this is a request/response client. It issues requests and reads
// their replies — the shape Client.Initialize / ListTools / CallTool /
// AsRegistry need — and does not handle server-initiated requests. That
// covers MCP debugging and remote-tool adoption; a server that pushes
// sampling/createMessage or roots/list to the client is out of scope.
//
// rawURL must be an absolute http or https URL. opts tune the underlying
// *http.Client.
func NewStreamableHTTPClientTransport(rawURL string, opts ...HTTPClientOption) (Transport, error) {
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
	t := &httpClientTransport{
		url:    rawURL,
		client: &http.Client{Timeout: 60 * time.Second},
		// The Client's dispatch loop drains this continuously, so the
		// buffer only needs to absorb bursts of concurrent replies. With
		// more than maxBufferedReplies replies arriving before the loop
		// drains, Send applies backpressure (it blocks, with ctx/done as
		// escapes) rather than dropping a frame.
		replies: make(chan []byte, maxBufferedReplies),
		done:    make(chan struct{}),
	}
	for _, opt := range opts {
		opt(t)
	}
	return t, nil
}

// maxBufferedReplies bounds how many replies can be queued for Recv
// before Send applies backpressure. Sized comfortably above the Server's
// own dispatch concurrency (64) so a burst of concurrent calls never
// stalls a Send in practice.
const maxBufferedReplies = 64

// HTTPClientOption configures NewStreamableHTTPClientTransport.
type HTTPClientOption func(*httpClientTransport)

// WithHTTPClient overrides the *http.Client used for requests (custom
// timeout, proxy, TLS, or a test transport).
func WithHTTPClient(c *http.Client) HTTPClientOption {
	return func(t *httpClientTransport) {
		if c != nil {
			t.client = c
		}
	}
}

// httpClientTransport implements Transport against a remote Streamable
// HTTP MCP server. Send performs the POST synchronously and enqueues the
// reply body (if any); Recv pops the next queued reply. Because the
// galdor Client correlates replies by JSON-RPC id, the queue order does
// not need to match request order.
type httpClientTransport struct {
	url    string
	client *http.Client

	mu        sync.Mutex
	sessionID string // learned from the initialize response header

	replies   chan []byte
	closeOnce sync.Once
	done      chan struct{}
}

// Send marshals msg, POSTs it, and queues the JSON-RPC reply for Recv. A
// notification (no reply body / 202 Accepted) enqueues nothing, so the
// receive loop never sees a spurious frame.
func (t *httpClientTransport) Send(ctx context.Context, msg any) error {
	select {
	case <-t.done:
		return fmt.Errorf("mcp: transport closed")
	default:
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mcp: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	t.mu.Lock()
	sid := t.sessionID
	t.mu.Unlock()
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	// Capture the session id minted on the initialize response so later
	// requests echo it. The header is only present once.
	if got := resp.Header.Get("Mcp-Session-Id"); got != "" {
		t.mu.Lock()
		if t.sessionID == "" {
			t.sessionID = got
		}
		t.mu.Unlock()
	}

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxMessageBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("mcp: server returned HTTP %d: %s", resp.StatusCode, msg)
	}

	reply := normalizeReplyBody(raw)
	if len(reply) == 0 {
		// Notification (202 Accepted) or otherwise empty: nothing to
		// deliver to the dispatch loop.
		return nil
	}
	select {
	case t.replies <- reply:
		return nil
	case <-t.done:
		return fmt.Errorf("mcp: transport closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Recv returns the next queued JSON-RPC reply, or io.EOF once the
// transport is closed and the queue is drained.
func (t *httpClientTransport) Recv(ctx context.Context) ([]byte, error) {
	select {
	case b := <-t.replies:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		// Drain anything already queued before reporting EOF.
		select {
		case b := <-t.replies:
			return b, nil
		default:
			return nil, io.EOF
		}
	}
}

// Close releases the transport. Idempotent.
func (t *httpClientTransport) Close() error {
	t.closeOnce.Do(func() { close(t.done) })
	return nil
}

// normalizeReplyBody strips a leading SSE "data:" framing if a server
// chose to wrap the JSON-RPC reply in an event stream. galdor's own
// server replies with a bare application/json body, so this is a
// defensive accommodation for spec-compliant peers, not a parser.
func normalizeReplyBody(raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		return trimmed
	}
	var buf bytes.Buffer
	for _, line := range bytes.Split(trimmed, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if rest, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			buf.Write(bytes.TrimSpace(rest))
		}
	}
	return buf.Bytes()
}
