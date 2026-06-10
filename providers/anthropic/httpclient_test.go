package anthropic

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Regression for audit C1: the default HTTP client must NOT impose a
// global http.Client.Timeout, because that deadline covers the response
// body read and would kill long SSE streams / extended-thinking
// generations mid-flight.
func TestStreamSafeHTTPClient_NoGlobalTimeout(t *testing.T) {
	c := streamSafeHTTPClient(defaultResponseHeaderTimeout)
	if c.Timeout != 0 {
		t.Fatalf("default client must have no global Timeout (it would cap body reads); got %v", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.Transport)
	}
	if tr.ResponseHeaderTimeout != defaultResponseHeaderTimeout {
		t.Fatalf("ResponseHeaderTimeout = %v, want %v", tr.ResponseHeaderTimeout, defaultResponseHeaderTimeout)
	}
}

// slowBodyServer flushes headers immediately, then dribbles the body in
// chunks separated by `gap`, for `chunks` chunks. Total body time is
// roughly chunks*gap — simulating a slow token stream.
func slowBodyServer(t *testing.T, gap time.Duration, chunks int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter is not a Flusher")
			return
		}
		w.WriteHeader(http.StatusOK)
		fl.Flush() // headers out immediately
		for i := 0; i < chunks; i++ {
			time.Sleep(gap)
			_, _ = io.WriteString(w, "data: chunk\n\n")
			fl.Flush()
		}
	}))
}

// The stream-safe client reads a body that takes far longer than a
// naive 60s-style cap would have allowed relative to the time-to-headers
// budget: headers arrive instantly, the body dribbles, and the read
// completes because only the header phase is bounded.
func TestStreamSafeHTTPClient_SlowBodyCompletes(t *testing.T) {
	srv := slowBodyServer(t, 60*time.Millisecond, 8) // ~480ms body
	defer srv.Close()

	// Header timeout shorter than the total body time, to prove it does
	// NOT bound the body.
	c := streamSafeHTTPClient(200 * time.Millisecond)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading slow body failed (it should complete): %v", err)
	}
	if got := strings.Count(string(body), "chunk"); got != 8 {
		t.Fatalf("read %d chunks, want 8 — body was truncated", got)
	}
}

// Contrast: a client with a short GLOBAL Timeout (the pre-fix shape)
// aborts the very same slow body. This pins down that the bug class is
// real and that our fix is what avoids it.
func TestGlobalTimeoutClient_AbortsSlowBody(t *testing.T) {
	srv := slowBodyServer(t, 60*time.Millisecond, 8) // ~480ms body
	defer srv.Close()

	c := &http.Client{Timeout: 150 * time.Millisecond}
	resp, err := c.Get(srv.URL)
	if err != nil {
		return // aborting before headers is also a valid demonstration
	}
	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)
	if err == nil {
		t.Fatal("expected the global-Timeout client to abort the slow body read, but it completed")
	}
}
