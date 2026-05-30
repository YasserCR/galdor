package provider_test

import (
	"errors"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

func TestCapabilities_ValidateRequest_OK(t *testing.T) {
	t.Parallel()
	caps := provider.Capabilities{
		Streaming: true, ToolCalling: true, StructuredOutput: true,
		VisionInput: true, PromptCaching: true,
	}
	req := provider.Request{
		Model:    "x",
		Messages: []schema.Message{schema.UserMessage("hi")},
		Tools:    []schema.ToolDef{{Name: "t", Description: "d", Schema: []byte(`{}`)}},
	}
	if err := caps.ValidateRequest(req); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestCapabilities_ValidateRequest_ToolsWithoutSupport(t *testing.T) {
	t.Parallel()
	caps := provider.Capabilities{ToolCalling: false}
	req := provider.Request{Tools: []schema.ToolDef{{Name: "t"}}}
	err := caps.ValidateRequest(req)
	if !errors.Is(err, provider.ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}

func TestCapabilities_ValidateRequest_ResponseFormatWithoutSupport(t *testing.T) {
	t.Parallel()
	caps := provider.Capabilities{}
	req := provider.Request{ResponseFormat: &provider.ResponseFormat{Type: provider.ResponseFormatJSONObject}}
	err := caps.ValidateRequest(req)
	if !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestCapabilities_ValidateRequest_VisionWithoutSupport(t *testing.T) {
	t.Parallel()
	caps := provider.Capabilities{}
	req := provider.Request{Messages: []schema.Message{
		{Role: schema.RoleUser, Content: []schema.ContentPart{
			{Type: schema.ContentTypeImage, Image: &schema.ImageContent{URL: "https://x.png"}},
		}},
	}}
	err := caps.ValidateRequest(req)
	if !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestCapabilities_ValidateRequest_VisionWithSupport(t *testing.T) {
	t.Parallel()
	caps := provider.Capabilities{VisionInput: true}
	req := provider.Request{Messages: []schema.Message{
		{Role: schema.RoleUser, Content: []schema.ContentPart{
			{Type: schema.ContentTypeImage, Image: &schema.ImageContent{URL: "https://x.png"}},
		}},
	}}
	if err := caps.ValidateRequest(req); err != nil {
		t.Errorf("vision with VisionInput=true should pass: %v", err)
	}
}

func TestCapabilities_ValidateRequest_CacheControlWithoutSupport(t *testing.T) {
	t.Parallel()
	caps := provider.Capabilities{}
	req := provider.Request{Messages: []schema.Message{
		{Role: schema.RoleUser, Content: []schema.ContentPart{schema.TextPart("hi")},
			CacheControl: schema.EphemeralCache()},
	}}
	err := caps.ValidateRequest(req)
	if !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestCapabilities_ValidateRequest_CacheControlWithSupport(t *testing.T) {
	t.Parallel()
	caps := provider.Capabilities{PromptCaching: true}
	req := provider.Request{Messages: []schema.Message{
		{Role: schema.RoleUser, Content: []schema.ContentPart{schema.TextPart("hi")},
			CacheControl: schema.EphemeralCache()},
	}}
	if err := caps.ValidateRequest(req); err != nil {
		t.Errorf("CacheControl with PromptCaching=true should pass: %v", err)
	}
}

func TestCapabilities_ValidateRequest_EmptyIsAlwaysOK(t *testing.T) {
	t.Parallel()
	caps := provider.Capabilities{}
	req := provider.Request{Messages: []schema.Message{schema.UserMessage("hi")}}
	if err := caps.ValidateRequest(req); err != nil {
		t.Errorf("plain text request should pass any capability set: %v", err)
	}
}
