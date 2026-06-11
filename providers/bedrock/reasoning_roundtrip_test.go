package bedrock

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/YasserCR/galdor/pkg/schema"
)

// Regression for audit H7 (Bedrock half): Claude-on-Bedrock requires the
// signed reasoning block echoed back in the assistant turn that carries
// tool_use when extended thinking is on ("include the text and its
// signature unmodified", per the Converse API). Before the fix,
// partsToBlocks dropped every thinking part unconditionally, so a
// Reasoning+tools loop could not complete a round-trip.
func TestPartsToBlocks_EchoesSignedReasoning(t *testing.T) {
	t.Parallel()

	blocks, err := partsToBlocks([]schema.ContentPart{
		{Type: schema.ContentTypeThinking, Text: "step by step", Signature: "sig-abc"},
		schema.TextPart("the answer is 42"),
	})
	if err != nil {
		t.Fatalf("partsToBlocks: %v", err)
	}

	var rc *brtypes.ContentBlockMemberReasoningContent
	for _, b := range blocks {
		if v, ok := b.(*brtypes.ContentBlockMemberReasoningContent); ok {
			rc = v
		}
	}
	if rc == nil {
		t.Fatal("signed reasoning block was not echoed back (regression of H7)")
	}
	rt, ok := rc.Value.(*brtypes.ReasoningContentBlockMemberReasoningText)
	if !ok {
		t.Fatalf("reasoning block has wrong variant: %T", rc.Value)
	}
	if got := aws.ToString(rt.Value.Text); got != "step by step" {
		t.Errorf("reasoning text = %q, want %q", got, "step by step")
	}
	if got := aws.ToString(rt.Value.Signature); got != "sig-abc" {
		t.Errorf("reasoning signature = %q, want %q (must be sent unmodified)", got, "sig-abc")
	}
}

// Unsigned reasoning carries no continuation value and would be rejected
// if resent without a valid signature, so it must still be skipped.
func TestPartsToBlocks_SkipsUnsignedReasoning(t *testing.T) {
	t.Parallel()

	blocks, err := partsToBlocks([]schema.ContentPart{
		{Type: schema.ContentTypeThinking, Text: "no signature here"},
		schema.TextPart("hi"),
	})
	if err != nil {
		t.Fatalf("partsToBlocks: %v", err)
	}
	for _, b := range blocks {
		if _, ok := b.(*brtypes.ContentBlockMemberReasoningContent); ok {
			t.Fatal("unsigned reasoning must not be echoed back")
		}
	}
}
