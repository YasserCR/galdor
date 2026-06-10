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

// Regression (audit low): a redacted_thinking block must round-trip. On the
// way out the opaque blob (carried in Signature) is echoed as a
// redacted_thinking wire block; on the way in it is preserved rather than
// dropped, so a Reasoning+tools loop with redacted reasoning can continue.
func TestRedactedThinking_RoundTrips(t *testing.T) {
	// Outbound: schema part -> wire block.
	m := schema.Message{
		Role: schema.RoleAssistant,
		Content: []schema.ContentPart{
			{Type: schema.ContentTypeRedactedThinking, Signature: "ENCRYPTED_BLOB"},
			schema.TextPart("answer"),
		},
	}
	wm, err := assistantMessageToWire(m)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(wm.Content) != 2 || wm.Content[0].Type != "redacted_thinking" {
		t.Fatalf("want [redacted_thinking, text], got %+v", wm.Content)
	}
	if wm.Content[0].Data != "ENCRYPTED_BLOB" {
		t.Errorf("redacted blob = %q, want it echoed in Data", wm.Content[0].Data)
	}

	// Inbound: wire response -> schema part preserved.
	resp := responseFromWire(&messageResponse{
		Content: []wireContentBlock{
			{Type: "redacted_thinking", Data: "ENCRYPTED_BLOB"},
			{Type: "text", Text: "answer"},
		},
	}, nil)
	var got *schema.ContentPart
	for i := range resp.Message.Content {
		if resp.Message.Content[i].Type == schema.ContentTypeRedactedThinking {
			got = &resp.Message.Content[i]
		}
	}
	if got == nil {
		t.Fatal("redacted_thinking block was dropped on parse")
	}
	if got.Signature != "ENCRYPTED_BLOB" {
		t.Errorf("preserved blob = %q, want ENCRYPTED_BLOB", got.Signature)
	}
	// It must not leak into Text().
	if resp.Message.Text() != "answer" {
		t.Errorf("Text() = %q, want just the answer (redacted reasoning excluded)", resp.Message.Text())
	}
}

// Regression (audit low): a tool_use with empty arguments must still emit
// `input: {}` — Anthropic requires the field, and omitempty would drop it.
func TestAssistantMessageToWire_EmptyToolArgsEmitsEmptyObject(t *testing.T) {
	m := schema.Message{
		Role:      schema.RoleAssistant,
		ToolCalls: []schema.ToolCall{{ID: "tu_1", Name: "now"}}, // nil Arguments
	}
	wm, err := assistantMessageToWire(m)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(wm.Content) != 1 || wm.Content[0].Type != "tool_use" {
		t.Fatalf("want one tool_use block, got %+v", wm.Content)
	}
	if string(wm.Content[0].Input) != "{}" {
		t.Errorf("Input = %q, want %q (Anthropic requires input present)", string(wm.Content[0].Input), "{}")
	}
}

// Regression (audit low): a CacheControl hint on an assistant message with
// tool calls must land on the LAST block (the final tool_use), so the tool
// calls are inside the cached prefix — not on the last text block before them.
func TestAssistantMessageToWire_CacheControlIncludesToolUse(t *testing.T) {
	m := schema.Message{
		Role:         schema.RoleAssistant,
		Content:      []schema.ContentPart{schema.TextPart("calling tools")},
		CacheControl: &schema.CacheControl{},
		ToolCalls: []schema.ToolCall{
			{ID: "tu_1", Name: "a", Arguments: []byte(`{}`)},
			{ID: "tu_2", Name: "b", Arguments: []byte(`{}`)},
		},
	}
	wm, err := assistantMessageToWire(m)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	last := wm.Content[len(wm.Content)-1]
	if last.Type != "tool_use" || last.CacheControl == nil {
		t.Errorf("cache_control must be on the last tool_use block; last = %+v", last)
	}
	// The earlier text block must NOT carry the breakpoint.
	if wm.Content[0].CacheControl != nil {
		t.Error("cache_control must not be on the text block before the tool calls")
	}
}
