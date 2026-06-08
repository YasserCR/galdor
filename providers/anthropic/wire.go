package anthropic

import "encoding/json"

// Wire types mirror the Anthropic Messages API request and response shapes
// at /v1/messages. They are kept intentionally separate from galdor's
// shared schema so changes to the wire format never leak upward.

type messageRequest struct {
	Model         string            `json:"model"`
	Messages      []wireMessage     `json:"messages"`
	System        []wireSystemBlock `json:"system,omitempty"`
	MaxTokens     int               `json:"max_tokens"`
	Temperature   *float64          `json:"temperature,omitempty"`
	TopP          *float64          `json:"top_p,omitempty"`
	StopSequences []string          `json:"stop_sequences,omitempty"`
	Stream        bool              `json:"stream,omitempty"`
	Tools         []wireTool        `json:"tools,omitempty"`
	ToolChoice    *wireToolChoice   `json:"tool_choice,omitempty"`
	Thinking      *wireThinking     `json:"thinking,omitempty"`
	Metadata      *wireMetadata     `json:"metadata,omitempty"`
}

// wireThinking enables Anthropic extended thinking. Type is "enabled";
// BudgetTokens caps the reasoning tokens and must be >= 1024 and less
// than max_tokens.
type wireThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type wireSystemBlock struct {
	Type         string            `json:"type"`
	Text         string            `json:"text"`
	CacheControl *wireCacheControl `json:"cache_control,omitempty"`
}

type wireMessage struct {
	Role    string             `json:"role"`
	Content []wireContentBlock `json:"content"`
}

// wireContentBlock encodes all variants of a content block. Only the fields
// relevant to a given Type are populated.
type wireContentBlock struct {
	Type string `json:"type"`

	// type=text
	Text string `json:"text,omitempty"`

	// type=image
	Source *wireImageSource `json:"source,omitempty"`

	// type=tool_use (assistant -> caller)
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// type=thinking (assistant -> caller): the reasoning text plus an
	// opaque signature that Anthropic requires echoed back to continue.
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// type=tool_result (caller -> assistant)
	ToolUseID string             `json:"tool_use_id,omitempty"`
	Content   []wireContentBlock `json:"content,omitempty"`
	IsError   bool               `json:"is_error,omitempty"`

	CacheControl *wireCacheControl `json:"cache_control,omitempty"`
}

type wireImageSource struct {
	Type      string `json:"type"`                 // "base64" or "url"
	MediaType string `json:"media_type,omitempty"` // when Type == "base64"
	Data      string `json:"data,omitempty"`       // base64 payload
	URL       string `json:"url,omitempty"`        // when Type == "url"
}

type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type wireToolChoice struct {
	Type string `json:"type"` // "auto", "any", "none", "tool"
	Name string `json:"name,omitempty"`
}

type wireMetadata struct {
	UserID string `json:"user_id,omitempty"`
}

type wireCacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// messageResponse is the body of a successful /v1/messages call (non-stream).
type messageResponse struct {
	ID           string             `json:"id"`
	Type         string             `json:"type"`
	Role         string             `json:"role"`
	Model        string             `json:"model"`
	Content      []wireContentBlock `json:"content"`
	StopReason   string             `json:"stop_reason"`
	StopSequence string             `json:"stop_sequence,omitempty"`
	Usage        wireUsage          `json:"usage"`
}

type wireUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// errorResponse is the body shape Anthropic returns on 4xx/5xx.
type errorResponse struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
