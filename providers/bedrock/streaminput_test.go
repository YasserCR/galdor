package bedrock

import (
	"encoding/json"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
)

// Regression for audit C2: the streaming path must carry the SAME
// reasoning_config and request metadata as the non-streaming path.
// Before the fix, Stream hand-copied only five ConverseInput fields into
// ConverseStreamInput, silently dropping AdditionalModelRequestFields
// (reasoning_config) and RequestMetadata — so a streamed call with
// Reasoning.Enabled ran with NO thinking while its side effects (nulled
// temperature/top_p, inflated max_tokens) still applied.
func TestBuildConverseStreamInput_CarriesReasoning(t *testing.T) {
	t.Parallel()

	temp := 0.7
	in, err := buildConverseStreamInput(provider.Request{
		Model:       "m",
		Temperature: &temp,
		Reasoning:   &provider.ReasoningConfig{Enabled: true, Budget: 4096},
		Metadata:    map[string]string{"user_id": "u-123"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	// reasoning_config must survive into the stream input.
	if in.AdditionalModelRequestFields == nil {
		t.Fatal("AdditionalModelRequestFields dropped on the stream path (regression of C2)")
	}
	b, err := in.AdditionalModelRequestFields.MarshalSmithyDocument()
	if err != nil {
		t.Fatalf("marshal fields: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatalf("unmarshal fields: %v", err)
	}
	if _, ok := fields["reasoning_config"].(map[string]any); !ok {
		t.Fatalf("reasoning_config missing from stream input: %+v", fields)
	}

	// RequestMetadata must survive too.
	if in.RequestMetadata["user_id"] != "u-123" {
		t.Errorf("RequestMetadata.user_id = %q, want u-123 (dropped on stream path)", in.RequestMetadata["user_id"])
	}

	// And the reasoning side effects must be consistent with the
	// non-stream path (temp/top_p dropped, max_tokens inflated).
	if in.InferenceConfig == nil || in.InferenceConfig.Temperature != nil || in.InferenceConfig.TopP != nil {
		t.Errorf("temperature/top_p must be dropped when reasoning is on; got %+v", in.InferenceConfig)
	}
}

// The stream input must mirror the non-stream input field-for-field, so
// the two paths can't drift again.
func TestBuildConverseStreamInput_MatchesConverseInput(t *testing.T) {
	t.Parallel()

	req := provider.Request{
		Model:     "m",
		Reasoning: &provider.ReasoningConfig{Enabled: true, Budget: 2048},
		Metadata:  map[string]string{"user_id": "u-9"},
	}
	gen, err := buildConverseInput(req)
	if err != nil {
		t.Fatalf("buildConverseInput: %v", err)
	}
	str, err := buildConverseStreamInput(req)
	if err != nil {
		t.Fatalf("buildConverseStreamInput: %v", err)
	}

	if (gen.AdditionalModelRequestFields == nil) != (str.AdditionalModelRequestFields == nil) {
		t.Error("AdditionalModelRequestFields presence diverges between Converse and ConverseStream")
	}
	if aws := str.ModelId; aws == nil || *aws != *gen.ModelId {
		t.Error("ModelId diverges between the two paths")
	}
	if str.RequestMetadata["user_id"] != gen.RequestMetadata["user_id"] {
		t.Error("RequestMetadata diverges between the two paths")
	}
}
