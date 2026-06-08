package openai

import "encoding/json"

// Wire types mirror the OpenAI Chat Completions API at /v1/chat/completions.
// They are kept intentionally separate from galdor's shared schema so
// changes to the wire format never leak upward.

type chatRequest struct {
	Model            string          `json:"model"`
	Messages         []wireMessage   `json:"messages"`
	MaxTokens        *int            `json:"max_tokens,omitempty"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	Stop             []string        `json:"stop,omitempty"`
	Stream           bool            `json:"stream,omitempty"`
	StreamOptions    *wireStreamOpts `json:"stream_options,omitempty"`
	Tools            []wireTool      `json:"tools,omitempty"`
	ToolChoice       json.RawMessage `json:"tool_choice,omitempty"`
	ResponseFormat   *wireRespFormat `json:"response_format,omitempty"`
	ReasoningEffort  string          `json:"reasoning_effort,omitempty"`
	User             string          `json:"user,omitempty"`
	ParallelToolCall *bool           `json:"parallel_tool_calls,omitempty"`
}

type wireStreamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

// wireMessage is one entry in the chat history.
//
// Content can be either a plain string (for simple text messages) or an
// array of content parts (for multimodal). json.RawMessage holds whichever
// form the adapter writes, and the response decoder coerces accordingly.
type wireMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []wireToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`

	// ReasoningContent carries the model's reasoning on responses from
	// OpenAI-compatible reasoning models (e.g. DeepSeek-R1). OpenAI's own
	// API hides reasoning, so this is empty there. Request-only on the
	// way out (never sent); surfaced as a thinking part on the way in.
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

// wireContentPart is one entry in the array form of message content.
type wireContentPart struct {
	Type string `json:"type"`

	// type=text
	Text string `json:"text,omitempty"`

	// type=image_url
	ImageURL *wireImageURL `json:"image_url,omitempty"`
}

type wireImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // "low", "high", "auto"
}

type wireTool struct {
	Type     string       `json:"type"` // "function"
	Function wireFuncDecl `json:"function"`
}

type wireFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type wireToolCall struct {
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"` // "function"
	Function wireFuncCall `json:"function"`

	// Index is only set on streaming deltas.
	Index *int `json:"index,omitempty"`
}

type wireFuncCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"` // JSON-encoded string
}

type wireRespFormat struct {
	Type       string          `json:"type"` // "text", "json_object", "json_schema"
	JSONSchema *wireJSONSchema `json:"json_schema,omitempty"`
}

type wireJSONSchema struct {
	Name   string          `json:"name,omitempty"`
	Strict bool            `json:"strict,omitempty"`
	Schema json.RawMessage `json:"schema"`
}

// chatResponse is the body of a successful non-streaming /v1/chat/completions call.
type chatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []wireChoice `json:"choices"`
	Usage   wireUsage    `json:"usage"`
}

type wireChoice struct {
	Index        int         `json:"index"`
	Message      wireMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type wireUsage struct {
	PromptTokens        int               `json:"prompt_tokens"`
	CompletionTokens    int               `json:"completion_tokens"`
	TotalTokens         int               `json:"total_tokens"`
	PromptTokensDetails *wireTokenDetails `json:"prompt_tokens_details,omitempty"`
}

type wireTokenDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// chunk is one streaming chat completion chunk.
type chatChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
	Usage   *wireUsage    `json:"usage,omitempty"`
}

type chunkChoice struct {
	Index        int        `json:"index"`
	Delta        chunkDelta `json:"delta"`
	FinishReason string     `json:"finish_reason,omitempty"`
}

type chunkDelta struct {
	Role             string         `json:"role,omitempty"`
	Content          string         `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []wireToolCall `json:"tool_calls,omitempty"`
}

// errorResponse is the body shape OpenAI returns on 4xx/5xx.
type errorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Param   string `json:"param,omitempty"`
		Message string `json:"message"`
	} `json:"error"`
}
