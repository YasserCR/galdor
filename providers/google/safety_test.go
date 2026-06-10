package google

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

// Regression for audit M5: a prompt blocked by Gemini's safety filter
// returns HTTP 200 with no candidates and a blockReason. Generate must
// surface an error, not an empty (apparently successful) response.
func TestGenerate_SafetyBlockedPromptErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"promptFeedback":{"blockReason":"SAFETY"}}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Generate(context.Background(), provider.Request{
		Model: "gemini-x", Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if err == nil {
		t.Fatal("a safety-blocked prompt must error (regression of M5)")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "block") {
		t.Errorf("error should mention the block, got: %v", err)
	}
}
