package provider

import (
	"fmt"

	"github.com/YasserCR/galdor/pkg/schema"
)

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

// ValidateRequest checks whether req can be served given these
// capabilities. It returns nil when the request fits, or an
// ErrUnsupported-wrapped error explaining the first mismatch.
//
// This is a defensive helper meant to run at the call boundary
// (typically inside a Provider implementation's Generate / Stream
// or inside a higher-level helper like agent.Run). Adapters are
// still free to do their own native validation; the helper is for
// the cases where you want a single, language-neutral check before
// the wire call.
//
// Mismatches caught:
//
//   - Tools set but ToolCalling == false
//   - ResponseFormat set but StructuredOutput == false
//   - Vision image part present but VisionInput == false
//   - CacheControl hints present but PromptCaching == false
//
// Streaming is NOT validated here; Stream returns ErrUnsupported on
// its own when Capabilities.Streaming is false.
func (c Capabilities) ValidateRequest(req Request) error {
	if len(req.Tools) > 0 && !c.ToolCalling {
		return fmt.Errorf("%w: provider does not support tool calling but Request.Tools has %d entries",
			ErrUnsupported, len(req.Tools))
	}
	if req.ResponseFormat != nil && !c.StructuredOutput {
		return fmt.Errorf("%w: provider does not support structured outputs but Request.ResponseFormat is set",
			ErrUnsupported)
	}
	if hasImageInput(req.Messages) && !c.VisionInput {
		return fmt.Errorf("%w: provider does not support vision input but Request.Messages contains image parts",
			ErrUnsupported)
	}
	if hasCacheControl(req.Messages) && !c.PromptCaching {
		return fmt.Errorf("%w: provider does not support prompt caching but Request.Messages carries CacheControl hints",
			ErrUnsupported)
	}
	return nil
}

// hasImageInput reports whether any message in msgs carries an
// image content part.
func hasImageInput(msgs []schema.Message) bool {
	for _, m := range msgs {
		for _, p := range m.Content {
			if p.Type == schema.ContentTypeImage {
				return true
			}
		}
	}
	return false
}

// hasCacheControl reports whether any message in msgs carries a
// CacheControl hint.
func hasCacheControl(msgs []schema.Message) bool {
	for _, m := range msgs {
		if m.CacheControl != nil {
			return true
		}
	}
	return false
}
