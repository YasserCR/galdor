package anthropic

import (
	"errors"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

func TestNormalizeStopReason(t *testing.T) {
	t.Parallel()
	cases := map[string]schema.StopReason{
		"end_turn":      schema.StopReasonEndTurn,
		"max_tokens":    schema.StopReasonMaxTokens,
		"tool_use":      schema.StopReasonToolUse,
		"stop_sequence": schema.StopReasonStopSequence,
		"refusal":       schema.StopReasonRefusal,
		"":              schema.StopReason(""),
		"unknown_kind":  schema.StopReason("unknown_kind"),
	}
	for in, want := range cases {
		if got := normalizeStopReason(in); got != want {
			t.Errorf("normalizeStopReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestImageToWire_URL(t *testing.T) {
	t.Parallel()
	src, err := imageToWire(&schema.ImageContent{URL: "https://example.com/x.png"})
	if err != nil {
		t.Fatal(err)
	}
	if src.Type != "url" || src.URL != "https://example.com/x.png" {
		t.Errorf("src = %+v", src)
	}
}

func TestImageToWire_MissingMediaType(t *testing.T) {
	t.Parallel()
	_, err := imageToWire(&schema.ImageContent{Data: []byte{0x89}})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestImageToWire_Empty(t *testing.T) {
	t.Parallel()
	_, err := imageToWire(&schema.ImageContent{})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildRequest_UnknownRole(t *testing.T) {
	t.Parallel()
	_, err := buildRequest(provider.Request{
		Model:    "x",
		Messages: []schema.Message{{Role: schema.Role("alien")}},
	}, false)
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildRequest_MaxTokensRespected(t *testing.T) {
	t.Parallel()
	mt := 4096
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
	if w.MaxTokens != mt || !w.Stream {
		t.Errorf("MaxTokens / Stream wrong: %+v", w)
	}
	if w.Temperature == nil || *w.Temperature != 0.7 {
		t.Errorf("Temperature = %v", w.Temperature)
	}
	if w.TopP == nil || *w.TopP != 0.9 {
		t.Errorf("TopP = %v", w.TopP)
	}
	if len(w.StopSequences) != 1 || w.StopSequences[0] != "END" {
		t.Errorf("StopSequences = %+v", w.StopSequences)
	}
	if w.Metadata == nil || w.Metadata.UserID != "alice" {
		t.Errorf("Metadata = %+v", w.Metadata)
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
	if len(w.Tools) != 1 || w.Tools[0].Name != "weather" {
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
	cases := map[string]error{
		"authentication_error":  provider.ErrAuth,
		"permission_error":      provider.ErrAuth,
		"rate_limit_error":      provider.ErrRateLimited,
		"overloaded_error":      provider.ErrRateLimited,
		"invalid_request_error": provider.ErrInvalidRequest,
		"not_found_error":       provider.ErrInvalidRequest,
		"api_error":             provider.ErrServer,
		"unknown_x":             nil,
	}
	for in, want := range cases {
		if got := kindForType(in); !sentinelEqual(got, want) {
			t.Errorf("kindForType(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestCacheControl_Roundtrip(t *testing.T) {
	t.Parallel()
	if cacheControl(nil) != nil {
		t.Error("nil hint must map to nil")
	}
	w := cacheControl(schema.EphemeralCache())
	if w == nil || w.Type != schema.CacheTypeEphemeral {
		t.Errorf("CacheControl mapping wrong: %+v", w)
	}
}

func TestPartsToWire_AttachesCacheControlToLastBlock(t *testing.T) {
	t.Parallel()
	parts := []schema.ContentPart{
		schema.TextPart("a"),
		schema.TextPart("b"),
	}
	blocks, err := partsToWire(parts, schema.EphemeralCache())
	if err != nil {
		t.Fatal(err)
	}
	if blocks[0].CacheControl != nil {
		t.Errorf("first block should not carry cache_control: %+v", blocks[0])
	}
	if blocks[1].CacheControl == nil || blocks[1].CacheControl.Type != schema.CacheTypeEphemeral {
		t.Errorf("last block must carry cache_control: %+v", blocks[1])
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
	}, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(out) != 1 || out[0].Text != "answer" {
		t.Fatalf("got %+v, want single text part %q", out, "answer")
	}
}

// TestBuildRequest_Reasoning verifies Request.Reasoning maps to
// Anthropic's thinking config with its constraints (budget floor,
// max_tokens room, temperature/top_p dropped), and that off = unchanged.
func TestBuildRequest_Reasoning(t *testing.T) {
	t.Parallel()
	temp := 0.7
	topP := 0.9

	// Off: no thinking, sampling params preserved.
	off, err := buildRequest(provider.Request{Model: "m", Temperature: &temp, TopP: &topP}, false)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if off.Thinking != nil {
		t.Errorf("Thinking = %+v, want nil", off.Thinking)
	}
	if off.Temperature == nil || off.TopP == nil {
		t.Error("sampling params should be preserved when reasoning is off")
	}

	// On, no budget: floor to 1024, max_tokens grown, temp/top_p dropped.
	mt := 512
	on, err := buildRequest(provider.Request{
		Model:       "m",
		MaxTokens:   &mt,
		Temperature: &temp,
		TopP:        &topP,
		Reasoning:   &provider.ReasoningConfig{Enabled: true},
	}, false)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if on.Thinking == nil || on.Thinking.Type != "enabled" || on.Thinking.BudgetTokens != 1024 {
		t.Fatalf("Thinking = %+v, want enabled/1024", on.Thinking)
	}
	if on.MaxTokens <= on.Thinking.BudgetTokens {
		t.Errorf("MaxTokens %d must exceed budget %d", on.MaxTokens, on.Thinking.BudgetTokens)
	}
	if on.Temperature != nil || on.TopP != nil {
		t.Error("temperature/top_p must be dropped when thinking is enabled")
	}

	// On, explicit budget honored.
	on2, _ := buildRequest(provider.Request{
		Model:     "m",
		Reasoning: &provider.ReasoningConfig{Enabled: true, Budget: 8000},
	}, false)
	if on2.Thinking.BudgetTokens != 8000 {
		t.Errorf("BudgetTokens = %d, want 8000", on2.Thinking.BudgetTokens)
	}
}

// TestResponseFromWire_SurfacesThinking verifies a thinking block becomes
// a thinking part (with signature) while the answer text stays clean.
func TestResponseFromWire_SurfacesThinking(t *testing.T) {
	t.Parallel()
	r := &messageResponse{
		Role:  "assistant",
		Model: "claude",
		Content: []wireContentBlock{
			{Type: "thinking", Thinking: "let me reason", Signature: "sig-abc"},
			{Type: "text", Text: "the answer"},
		},
		StopReason: "end_turn",
	}
	resp := responseFromWire(r, nil)
	if got := resp.Message.Text(); got != "the answer" {
		t.Errorf("Text() = %q, want %q", got, "the answer")
	}
	var found *schema.ContentPart
	for i := range resp.Message.Content {
		if resp.Message.Content[i].Type == schema.ContentTypeThinking {
			found = &resp.Message.Content[i]
		}
	}
	if found == nil {
		t.Fatal("no thinking part surfaced")
	}
	if found.Text != "let me reason" || found.Signature != "sig-abc" {
		t.Errorf("thinking part = %+v, want text/signature preserved", found)
	}
}
