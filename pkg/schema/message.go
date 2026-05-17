package schema

import "strings"

// Message is a single entry in a conversation, normalized across providers.
//
// Content carries the body as a slice of parts to support multimodal
// inputs. Single-text messages can use the convenience helpers
// (SystemMessage, UserMessage, AssistantMessage, ToolResultMessage).
type Message struct {
	Role    Role          `json:"role"`
	Content []ContentPart `json:"content"`

	// Name is an optional participant name. Some providers (OpenAI) accept
	// it on user and tool messages; others ignore it.
	Name string `json:"name,omitempty"`

	// ToolCalls is populated on assistant messages when the model requests
	// one or more tool invocations.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// ToolCallID is set on tool-role messages and references the ToolCall.ID
	// being responded to.
	ToolCallID string `json:"tool_call_id,omitempty"`

	// CacheControl is a hint to the provider that the prefix up to and
	// including this message may be cached. Providers that do not support
	// caching ignore the field.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// Text returns the concatenated text of all text parts in the message.
// Non-text parts are skipped. Useful for logging and simple consumers.
func (m Message) Text() string {
	var b strings.Builder
	for _, p := range m.Content {
		if p.Type == ContentTypeText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// SystemMessage returns a system-role message with a single text part.
func SystemMessage(text string) Message {
	return Message{Role: RoleSystem, Content: []ContentPart{TextPart(text)}}
}

// UserMessage returns a user-role message with a single text part.
func UserMessage(text string) Message {
	return Message{Role: RoleUser, Content: []ContentPart{TextPart(text)}}
}

// AssistantMessage returns an assistant-role message with a single text part.
func AssistantMessage(text string) Message {
	return Message{Role: RoleAssistant, Content: []ContentPart{TextPart(text)}}
}

// ToolResultMessage returns a tool-role message carrying the textual result
// of executing a tool call. callID must match the ToolCall.ID issued by the
// assistant.
func ToolResultMessage(callID, result string) Message {
	return Message{
		Role:       RoleTool,
		Content:    []ContentPart{TextPart(result)},
		ToolCallID: callID,
	}
}
