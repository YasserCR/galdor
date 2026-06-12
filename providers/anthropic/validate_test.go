package anthropic

import (
	"context"
	"errors"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// Anthropic now supports structured output (json_schema via a forced
// tool), so ValidateRequest must NOT reject a ResponseFormat request with
// ErrUnsupported. (The end-to-end translation is covered in
// structured_test.go; here we only assert the capability gate lets it
// through — the call fails later on the fake key, not on validation.)
func TestGenerate_AcceptsStructuredOutput(t *testing.T) {
	p, err := New(Config{APIKey: "test-key", BaseURL: "http://127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Generate(context.Background(), provider.Request{
		Model:    "claude-haiku-4-5",
		Messages: []schema.Message{schema.UserMessage("hi")},
		ResponseFormat: &provider.ResponseFormat{
			Type:   provider.ResponseFormatJSONSchema,
			Schema: []byte(`{"type":"object"}`),
			Name:   "x",
		},
	})
	if errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("structured output must not be rejected as unsupported anymore, got %v", err)
	}
}
