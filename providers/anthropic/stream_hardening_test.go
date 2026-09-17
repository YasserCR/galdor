package anthropic

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// A 2xx whose body is not a stream — a plain JSON reply from an endpoint
// that ignored stream:true, an error envelope, an HTML page — used to read
// as an empty stream, and the consumer took it for an empty answer with
// nothing to retry and the reply that was in the body lost.
func TestStream_ANonStreamBodyIsAnError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"error":{"message":"stream not supported here"}}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := provider.CollectStream(context.Background(), mustStream(t, p,
		provider.Request{Model: "claude-sonnet-5", Messages: []schema.Message{schema.UserMessage("hola")}}))
	if err == nil {
		t.Fatal("a body with no stream was read as an empty answer")
	}
	if !strings.Contains(err.Error(), "stream not supported here") {
		t.Errorf("the error does not carry the body: %v", err)
	}
}
