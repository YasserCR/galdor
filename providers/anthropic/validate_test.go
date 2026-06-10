package anthropic

import (
	"context"
	"errors"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// Regression for audit M7: Capabilities.ValidateRequest was never called,
// so a request asking for a feature the provider doesn't support (here,
// structured output, which Anthropic reports as unsupported) was silently
// served as free-form text. Generate must now reject it. The error is
// returned before any network call.
func TestGenerate_RejectsUnsupportedResponseFormat(t *testing.T) {
	p, err := New(Config{APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Generate(context.Background(), provider.Request{
		Model:          "claude-haiku-4-5",
		Messages:       []schema.Message{schema.UserMessage("hi")},
		ResponseFormat: &provider.ResponseFormat{},
	})
	if !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("Generate must reject ResponseFormat when StructuredOutput is unsupported (regression of M7), got %v", err)
	}
}
