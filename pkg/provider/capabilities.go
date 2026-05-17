package provider

// Capabilities advertises what a Provider can do. Callers use it to
// gracefully fall back when a feature is not supported (e.g., omit tools
// when ToolCalling is false rather than failing the call).
//
// Adapters return their Capabilities synchronously and the value must not
// vary across calls — it is a property of the provider build, not of the
// request.
type Capabilities struct {
	// Streaming indicates whether Stream is supported. When false, Stream
	// returns ErrUnsupported and callers must use Generate.
	Streaming bool

	// ToolCalling indicates whether the provider can invoke tools as part
	// of its reply.
	ToolCalling bool

	// StructuredOutput indicates support for JSON-mode or schema-guided
	// outputs (Request.ResponseFormat).
	StructuredOutput bool

	// PromptCaching indicates that schema.CacheControl hints are honored.
	PromptCaching bool

	// VisionInput indicates that image ContentParts may be sent.
	VisionInput bool

	// MaxContextTokens is the provider-advertised context window for the
	// default model. Zero means unknown.
	MaxContextTokens int
}
