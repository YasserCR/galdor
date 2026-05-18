package provider

import "github.com/YasserCR/galdor/pkg/schema"

// Request is the provider-agnostic input for a generation call.
//
// Optional numeric parameters (Temperature, TopP, MaxTokens) are pointers
// so a caller can distinguish "unset, use provider default" from "explicit
// zero". Adapters must treat nil as "do not send".
type Request struct {
	// Model identifies the target model. Adapters validate it against
	// their known list and return ErrInvalidRequest on a mismatch.
	Model string

	// Messages is the ordered conversation history. Some providers carry
	// the system prompt out-of-band; adapters extract it from the first
	// RoleSystem message when needed.
	Messages []schema.Message

	// Tools is the set of tools the model may invoke. Ignored when the
	// provider does not advertise ToolCalling.
	Tools []schema.ToolDef

	// ToolChoice constrains how the model may use tools.
	ToolChoice ToolChoice

	// Sampling parameters.
	Temperature *float64
	TopP        *float64
	MaxTokens   *int

	// StopSequences are strings that, when emitted, terminate generation.
	StopSequences []string

	// ResponseFormat requests a structured output shape. nil means free-form
	// text. Honored when the provider advertises StructuredOutput.
	ResponseFormat *ResponseFormat

	// Metadata carries opaque key/value pairs forwarded to the provider
	// (e.g., user IDs for abuse tracking). Keys with no provider mapping
	// are ignored.
	Metadata map[string]string
}

// ToolChoice constrains the model's freedom to call tools.
type ToolChoice string

const (
	// ToolChoiceAuto lets the model decide. This is the default.
	ToolChoiceAuto ToolChoice = "auto"

	// ToolChoiceNone forbids tool calls; only text output is allowed.
	ToolChoiceNone ToolChoice = "none"

	// ToolChoiceRequired forces the model to call at least one tool.
	ToolChoiceRequired ToolChoice = "required"
)

// ResponseFormat describes a desired output shape.
//
// When Type is ResponseFormatJSONSchema, Schema must be a JSON Schema
// document describing the required output. When Type is
// ResponseFormatJSONObject, the model is asked to emit any valid JSON
// object without a schema constraint.
type ResponseFormat struct {
	Type   ResponseFormatType
	Schema []byte
	Name   string
}

// ResponseFormatType discriminates the variants of ResponseFormat.
type ResponseFormatType string

// Variants of ResponseFormatType.
const (
	// ResponseFormatJSONObject asks the model to emit any valid JSON
	// object, without committing to a specific schema.
	ResponseFormatJSONObject ResponseFormatType = "json_object"

	// ResponseFormatJSONSchema asks the model to emit JSON matching
	// the document in ResponseFormat.Schema.
	ResponseFormatJSONSchema ResponseFormatType = "json_schema"
)
