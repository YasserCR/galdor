package bedrock

import (
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// Regression for audit M11: Bedrock's Converse has no "none" tool choice,
// so the model may emit tool_use even when ToolChoiceNone was requested.
// The contract (ToolChoiceNone -> no tool calls) is honored by stripping
// them from the response.
func TestEnforceToolChoiceNone(t *testing.T) {
	withCalls := func() *provider.Response {
		return &provider.Response{Message: schema.Message{
			ToolCalls: []schema.ToolCall{{ID: "1", Name: "x"}},
		}}
	}
	r := withCalls()
	enforceToolChoiceNone(r, provider.ToolChoiceNone)
	if len(r.Message.ToolCalls) != 0 {
		t.Fatalf("ToolChoiceNone must strip tool calls (regression of M11), got %d", len(r.Message.ToolCalls))
	}
	r2 := withCalls()
	enforceToolChoiceNone(r2, provider.ToolChoiceAuto)
	if len(r2.Message.ToolCalls) != 1 {
		t.Fatalf("a non-None choice must keep tool calls, got %d", len(r2.Message.ToolCalls))
	}
}
