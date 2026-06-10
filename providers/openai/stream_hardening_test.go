package openai

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

func sseServer(t *testing.T, frames ...string) *httptest.Server {
	t.Helper()
	body := strings.Join(append(frames, "", ""), "\n")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
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
