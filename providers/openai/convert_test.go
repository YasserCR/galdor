package openai

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

func TestRoleToWire_Mapping(t *testing.T) {
	t.Parallel()
	cases := map[schema.Role]string{
		schema.RoleSystem:    "system",
		schema.RoleUser:      "user",
		schema.RoleAssistant: "assistant",
		schema.RoleTool:      "tool",
	}
	for in, want := range cases {
		got, err := roleToWire(in)
		if err != nil || got != want {
			t.Errorf("roleToWire(%q) = %q,%v; want %q,nil", in, got, err, want)
		}
	}
	if _, err := roleToWire(schema.Role("alien")); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("unknown role should map to ErrInvalidRequest, got %v", err)
	}
}

func TestNormalizeFinishReason(t *testing.T) {
	t.Parallel()
	cases := map[string]schema.StopReason{
		"stop":           schema.StopReasonEndTurn,
		"length":         schema.StopReasonMaxTokens,
		"tool_calls":     schema.StopReasonToolUse,
		"function_call":  schema.StopReasonToolUse,
		"content_filter": schema.StopReasonRefusal,
		"":               "",
		"weird_value":    schema.StopReason("weird_value"),
	}
	for in, want := range cases {
		if got := normalizeFinishReason(in); got != want {
			t.Errorf("normalizeFinishReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMessageToWire_TextOnlyUsesStringContent(t *testing.T) {
	t.Parallel()
	wm, err := messageToWire(schema.UserMessage("hello"))
	if err != nil {
		t.Fatal(err)
	}
	var s string
	if err := json.Unmarshal(wm.Content, &s); err != nil {
		t.Fatalf("content should be a JSON string: %s", wm.Content)
	}
	if s != "hello" {
		t.Errorf("decoded content = %q", s)
	}
}

func TestMessageToWire_MultimodalUsesArrayContent(t *testing.T) {
	t.Parallel()
	m := schema.Message{
		Role: schema.RoleUser,
		Content: []schema.ContentPart{
			schema.TextPart("look"),
			schema.ImagePartURL("https://example.com/x.png"),
		},
	}
	wm, err := messageToWire(m)
	if err != nil {
		t.Fatal(err)
	}
	var parts []wireContentPart
	if err := json.Unmarshal(wm.Content, &parts); err != nil {
		t.Fatalf("expected array content, got: %s", wm.Content)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "look" {
		t.Errorf("first = %+v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil || parts[1].ImageURL.URL != "https://example.com/x.png" {
		t.Errorf("second = %+v", parts[1])
	}
}

func TestMessageToWire_AssistantToolCallsOnly(t *testing.T) {
	t.Parallel()
	m := schema.Message{
		Role: schema.RoleAssistant,
		ToolCalls: []schema.ToolCall{
			{ID: "call_1", Name: "weather", Arguments: json.RawMessage(`{"city":"Quito"}`)},
		},
	}
	wm, err := messageToWire(m)
	if err != nil {
		t.Fatal(err)
	}
	if wm.Content != nil {
		t.Errorf("Content should be nil for tool-call-only messages, got %s", wm.Content)
	}
	if len(wm.ToolCalls) != 1 || wm.ToolCalls[0].ID != "call_1" {
		t.Errorf("ToolCalls = %+v", wm.ToolCalls)
	}
	if wm.ToolCalls[0].Function.Arguments != `{"city":"Quito"}` {
		t.Errorf("Arguments encoding wrong: %q", wm.ToolCalls[0].Function.Arguments)
	}
}

func TestImageToURL_InlineBase64(t *testing.T) {
	t.Parallel()
	got, err := imageToURL(&schema.ImageContent{Data: []byte{0x89, 0x50}, Media: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Errorf("expected data URL, got %q", got)
	}
}

func TestImageToURL_MissingMedia(t *testing.T) {
	t.Parallel()
	_, err := imageToURL(&schema.ImageContent{Data: []byte{0x89}})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestImageToURL_Empty(t *testing.T) {
	t.Parallel()
	_, err := imageToURL(&schema.ImageContent{})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestToolChoiceToWire(t *testing.T) {
	t.Parallel()
	cases := map[provider.ToolChoice]string{
		provider.ToolChoiceAuto:     `"auto"`,
		provider.ToolChoiceNone:     `"none"`,
		provider.ToolChoiceRequired: `"required"`,
	}
	for in, want := range cases {
		got := toolChoiceToWire(in)
		if string(got) != want {
			t.Errorf("toolChoiceToWire(%q) = %s, want %s", in, got, want)
		}
	}
	if toolChoiceToWire(provider.ToolChoice("")) != nil {
		t.Error("empty ToolChoice should map to nil")
	}
}

func TestResponseFormatToWire(t *testing.T) {
	t.Parallel()
	if responseFormatToWire(nil) != nil {
		t.Error("nil ResponseFormat should map to nil")
	}
	w := responseFormatToWire(&provider.ResponseFormat{Type: provider.ResponseFormatJSONObject})
	if w == nil || w.Type != "json_object" {
		t.Errorf("json_object mapping wrong: %+v", w)
	}
	w = responseFormatToWire(&provider.ResponseFormat{
		Type:   provider.ResponseFormatJSONSchema,
		Schema: json.RawMessage(`{"type":"object"}`),
		Name:   "myschema",
	})
	if w == nil || w.Type != "json_schema" || w.JSONSchema == nil || w.JSONSchema.Name != "myschema" || !w.JSONSchema.Strict {
		t.Errorf("json_schema mapping wrong: %+v", w)
	}
}

func TestBuildRequest_ParametersForwarded(t *testing.T) {
	t.Parallel()
	mt := 256
	temp := 0.7
	topP := 0.9
	w, err := buildRequest(provider.Request{
		Model:         "x",
		Messages:      []schema.Message{schema.UserMessage("hi")},
		MaxTokens:     &mt,
		Temperature:   &temp,
		TopP:          &topP,
		StopSequences: []string{"END"},
		Metadata:      map[string]string{"user_id": "alice"},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if w.MaxTokens == nil || *w.MaxTokens != mt {
		t.Errorf("MaxTokens = %v", w.MaxTokens)
	}
	if !w.Stream {
		t.Error("Stream must be true")
	}
	if w.StreamOptions == nil || !w.StreamOptions.IncludeUsage {
		t.Errorf("StreamOptions = %+v", w.StreamOptions)
	}
	if w.User != "alice" {
		t.Errorf("User = %q", w.User)
	}
	if w.Stop[0] != "END" {
		t.Errorf("Stop = %+v", w.Stop)
	}
}

func TestBuildRequest_ToolsAttached(t *testing.T) {
	t.Parallel()
	w, err := buildRequest(provider.Request{
		Model:    "x",
		Messages: []schema.Message{schema.UserMessage("hi")},
		Tools: []schema.ToolDef{{
			Name:        "weather",
			Description: "get weather",
			Schema:      []byte(`{"type":"object"}`),
		}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Tools) != 1 || w.Tools[0].Function.Name != "weather" || w.Tools[0].Type != "function" {
		t.Errorf("tools = %+v", w.Tools)
	}
}

func TestKindForStatus(t *testing.T) {
	t.Parallel()
	cases := map[int]error{
		401: provider.ErrAuth,
		403: provider.ErrAuth,
		429: provider.ErrRateLimited,
		400: provider.ErrInvalidRequest,
		404: provider.ErrInvalidRequest,
		500: provider.ErrServer,
		503: provider.ErrServer,
		200: nil,
	}
	for code, want := range cases {
		if got := kindForStatus(code); !sentinelEqual(got, want) {
			t.Errorf("kindForStatus(%d) = %v, want %v", code, got, want)
		}
	}
}

// sentinelEqual compares classifier helpers' return values (a known
// sentinel from pkg/provider or nil). errorlint flags == on errors, but
// these are not wrapped — so we explicitly opt into errors.Is with a
// nil-safe shim.
func sentinelEqual(got, want error) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return errors.Is(got, want)
}

func TestKindForType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		t, c string
		want error
	}{
		{"invalid_request_error", "", provider.ErrInvalidRequest},
		{"invalid_request_error", "context_length_exceeded", provider.ErrContextWindow},
		{"authentication_error", "", provider.ErrAuth},
		{"permission_error", "", provider.ErrAuth},
		{"rate_limit_error", "", provider.ErrRateLimited},
		{"server_error", "", provider.ErrServer},
		{"", "context_length_exceeded", provider.ErrContextWindow},
		{"", "rate_limit_exceeded", provider.ErrRateLimited},
		{"", "invalid_api_key", provider.ErrAuth},
		{"unknown", "unknown", nil},
	}
	for _, c := range cases {
		if got := kindForType(c.t, c.c); !sentinelEqual(got, c.want) {
			t.Errorf("kindForType(%q,%q) = %v, want %v", c.t, c.c, got, c.want)
		}
	}
}

func TestDecodeContent_StringForm(t *testing.T) {
	t.Parallel()
	s, err := decodeContent(json.RawMessage(`"hello"`))
	if err != nil || s != "hello" {
		t.Errorf("decodeContent string form failed: %q, %v", s, err)
	}
}

func TestDecodeContent_ArrayForm(t *testing.T) {
	t.Parallel()
	s, err := decodeContent(json.RawMessage(`[{"type":"text","text":"a"},{"type":"text","text":"b"}]`))
	if err != nil || s != "ab" {
		t.Errorf("decodeContent array form failed: %q, %v", s, err)
	}
}

func TestDecodeContent_Empty(t *testing.T) {
	t.Parallel()
	s, err := decodeContent(nil)
	if err != nil || s != "" {
		t.Errorf("decodeContent(nil) = %q, %v", s, err)
	}
}

// TestPartsToWire_SkipsThinking guarantees a captured reasoning part on
// an assistant turn can be fed back into a request without error: it is
// skipped, not rejected.
func TestPartsToWire_SkipsThinking(t *testing.T) {
	t.Parallel()
	out, err := partsToWire([]schema.ContentPart{
		schema.ThinkingPart("internal reasoning"),
		schema.TextPart("answer"),
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(out) != 1 || out[0].Text != "answer" {
		t.Fatalf("got %+v, want single text part %q", out, "answer")
	}
}

// TestBuildRequest_Reasoning verifies Request.Reasoning maps to OpenAI's
// reasoning_effort (defaulting to medium), and off = no field.
func TestBuildRequest_Reasoning(t *testing.T) {
	t.Parallel()

	off, err := buildRequest(provider.Request{Model: "m"}, false)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if off.ReasoningEffort != "" {
		t.Errorf("ReasoningEffort = %q, want empty", off.ReasoningEffort)
	}

	def, _ := buildRequest(provider.Request{
		Model:     "m",
		Reasoning: &provider.ReasoningConfig{Enabled: true},
	}, false)
	if def.ReasoningEffort != "medium" {
		t.Errorf("ReasoningEffort = %q, want medium (default)", def.ReasoningEffort)
	}

	hi, _ := buildRequest(provider.Request{
		Model:     "m",
		Reasoning: &provider.ReasoningConfig{Enabled: true, Effort: provider.ReasoningEffortHigh},
	}, false)
	if hi.ReasoningEffort != "high" {
		t.Errorf("ReasoningEffort = %q, want high", hi.ReasoningEffort)
	}
}

// TestResponseFromWire_SurfacesReasoningContent verifies reasoning_content
// becomes a thinking part while the answer text stays clean.
func TestResponseFromWire_SurfacesReasoningContent(t *testing.T) {
	t.Parallel()
	r := &chatResponse{
		Model: "deepseek-reasoner",
		Choices: []wireChoice{{
			Message: wireMessage{
				Role:             "assistant",
				Content:          json.RawMessage(`"the final answer"`),
				ReasoningContent: "chain of thought",
			},
			FinishReason: "stop",
		}},
	}
	resp := responseFromWire(r, nil)
	if got := resp.Message.Text(); got != "the final answer" {
		t.Errorf("Text() = %q, want %q", got, "the final answer")
	}
	var thinks []string
	for _, p := range resp.Message.Content {
		if p.Type == schema.ContentTypeThinking {
			thinks = append(thinks, p.Text)
		}
	}
	if len(thinks) != 1 || thinks[0] != "chain of thought" {
		t.Errorf("thinking = %v, want [chain of thought]", thinks)
	}
}
