package openai

import (
	"net/http"
	"testing"
)

// Regression for audit C1: the default client must not impose a global
// http.Client.Timeout (it would cap body reads and kill long streams).
func TestStreamSafeHTTPClient_NoGlobalTimeout(t *testing.T) {
	c := streamSafeHTTPClient(defaultResponseHeaderTimeout)
	if c.Timeout != 0 {
		t.Fatalf("default client must have no global Timeout; got %v", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.Transport)
	}
	if tr.ResponseHeaderTimeout != defaultResponseHeaderTimeout {
		t.Fatalf("ResponseHeaderTimeout = %v, want %v", tr.ResponseHeaderTimeout, defaultResponseHeaderTimeout)
	}
}
