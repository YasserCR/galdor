package google

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

func TestNormalizeFinishReason(t *testing.T) {
	t.Parallel()
	cases := map[string]schema.StopReason{
		"STOP":               schema.StopReasonEndTurn,
		"MAX_TOKENS":         schema.StopReasonMaxTokens,
		"SAFETY":             schema.StopReasonRefusal,
		"RECITATION":         schema.StopReasonRefusal,
		"BLOCKLIST":          schema.StopReasonRefusal,
		"PROHIBITED_CONTENT": schema.StopReasonRefusal,
		"SPII":               schema.StopReasonRefusal,
		"":                   "",
		"OTHER":              schema.StopReason("other"),
	}
	for in, want := range cases {
		if got := normalizeFinishReason(in); got != want {
			t.Errorf("normalizeFinishReason(%q) = %q, want %q", in, got, want)
		}
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

func TestKindForStatusName(t *testing.T) {
	t.Parallel()
	cases := map[string]error{
		"UNAUTHENTICATED":     provider.ErrAuth,
		"PERMISSION_DENIED":   provider.ErrAuth,
		"RESOURCE_EXHAUSTED":  provider.ErrRateLimited,
		"INVALID_ARGUMENT":    provider.ErrInvalidRequest,
		"FAILED_PRECONDITION": provider.ErrInvalidRequest,
		"NOT_FOUND":           provider.ErrInvalidRequest,
		"INTERNAL":            provider.ErrServer,
		"UNAVAILABLE":         provider.ErrServer,
		"UNKNOWN_SOMETHING":   nil,
	}
	for in, want := range cases {
		if got := kindForStatusName(in); !sentinelEqual(got, want) {
			t.Errorf("kindForStatusName(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestBuildRequest_UnknownRole(t *testing.T) {
	t.Parallel()
	_, err := buildRequest(provider.Request{
		Model:    "x",
		Messages: []schema.Message{{Role: schema.Role("alien")}},
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildRequest_GenerationConfig(t *testing.T) {
	t.Parallel()
	mt := 256
	temp := 0.7
	topP := 0.9
	w, err := buildRequest(provider.Request{
		Model:         "gemini-2.5-flash",
		Messages:      []schema.Message{schema.UserMessage("hi")},
		MaxTokens:     &mt,
		Temperature:   &temp,
		TopP:          &topP,
		StopSequences: []string{"END"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := w.GenerationConfig
	if cfg == nil {
		t.Fatal("GenerationConfig nil")
	}
	if cfg.MaxOutputTokens == nil || *cfg.MaxOutputTokens != mt {
		t.Errorf("MaxOutputTokens = %v", cfg.MaxOutputTokens)
	}
	if cfg.Temperature == nil || *cfg.Temperature != 0.7 {
		t.Errorf("Temperature = %v", cfg.Temperature)
	}
	if cfg.TopP == nil || *cfg.TopP != 0.9 {
		t.Errorf("TopP = %v", cfg.TopP)
	}
	if len(cfg.StopSequences) != 1 || cfg.StopSequences[0] != "END" {
		t.Errorf("StopSequences = %+v", cfg.StopSequences)
	}
}

func TestBuildRequest_ResponseFormatJSONSchema(t *testing.T) {
	t.Parallel()
	w, err := buildRequest(provider.Request{
		Model:    "gemini-2.5-flash",
		Messages: []schema.Message{schema.UserMessage("hi")},
		ResponseFormat: &provider.ResponseFormat{
			Type:   provider.ResponseFormatJSONSchema,
			Schema: json.RawMessage(`{"type":"object"}`),
			Name:   "out",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if w.GenerationConfig.ResponseMIMEType != "application/json" {
		t.Errorf("ResponseMIMEType = %q", w.GenerationConfig.ResponseMIMEType)
	}
	if string(w.GenerationConfig.ResponseSchema) != `{"type":"object"}` {
		t.Errorf("ResponseSchema = %s", w.GenerationConfig.ResponseSchema)
	}
}

func TestBuildRequest_ToolsAttached(t *testing.T) {
	t.Parallel()
	w, err := buildRequest(provider.Request{
		Model:    "gemini-2.5-flash",
		Messages: []schema.Message{schema.UserMessage("hi")},
		Tools: []schema.ToolDef{{
			Name:        "weather",
			Description: "get weather",
			Schema:      json.RawMessage(`{"type":"object"}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Tools) != 1 || len(w.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools = %+v", w.Tools)
	}
	if w.Tools[0].FunctionDeclarations[0].Name != "weather" {
		t.Errorf("name = %+v", w.Tools[0].FunctionDeclarations[0])
	}
}

func TestBuildRequest_ToolResultsFoldIntoUserMessage(t *testing.T) {
	t.Parallel()
	// Build a conversation: user asks → assistant emits a tool call →
	// user (tool role) returns the result. Gemini must see the result as
	// a functionResponse part on a USER content block, and the name must
	// be recovered from the prior assistant ToolCall.
	req := provider.Request{
		Model: "gemini-2.5-flash",
		Messages: []schema.Message{
			schema.UserMessage("what time?"),
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "gfc_0_time", Name: "time", Arguments: json.RawMessage(`{}`)},
				},
			},
			schema.ToolResultMessage("gfc_0_time", "16:32"),
		},
	}
	w, err := buildRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Contents) != 3 {
		t.Fatalf("contents = %d", len(w.Contents))
	}
	last := w.Contents[2]
	if last.Role != "user" {
		t.Errorf("tool result must be on a user content, got role=%q", last.Role)
	}
	if len(last.Parts) != 1 || last.Parts[0].FunctionResponse == nil {
		t.Fatalf("parts = %+v", last.Parts)
	}
	if last.Parts[0].FunctionResponse.Name != "time" {
		t.Errorf("functionResponse.Name = %q, want recovered from prior ToolCall", last.Parts[0].FunctionResponse.Name)
	}
	// Response payload should wrap plain text as {"result": ...}.
	if string(last.Parts[0].FunctionResponse.Response) != `{"result":"16:32"}` {
		t.Errorf("Response = %s", last.Parts[0].FunctionResponse.Response)
	}
}

func TestBuildRequest_ToolResultJSONPassthrough(t *testing.T) {
	t.Parallel()
	req := provider.Request{
		Model: "gemini-2.5-flash",
		Messages: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "gfc_0_weather", Name: "weather", Arguments: json.RawMessage(`{}`)},
				},
			},
			schema.ToolResultMessage("gfc_0_weather", `{"temp":21,"unit":"C"}`),
		},
	}
	w, err := buildRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	resp := w.Contents[1].Parts[0].FunctionResponse.Response
	if string(resp) != `{"temp":21,"unit":"C"}` {
		t.Errorf("Response passthrough failed: %s", resp)
	}
}

func TestImageToWire_Base64(t *testing.T) {
	t.Parallel()
	blob, err := imageToWire(&schema.ImageContent{Data: []byte{0x89, 0x50, 0x4e, 0x47}, Media: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	if blob.MimeType != "image/png" || blob.Data == "" {
		t.Errorf("blob = %+v", blob)
	}
}

func TestImageToWire_URLRejected(t *testing.T) {
	t.Parallel()
	_, err := imageToWire(&schema.ImageContent{URL: "https://example.com/x.png"})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest (Gemini doesn't accept inline URLs)", err)
	}
}

func TestImageToWire_MissingMedia(t *testing.T) {
	t.Parallel()
	_, err := imageToWire(&schema.ImageContent{Data: []byte{0x89}})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestToolConfigFromChoice(t *testing.T) {
	t.Parallel()
	cases := map[provider.ToolChoice]string{
		provider.ToolChoiceAuto:     "AUTO",
		provider.ToolChoiceNone:     "NONE",
		provider.ToolChoiceRequired: "ANY",
	}
	for in, want := range cases {
		c := toolConfigFromChoice(in)
		if c == nil || c.FunctionCallingConfig == nil || c.FunctionCallingConfig.Mode != want {
			t.Errorf("toolConfigFromChoice(%q) = %+v, want mode=%q", in, c, want)
		}
	}
	if toolConfigFromChoice(provider.ToolChoice("")) != nil {
		t.Error("empty ToolChoice should map to nil")
	}
}

func TestToolResponseJSON(t *testing.T) {
	t.Parallel()
	// Already JSON object: pass through.
	if got := string(toolResponseJSON(`{"a":1}`)); got != `{"a":1}` {
		t.Errorf("passthrough: %s", got)
	}
	// Plain text: wrap in {"result": ...}.
	if got := string(toolResponseJSON(`hello`)); got != `{"result":"hello"}` {
		t.Errorf("wrap: %s", got)
	}
}

func TestSynthToolID_IncludesName(t *testing.T) {
	t.Parallel()
	if id := synthToolID("weather", 2); id != "gfc_2_weather" {
		t.Errorf("synthToolID = %q", id)
	}
}

func TestResponseFromWire_ThoughtPartsExcluded(t *testing.T) {
	t.Parallel()
	r := &generateResponse{
		Candidates: []wireCandidate{{
			Content: wireContent{Role: "model", Parts: []wirePart{
				{Text: "internal reasoning", Thought: true},
				{Text: "visible answer"},
			}},
			FinishReason: "STOP",
		}},
		UsageMetadata: wireUsage{PromptTokenCount: 10, CandidatesTokenCount: 5, ThoughtsTokenCount: 3},
	}
	resp := responseFromWire(r, nil)
	if resp.Message.Text() != "visible answer" {
		t.Errorf("Thought part should not bleed into Message.Text: %q", resp.Message.Text())
	}
	// Thought tokens are still counted in OutputTokens for cost reporting.
	if resp.Usage.OutputTokens != 8 {
		t.Errorf("OutputTokens = %d, want 8 (candidates+thoughts)", resp.Usage.OutputTokens)
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

// TestBuildGenerationConfig_Reasoning verifies Request.Reasoning maps to
// Gemini's thinkingConfig (includeThoughts + optional budget), and that
// leaving it off keeps thinkingConfig nil — identical to prior behavior.
func TestBuildGenerationConfig_Reasoning(t *testing.T) {
	t.Parallel()

	// Off by default: no thinkingConfig, nil generationConfig.
	if cfg := buildGenerationConfig(provider.Request{}); cfg != nil {
		t.Errorf("no-reasoning request produced cfg %+v, want nil", cfg)
	}

	// Enabled without a budget: includeThoughts true, budget unset.
	cfg := buildGenerationConfig(provider.Request{
		Reasoning: &provider.ReasoningConfig{Enabled: true},
	})
	if cfg == nil || cfg.ThinkingConfig == nil {
		t.Fatalf("reasoning request produced %+v, want thinkingConfig", cfg)
	}
	if !cfg.ThinkingConfig.IncludeThoughts {
		t.Error("IncludeThoughts = false, want true")
	}
	if cfg.ThinkingConfig.ThinkingBudget != nil {
		t.Errorf("ThinkingBudget = %v, want nil (provider default)", *cfg.ThinkingConfig.ThinkingBudget)
	}

	// Enabled with a budget: budget forwarded.
	cfg = buildGenerationConfig(provider.Request{
		Reasoning: &provider.ReasoningConfig{Enabled: true, Budget: 2048},
	})
	if cfg.ThinkingConfig.ThinkingBudget == nil || *cfg.ThinkingConfig.ThinkingBudget != 2048 {
		t.Errorf("ThinkingBudget = %v, want 2048", cfg.ThinkingConfig.ThinkingBudget)
	}

	// Enabled:false is treated as off.
	if cfg := buildGenerationConfig(provider.Request{
		Reasoning: &provider.ReasoningConfig{Enabled: false},
	}); cfg != nil {
		t.Errorf("disabled reasoning produced cfg %+v, want nil", cfg)
	}
}

// TestResponseFromWire_SurfacesThought verifies a thought part becomes a
// thinking part while the answer text stays clean.
func TestResponseFromWire_SurfacesThought(t *testing.T) {
	t.Parallel()
	r := &generateResponse{
		Candidates: []wireCandidate{{
			Content: wireContent{Parts: []wirePart{
				{Text: "step-by-step reasoning", Thought: true},
				{Text: "the final answer"},
			}},
			FinishReason: "STOP",
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
	if len(thinks) != 1 || thinks[0] != "step-by-step reasoning" {
		t.Errorf("thinking parts = %v, want [step-by-step reasoning]", thinks)
	}
}
