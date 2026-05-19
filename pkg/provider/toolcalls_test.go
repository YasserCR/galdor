package provider_test

import (
	"errors"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

func TestValidateToolCalls_OK(t *testing.T) {
	t.Parallel()
	msg := schema.Message{
		Role: schema.RoleAssistant,
		ToolCalls: []schema.ToolCall{
			{ID: "a", Name: "weather", Arguments: []byte(`{"city":"Quito"}`)},
			{ID: "b", Name: "search", Arguments: []byte(`{}`)},
			{ID: "c", Name: "ping"}, // empty args is allowed
		},
	}
	if err := provider.ValidateToolCalls(msg); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateToolCalls_NoCalls(t *testing.T) {
	t.Parallel()
	msg := schema.Message{Role: schema.RoleAssistant, Content: []schema.ContentPart{schema.TextPart("hi")}}
	if err := provider.ValidateToolCalls(msg); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateToolCalls_EmptyID(t *testing.T) {
	t.Parallel()
	msg := schema.Message{
		Role:      schema.RoleAssistant,
		ToolCalls: []schema.ToolCall{{Name: "weather", Arguments: []byte(`{}`)}},
	}
	if err := provider.ValidateToolCalls(msg); !errors.Is(err, provider.ErrToolCallInvariant) {
		t.Fatalf("err = %v, want ErrToolCallInvariant", err)
	}
}

func TestValidateToolCalls_EmptyName(t *testing.T) {
	t.Parallel()
	msg := schema.Message{
		Role:      schema.RoleAssistant,
		ToolCalls: []schema.ToolCall{{ID: "a", Arguments: []byte(`{}`)}},
	}
	if err := provider.ValidateToolCalls(msg); !errors.Is(err, provider.ErrToolCallInvariant) {
		t.Fatalf("err = %v, want ErrToolCallInvariant", err)
	}
}

func TestValidateToolCalls_InvalidJSON(t *testing.T) {
	t.Parallel()
	msg := schema.Message{
		Role:      schema.RoleAssistant,
		ToolCalls: []schema.ToolCall{{ID: "a", Name: "weather", Arguments: []byte(`{"city":`)}},
	}
	if err := provider.ValidateToolCalls(msg); !errors.Is(err, provider.ErrToolCallInvariant) {
		t.Fatalf("err = %v, want ErrToolCallInvariant", err)
	}
}
