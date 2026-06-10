package anthropic

import (
	"testing"

	"github.com/YasserCR/galdor/pkg/schema"
)

// Regression for audit H7: a signed thinking block must be echoed back in
// the assistant turn so a Reasoning+tools loop can continue (Anthropic
// rejects the follow-up otherwise). Unsigned reasoning is still skipped.
func TestAssistantMessageToWire_RoundTripsSignedThinking(t *testing.T) {
	m := schema.Message{
		Role: schema.RoleAssistant,
		Content: []schema.ContentPart{
			{Type: schema.ContentTypeThinking, Text: "let me think", Signature: "sig-xyz"},
			schema.TextPart("calling a tool"),
		},
		ToolCalls: []schema.ToolCall{
			{ID: "tu_1", Name: "weather", Arguments: []byte(`{"city":"Quito"}`)},
		},
	}

	wm, err := assistantMessageToWire(m)
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	// Expect order: thinking, text, tool_use — the order Anthropic
	// requires for extended thinking with tool use.
	if len(wm.Content) != 3 {
		t.Fatalf("want 3 blocks (thinking,text,tool_use), got %d: %+v", len(wm.Content), wm.Content)
	}
	if wm.Content[0].Type != "thinking" {
		t.Fatalf("first block must be thinking, got %q (regression of H7)", wm.Content[0].Type)
	}
	if wm.Content[0].Thinking != "let me think" || wm.Content[0].Signature != "sig-xyz" {
		t.Errorf("thinking block = %+v", wm.Content[0])
	}
	if wm.Content[1].Type != "text" {
		t.Errorf("second block = %q, want text", wm.Content[1].Type)
	}
	if wm.Content[2].Type != "tool_use" {
		t.Errorf("third block = %q, want tool_use", wm.Content[2].Type)
	}
}

// An unsigned thinking part must NOT be resent (Anthropic rejects a
// thinking block without a valid signature).
func TestAssistantMessageToWire_SkipsUnsignedThinking(t *testing.T) {
	m := schema.Message{
		Role: schema.RoleAssistant,
		Content: []schema.ContentPart{
			{Type: schema.ContentTypeThinking, Text: "no signature here"},
			schema.TextPart("answer"),
		},
	}
	wm, err := assistantMessageToWire(m)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, b := range wm.Content {
		if b.Type == "thinking" {
			t.Fatalf("unsigned thinking must be skipped, got %+v", b)
		}
	}
	if len(wm.Content) != 1 || wm.Content[0].Type != "text" {
		t.Errorf("want a single text block, got %+v", wm.Content)
	}
}
