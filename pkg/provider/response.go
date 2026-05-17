package provider

import "github.com/YasserCR/galdor/pkg/schema"

// Response is the result of a Generate call (or the assembled result of a
// fully consumed stream — see CollectStream).
type Response struct {
	// Message is the assistant's reply, including any ToolCalls.
	Message schema.Message

	// StopReason describes why generation stopped.
	StopReason schema.StopReason

	// Usage reports tokens consumed. Zero fields mean "not reported" by
	// the provider, not "zero tokens used".
	Usage schema.Usage

	// Model echoes the model that actually served the request, which can
	// differ from Request.Model when the provider routes aliases.
	Model string

	// ProviderRaw carries the original wire payload as JSON, when the
	// adapter chooses to surface it. Useful for trace fidelity and for
	// extracting fields not yet modeled in galdor's shared schema.
	ProviderRaw []byte
}
