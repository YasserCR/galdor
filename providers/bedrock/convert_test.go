package bedrock

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

func TestNormalizeStopReason(t *testing.T) {
	t.Parallel()
	cases := map[string]schema.StopReason{
		"end_turn":             schema.StopReasonEndTurn,
		"max_tokens":           schema.StopReasonMaxTokens,
		"tool_use":             schema.StopReasonToolUse,
		"stop_sequence":        schema.StopReasonStopSequence,
		"guardrail_intervened": schema.StopReasonRefusal,
		"content_filtered":     schema.StopReasonRefusal,
		"":                     "",
		"unknown":              schema.StopReason("unknown"),
	}
	for in, want := range cases {
		if got := normalizeStopReason(in); got != want {
			t.Errorf("normalizeStopReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildConverseInput_SystemHoisted(t *testing.T) {
	t.Parallel()
	in, err := buildConverseInput(provider.Request{
		Model: "anthropic.claude-3-7-sonnet-20250219-v1:0",
		Messages: []schema.Message{
			schema.SystemMessage("be terse"),
			schema.UserMessage("hi"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(in.System) != 1 {
		t.Fatalf("system blocks = %d", len(in.System))
	}
	sys, ok := in.System[0].(*brtypes.SystemContentBlockMemberText)
	if !ok || sys.Value != "be terse" {
		t.Errorf("system text wrong: %T %+v", in.System[0], in.System[0])
	}
	if len(in.Messages) != 1 || in.Messages[0].Role != brtypes.ConversationRoleUser {
		t.Errorf("messages = %+v", in.Messages)
	}
}

func TestBuildConverseInput_ToolResultFoldsIntoUser(t *testing.T) {
	t.Parallel()
	in, err := buildConverseInput(provider.Request{
		Model: "x",
		Messages: []schema.Message{
			schema.UserMessage("what time?"),
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "tu_1", Name: "time", Arguments: json.RawMessage(`{}`)},
				},
			},
			schema.ToolResultMessage("tu_1", "16:32"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Messages) != 3 {
		t.Fatalf("messages = %d", len(in.Messages))
	}
	last := in.Messages[2]
	if last.Role != brtypes.ConversationRoleUser {
		t.Errorf("tool result must live on a user message, got %q", last.Role)
	}
	if len(last.Content) != 1 {
		t.Fatalf("content blocks = %d", len(last.Content))
	}
	tr, ok := last.Content[0].(*brtypes.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("wrong block type: %T", last.Content[0])
	}
	if aws.ToString(tr.Value.ToolUseId) != "tu_1" {
		t.Errorf("ToolUseId = %q", aws.ToString(tr.Value.ToolUseId))
	}
}

func TestBuildConverseInput_AssistantToolCalls(t *testing.T) {
	t.Parallel()
	in, err := buildConverseInput(provider.Request{
		Model: "x",
		Messages: []schema.Message{{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{
				{ID: "tu_1", Name: "weather", Arguments: json.RawMessage(`{"city":"Quito"}`)},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Messages) != 1 || in.Messages[0].Role != brtypes.ConversationRoleAssistant {
		t.Fatalf("messages = %+v", in.Messages)
	}
	if len(in.Messages[0].Content) != 1 {
		t.Fatalf("content = %+v", in.Messages[0].Content)
	}
	use, ok := in.Messages[0].Content[0].(*brtypes.ContentBlockMemberToolUse)
	if !ok {
		t.Fatalf("wrong block type: %T", in.Messages[0].Content[0])
	}
	if aws.ToString(use.Value.ToolUseId) != "tu_1" || aws.ToString(use.Value.Name) != "weather" {
		t.Errorf("toolUse = %+v", use.Value)
	}
}

func TestBuildConverseInput_InvalidToolArgs(t *testing.T) {
	t.Parallel()
	_, err := buildConverseInput(provider.Request{
		Model: "x",
		Messages: []schema.Message{{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{
				{ID: "tu", Name: "x", Arguments: json.RawMessage(`{not-json`)},
			},
		}},
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
}

func TestBuildConverseInput_UnknownRole(t *testing.T) {
	t.Parallel()
	_, err := buildConverseInput(provider.Request{
		Model:    "x",
		Messages: []schema.Message{{Role: schema.Role("alien")}},
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildConverseInput_EmptyModel(t *testing.T) {
	t.Parallel()
	_, err := buildConverseInput(provider.Request{
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildConverseInput_InferenceConfig(t *testing.T) {
	t.Parallel()
	mt := 256
	temp := 0.7
	topP := 0.9
	in, err := buildConverseInput(provider.Request{
		Model:         "x",
		Messages:      []schema.Message{schema.UserMessage("hi")},
		MaxTokens:     &mt,
		Temperature:   &temp,
		TopP:          &topP,
		StopSequences: []string{"END"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := in.InferenceConfig
	if cfg == nil {
		t.Fatal("InferenceConfig nil")
	}
	if cfg.MaxTokens == nil || *cfg.MaxTokens != 256 {
		t.Errorf("MaxTokens = %v", cfg.MaxTokens)
	}
	if cfg.Temperature == nil || *cfg.Temperature != float32(0.7) {
		t.Errorf("Temperature = %v", cfg.Temperature)
	}
	if cfg.TopP == nil || *cfg.TopP != float32(0.9) {
		t.Errorf("TopP = %v", cfg.TopP)
	}
	if len(cfg.StopSequences) != 1 || cfg.StopSequences[0] != "END" {
		t.Errorf("StopSequences = %+v", cfg.StopSequences)
	}
}

func TestBuildConverseInput_InferenceConfigOmittedWhenUnset(t *testing.T) {
	t.Parallel()
	in, err := buildConverseInput(provider.Request{
		Model:    "x",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if in.InferenceConfig != nil {
		t.Errorf("InferenceConfig should be nil when no params are set; got %+v", in.InferenceConfig)
	}
}

func TestBuildConverseInput_ToolsAttached(t *testing.T) {
	t.Parallel()
	in, err := buildConverseInput(provider.Request{
		Model:    "x",
		Messages: []schema.Message{schema.UserMessage("hi")},
		Tools: []schema.ToolDef{{
			Name:        "weather",
			Description: "get the weather",
			Schema:      json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		}},
		ToolChoice: provider.ToolChoiceAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	if in.ToolConfig == nil || len(in.ToolConfig.Tools) != 1 {
		t.Fatalf("ToolConfig = %+v", in.ToolConfig)
	}
	spec, ok := in.ToolConfig.Tools[0].(*brtypes.ToolMemberToolSpec)
	if !ok {
		t.Fatalf("wrong tool type: %T", in.ToolConfig.Tools[0])
	}
	if aws.ToString(spec.Value.Name) != "weather" {
		t.Errorf("name = %q", aws.ToString(spec.Value.Name))
	}
	if _, ok := in.ToolConfig.ToolChoice.(*brtypes.ToolChoiceMemberAuto); !ok {
		t.Errorf("ToolChoice = %T", in.ToolConfig.ToolChoice)
	}
}

func TestBuildConverseInput_ToolChoiceRequired(t *testing.T) {
	t.Parallel()
	in, err := buildConverseInput(provider.Request{
		Model:    "x",
		Messages: []schema.Message{schema.UserMessage("hi")},
		Tools: []schema.ToolDef{{
			Name: "t", Schema: json.RawMessage(`{"type":"object"}`),
		}},
		ToolChoice: provider.ToolChoiceRequired,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := in.ToolConfig.ToolChoice.(*brtypes.ToolChoiceMemberAny); !ok {
		t.Errorf("ToolChoice = %T, want MemberAny", in.ToolConfig.ToolChoice)
	}
}

func TestBuildConverseInput_ToolChoiceNoneKeepsToolDefs(t *testing.T) {
	t.Parallel()
	in, err := buildConverseInput(provider.Request{
		Model:    "x",
		Messages: []schema.Message{schema.UserMessage("hi")},
		Tools: []schema.ToolDef{{
			Name: "t", Schema: json.RawMessage(`{"type":"object"}`),
		}},
		ToolChoice: provider.ToolChoiceNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Bedrock has no "none" choice. The tool *definitions* must stay
	// declared (so a follow-up turn with prior tool_result blocks still
	// validates), but no ToolChoice is forced.
	if in.ToolConfig == nil || len(in.ToolConfig.Tools) != 1 {
		t.Fatalf("ToolChoiceNone should keep tool defs; got %+v", in.ToolConfig)
	}
	if in.ToolConfig.ToolChoice != nil {
		t.Errorf("ToolChoiceNone should leave ToolChoice unset; got %T", in.ToolConfig.ToolChoice)
	}
}

func TestBuildConverseInput_ForwardsUserIDMetadata(t *testing.T) {
	t.Parallel()
	in, err := buildConverseInput(provider.Request{
		Model:    "x",
		Messages: []schema.Message{schema.UserMessage("hi")},
		Metadata: map[string]string{"user_id": "u-123", "other": "ignored"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if in.RequestMetadata["user_id"] != "u-123" {
		t.Errorf("RequestMetadata[user_id] = %q, want u-123", in.RequestMetadata["user_id"])
	}
	if _, ok := in.RequestMetadata["other"]; ok {
		t.Error("only user_id should be forwarded; 'other' leaked into RequestMetadata")
	}
}

func TestImageFormatFromMIME(t *testing.T) {
	t.Parallel()
	cases := map[string]brtypes.ImageFormat{
		"image/png":  brtypes.ImageFormatPng,
		"image/jpeg": brtypes.ImageFormatJpeg,
		"image/jpg":  brtypes.ImageFormatJpeg,
		"image/gif":  brtypes.ImageFormatGif,
		"image/webp": brtypes.ImageFormatWebp,
	}
	for mime, want := range cases {
		got, err := imageFormatFromMIME(mime)
		if err != nil || got != want {
			t.Errorf("imageFormatFromMIME(%q) = %v, %v; want %v, nil", mime, got, err, want)
		}
	}
	if _, err := imageFormatFromMIME("image/svg+xml"); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("unsupported MIME should map to ErrInvalidRequest, got %v", err)
	}
}

func TestPartsToBlocks_URLImageRejected(t *testing.T) {
	t.Parallel()
	_, err := partsToBlocks([]schema.ContentPart{
		schema.ImagePartURL("https://example.com/x.png"),
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
}

func TestPartsToBlocks_InlineImageEncoded(t *testing.T) {
	t.Parallel()
	blocks, err := partsToBlocks([]schema.ContentPart{
		schema.ImagePartData([]byte{0x89, 0x50, 0x4e, 0x47}, "image/png"),
	})
	if err != nil {
		t.Fatal(err)
	}
	img, ok := blocks[0].(*brtypes.ContentBlockMemberImage)
	if !ok {
		t.Fatalf("wrong block type: %T", blocks[0])
	}
	if img.Value.Format != brtypes.ImageFormatPng {
		t.Errorf("Format = %v", img.Value.Format)
	}
}

func TestKindForSmithyCode(t *testing.T) {
	t.Parallel()
	cases := map[string]error{
		"ValidationException":           provider.ErrInvalidRequest,
		"BadRequestException":           provider.ErrInvalidRequest,
		"AccessDeniedException":         provider.ErrAuth,
		"ExpiredTokenException":         provider.ErrAuth,
		"ThrottlingException":           provider.ErrRateLimited,
		"ServiceQuotaExceededException": provider.ErrRateLimited,
		"InternalServerException":       provider.ErrServer,
		"ModelStreamErrorException":     provider.ErrServer,
		"WeirdNewException":             provider.ErrServer, // safe fallback
	}
	for code, want := range cases {
		got := kindForSmithyCode(code)
		if !sentinelEqual(got, want) {
			t.Errorf("kindForSmithyCode(%q) = %v, want %v", code, got, want)
		}
	}
}

// sentinelEqual compares two sentinel-style errors that the
// classification helpers return. Both nil compares equal; otherwise
// errors.Is is used so future wrapping doesn't break callers.
func sentinelEqual(got, want error) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return errors.Is(got, want)
}

func TestSafeStr(t *testing.T) {
	t.Parallel()
	if safeStr(nil) != "" {
		t.Error("nil should give empty")
	}
	s := "x"
	if safeStr(&s) != "x" {
		t.Error("pointer deref failed")
	}
}

func TestNormalizeAWSError_NilPassthrough(t *testing.T) {
	t.Parallel()
	if got := normalizeAWSError(nil); got != nil {
		t.Errorf("nil err should pass through, got %v", got)
	}
}

func TestNormalizeAWSError_TypedExceptions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   error
		want error
	}{
		{"validation", &brtypes.ValidationException{Message: strPtr("bad input")}, provider.ErrInvalidRequest},
		{"access", &brtypes.AccessDeniedException{Message: strPtr("forbidden")}, provider.ErrAuth},
		{"throttle", &brtypes.ThrottlingException{Message: strPtr("slow down")}, provider.ErrRateLimited},
		{"quota", &brtypes.ServiceQuotaExceededException{Message: strPtr("quota")}, provider.ErrRateLimited},
		{"internal", &brtypes.InternalServerException{Message: strPtr("boom")}, provider.ErrServer},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := normalizeAWSError(c.in)
			if !errors.Is(err, c.want) {
				t.Errorf("err = %v, want %v", err, c.want)
			}
			var apiErr *provider.APIError
			if !errors.As(err, &apiErr) {
				t.Fatal("err not *APIError")
			}
			if apiErr.Provider != "bedrock" {
				t.Errorf("Provider = %q", apiErr.Provider)
			}
		})
	}
}

func TestNormalizeAWSError_ExtractsRetryAfter(t *testing.T) {
	t.Parallel()
	// A throttling error carrying an HTTP Retry-After header must surface
	// it on the APIError so the retry wrapper honors the server backoff.
	in := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": {"7"}},
		}},
		Err: &brtypes.ThrottlingException{Message: strPtr("slow down")},
	}
	err := normalizeAWSError(in)
	if !errors.Is(err, provider.ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("err not *APIError")
	}
	if apiErr.RetryAfter != 7 {
		t.Errorf("RetryAfter = %d, want 7", apiErr.RetryAfter)
	}
}

func TestNormalizeAWSError_PlainErrorWrapped(t *testing.T) {
	t.Parallel()
	// Plain errors (anything not matching a typed Bedrock exception or
	// smithy.APIError) get wrapped into *APIError with ErrServer as a
	// safe default. context.Canceled / DeadlineExceeded have their own
	// path tested separately in stream_test.go.
	raw := errors.New("transport gave up")
	out := normalizeAWSError(raw)
	if out == nil {
		t.Fatal("expected non-nil")
	}
	var apiErr *provider.APIError
	if !errors.As(out, &apiErr) {
		t.Fatalf("wrapped err not *APIError: %v", out)
	}
	if apiErr.Provider != "bedrock" {
		t.Errorf("Provider = %q", apiErr.Provider)
	}
}

func TestEncodeToolInput_Nil(t *testing.T) {
	t.Parallel()
	out, err := encodeToolInput(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "{}" {
		t.Errorf("nil doc should encode to '{}', got %s", out)
	}
}

// encodeToolInput's non-nil path is exercised by the SDK-decoded
// Generate tests below (TestGenerate_ToolCallsInResponse), which
// produce documents on the response decode path.

func strPtr(s string) *string { return &s }
