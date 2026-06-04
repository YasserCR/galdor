package observability

// Attribute keys. Where the OpenTelemetry GenAI semantic conventions
// already define a key (gen_ai.*) we use it verbatim; galdor-specific
// dimensions (run ID, node name, step counter) are namespaced under
// galdor.*.
//
// Reference: https://opentelemetry.io/docs/specs/semconv/gen-ai/
const (
	// gen_ai.* attributes follow the OTel GenAI semantic conventions.
	AttrGenAISystem         = "gen_ai.system"         // "anthropic", "openai", ...
	AttrGenAIRequestModel   = "gen_ai.request.model"  // model ID sent to the provider
	AttrGenAIResponseModel  = "gen_ai.response.model" // model that actually served
	AttrGenAIResponseFinish = "gen_ai.response.finish_reasons"
	// gosec G101 sees "tokens" in these identifiers and flags them as
	// potential credentials. They are OpenTelemetry semantic-convention
	// attribute names, not secrets.
	AttrGenAIUsageInputTokens  = "gen_ai.usage.input_tokens"  /* #nosec G101 -- OTel semconv attribute name, not a credential */
	AttrGenAIUsageOutputTokens = "gen_ai.usage.output_tokens" /* #nosec G101 -- OTel semconv attribute name, not a credential */

	// gen_ai.tool.* — emitted by InstrumentTool.
	AttrGenAIToolName       = "gen_ai.tool.name"
	AttrGenAIToolInputSize  = "gen_ai.tool.input_size_bytes"
	AttrGenAIToolOutputSize = "gen_ai.tool.output_size_bytes"

	// gen_ai.prompt / gen_ai.completion — emitted only when the
	// caller opts into content capture via WithCaptureContent.
	// Both are JSON-encoded so the UI can render them structured.
	// Off by default because prompts often contain PII.
	AttrGenAIPrompt     = "gen_ai.prompt"
	AttrGenAICompletion = "gen_ai.completion"

	// galdor.* — framework-specific dimensions.
	AttrGaldorRunID     = "galdor.run.id"
	AttrGaldorNode      = "galdor.node.name"
	AttrGaldorStep      = "galdor.step"
	AttrGaldorStateGo   = "galdor.state.type" // Go type name of the graph state
	AttrGaldorProvider  = "galdor.provider.name"
	AttrGaldorStreaming = "galdor.provider.streaming"
	AttrGaldorSpanLabel = "galdor.span.label" // optional human-readable label set via WithSpanLabel
)

// Span names. Centralized so the dashboard (Phase 5) and any
// external trace pipeline can recognize galdor spans without
// fuzzy matching.
const (
	SpanProviderGenerate = "galdor.provider.generate"
	SpanProviderStream   = "galdor.provider.stream"
	SpanToolExecute      = "galdor.tool.execute"
	SpanGraphRun         = "galdor.graph.run"
	SpanGraphNode        = "galdor.graph.node"
)
