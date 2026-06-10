package providerset

import (
	"net/http"
	"testing"
	"time"
)

// Regression for audit C1: when LLM_HTTP_TIMEOUT is set, providerset must
// map it to the transport's ResponseHeaderTimeout, NOT a global
// http.Client.Timeout that would cap the response body and kill streams.
func TestStreamSafeHTTPClient_MapsToHeaderTimeout(t *testing.T) {
	const d = 25 * time.Second
	c := streamSafeHTTPClient(d)
	if c.Timeout != 0 {
		t.Fatalf("client must have no global Timeout; got %v", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.Transport)
	}
	if tr.ResponseHeaderTimeout != d {
		t.Fatalf("ResponseHeaderTimeout = %v, want %v", tr.ResponseHeaderTimeout, d)
	}
}
