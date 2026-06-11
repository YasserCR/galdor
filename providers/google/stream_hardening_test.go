package google

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// Regression for audit H6 (Google half): an error frame streamed
// mid-response must surface as an error, not be silently ignored (ending
// the stream with a synthesized, apparently-successful MessageStop and a
// truncated answer). The fix was applied to OpenAI but originally missed
// Google.
func TestStream_SurfacesInStreamError(t *testing.T) {
	t.Parallel()
	body := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]},"index":0}],"modelVersion":"gemini-2.5-flash-001"}`,
		"",
		`data: {"error":{"code":503,"message":"upstream exploded","status":"UNAVAILABLE"}}`,
		"",
		"",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := provider.CollectStream(context.Background(), mustStream(t, p, provider.Request{
		Model:    "gemini-2.5-flash",
		Messages: []schema.Message{schema.UserMessage("hi")},
	}))
	if err == nil {
		t.Fatal("an in-stream error frame must surface as an error (regression of H6)")
	}
	var ae *provider.APIError
	if !errors.As(err, &ae) || !strings.Contains(ae.Message, "upstream exploded") {
		t.Fatalf("expected an APIError carrying the message, got %v", err)
	}
	// 503/UNAVAILABLE must classify as a server error, reusing the HTTP
	// error path's classification.
	if !errors.Is(err, provider.ErrServer) {
		t.Errorf("expected ErrServer classification, got %v", err)
	}
}

// Regression for audit M5 (streaming parity): a prompt blocked by the
// safety filter arrives mid-stream as a frame with no candidates and a
// blockReason. Generate already fails on this; Stream must too, rather
// than terminating as if the (empty) response succeeded.
func TestStream_SurfacesSafetyBlock(t *testing.T) {
	t.Parallel()
	body := strings.Join([]string{
		`data: {"promptFeedback":{"blockReason":"SAFETY"},"modelVersion":"gemini-2.5-flash-001"}`,
		"",
		"",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := provider.CollectStream(context.Background(), mustStream(t, p, provider.Request{
		Model:    "gemini-2.5-flash",
		Messages: []schema.Message{schema.UserMessage("hi")},
	}))
	if err == nil {
		t.Fatal("a blocked prompt must surface as an error on the stream path (M5 parity)")
	}
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("expected ErrInvalidRequest, got %v", err)
	}
	if !strings.Contains(err.Error(), "SAFETY") {
		t.Errorf("error should name the block reason, got %v", err)
	}
}
