package provider

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/YasserCR/galdor/pkg/schema"
)

// Tool-calling contract.
//
// Every Provider adapter (Anthropic, OpenAI, Google, Bedrock, ...) MUST
// honor the following invariants so that callers in pkg/agent or anywhere
// else can swap providers without changing tool-handling code:
//
//  1. ToolDef in, schema.ToolCall out. Adapters translate the provider's
//     native tool definition format from a schema.ToolDef and translate
//     the native tool-use response into schema.ToolCall values inside
//     Response.Message.ToolCalls.
//
//  2. Each ToolCall has a non-empty ID and Name. The ID is whatever the
//     provider returned, or a stable synthetic identifier when the
//     provider does not assign one (currently Google). Arguments is a
//     JSON document — possibly empty (len 0) when the tool takes no
//     arguments, otherwise a syntactically valid JSON object.
//
//  3. Order is preserved. When the model emits parallel tool calls in a
//     single assistant turn, ToolCalls keeps the wire order.
//
//  4. ToolChoice maps:
//     - ToolChoiceAuto    → model decides (the default)
//     - ToolChoiceNone    → no tool calls produced; Response.Message.ToolCalls is empty
//     - ToolChoiceRequired→ at least one tool call produced (when tools are present)
//
//  5. When ToolCalls is non-empty, StopReason is typically StopReasonToolUse.
//     Adapters do not synthesize StopReasonToolUse when the model did not
//     actually request tools.
//
// ValidateToolCalls below enforces (2) and is a useful invariant check
// for adapter tests and for cross-provider contract suites.

// ErrToolCallInvariant is returned by ValidateToolCalls when a message
// violates the cross-provider tool-call contract.
var ErrToolCallInvariant = errors.New("provider: tool call invariant violated")

// ValidateToolCalls checks that every ToolCall in msg satisfies the
// cross-provider contract documented above: non-empty ID, non-empty Name,
// and Arguments that is either empty or a syntactically valid JSON value.
//
// It returns nil when msg has no tool calls. Callers can use this as a
// sanity check after Generate or after assembling a stream.
func ValidateToolCalls(msg schema.Message) error {
	for i, tc := range msg.ToolCalls {
		if tc.ID == "" {
			return fmt.Errorf("%w: tool_calls[%d] has empty ID", ErrToolCallInvariant, i)
		}
		if tc.Name == "" {
			return fmt.Errorf("%w: tool_calls[%d] (id=%q) has empty Name", ErrToolCallInvariant, i, tc.ID)
		}
		if len(tc.Arguments) == 0 {
			continue
		}
		if !json.Valid(tc.Arguments) {
			return fmt.Errorf("%w: tool_calls[%d] (name=%q) Arguments is not valid JSON", ErrToolCallInvariant, i, tc.Name)
		}
	}
	return nil
}
