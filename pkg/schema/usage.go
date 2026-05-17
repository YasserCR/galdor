package schema

// Usage describes token consumption for a single Response.
//
// Fields are populated only when the provider reports them. Zero values
// mean "not reported" rather than "zero tokens"; consumers must not infer
// otherwise.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`

	// CacheCreationTokens counts input tokens written to the provider's
	// prompt cache during this request. Reported by providers that support
	// caching (Anthropic, OpenAI, Google).
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`

	// CacheReadTokens counts input tokens served from the cache.
	CacheReadTokens int `json:"cache_read_tokens,omitempty"`
}

// Total returns the sum of input and output tokens. Cache tokens are
// already a subset of input tokens and are not added separately.
func (u Usage) Total() int { return u.InputTokens + u.OutputTokens }

// StopReason describes why generation stopped, normalized across providers.
type StopReason string

const (
	// StopReasonEndTurn means the model produced a complete reply and
	// chose to stop on its own.
	StopReasonEndTurn StopReason = "end_turn"

	// StopReasonMaxTokens means generation hit the requested or model-side
	// token limit.
	StopReasonMaxTokens StopReason = "max_tokens"

	// StopReasonToolUse means the model emitted one or more tool calls
	// and is waiting for results to continue.
	StopReasonToolUse StopReason = "tool_use"

	// StopReasonStopSequence means a configured stop sequence was emitted.
	StopReasonStopSequence StopReason = "stop_sequence"

	// StopReasonRefusal means the provider refused to produce content for
	// safety or policy reasons.
	StopReasonRefusal StopReason = "refusal"

	// StopReasonError means generation failed mid-stream. The accompanying
	// Response error carries the details.
	StopReasonError StopReason = "error"
)
