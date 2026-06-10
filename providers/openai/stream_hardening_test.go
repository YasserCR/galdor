package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

func sseServer(t *testing.T, frames ...string) *httptest.Server {
	t.Helper()
	body := strings.Join(append(frames, "", ""), "\n")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
}

// Regression (audit low): a per-call ctx cancellation must unblock a Recv
// that is stuck waiting on a slow/idle connection, not just be noticed
// between frames. The server sends one frame then hangs forever; cancelling
// the ctx mid-read must return promptly with the cancellation error.
func TestStream_RecvHonorsCtxMidRead(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, `data: {"id":"c1","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`+"\n\n")
		if fl != nil {
			fl.Flush()
		}
		// Hang until the test is done (or the request ctx is cancelled),
		// so the next Recv blocks on an idle connection.
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	p := newTestProvider(t, srv)
	sr := mustStream(t, p, provider.Request{
		Model: "gpt-4o-mini", Messages: []schema.Message{schema.UserMessage("hi")},
	})
	defer func() { _ = sr.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	// Drain until we block, then cancel from another goroutine.
	go func() {
		// Give the first (buffered) event time to be consumed, then cancel.
		for i := 0; i < 1000; i++ {
			_ = i
		}
		cancel()
	}()

	done := make(chan error, 1)
	go func() {
		for {
			_, err := sr.Recv(ctx)
			if err != nil {
				done <- err
				return
			}
		}
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Recv returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Recv did not return after ctx cancellation — blocked read ignored the per-call ctx")
	}
}

// Regression for audit H6: an error frame streamed mid-response must
// surface as an error, not be silently ignored (ending the stream with a
// synthesized, apparently-successful MessageStop and a truncated answer).
func TestStream_SurfacesInStreamError(t *testing.T) {
	srv := sseServer(t,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}`,
		"",
		`data: {"error":{"message":"upstream exploded","type":"server_error"}}`,
	)
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := provider.CollectStream(context.Background(), mustStream(t, p, provider.Request{
		Model: "gpt-4o-mini", Messages: []schema.Message{schema.UserMessage("hi")},
	}))
	if err == nil {
		t.Fatal("an in-stream error frame must surface as an error (regression of H6)")
	}
	var ae *provider.APIError
	if !errors.As(err, &ae) || !strings.Contains(ae.Message, "upstream exploded") {
		t.Fatalf("expected an APIError carrying the message, got %v", err)
	}
}

// Regression for audit M10: a tool-call delta whose id the provider
// omitted (common with OpenAI-compatible backends) must get a synthesized
// id so it isn't dropped by the stream-collection layer.
func TestStream_SynthesizesMissingToolCallID(t *testing.T) {
	srv := sseServer(t,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"get_weather","arguments":"{}"}}]},"finish_reason":null}]}`,
		"",
		`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		"data: [DONE]",
	)
	defer srv.Close()

	p := newTestProvider(t, srv)
	resp, err := provider.CollectStream(context.Background(), mustStream(t, p, provider.Request{
		Model: "gpt-4o-mini", Messages: []schema.Message{schema.UserMessage("weather?")},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("a tool call with no id must not be dropped (regression of M10), got %d calls", len(resp.Message.ToolCalls))
	}
	if resp.Message.ToolCalls[0].ID == "" {
		t.Error("tool-call id must be synthesized when the provider omits it (M10)")
	}
	if resp.Message.ToolCalls[0].Name != "get_weather" {
		t.Errorf("name = %q", resp.Message.ToolCalls[0].Name)
	}
}
