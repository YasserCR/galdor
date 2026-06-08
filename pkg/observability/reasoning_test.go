package observability

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// reasoningResp is a response carrying both a clean text answer and a
// reasoning (thinking) part, as ExtractThinkingBlocks would produce.
var reasoningResp = provider.Response{
	Message: schema.Message{
		Role: schema.RoleAssistant,
		Content: []schema.ContentPart{
			schema.TextPart("the answer"),
			schema.ThinkingPart("first I considered X, then Y"),
		},
	},
	StopReason: schema.StopReasonEndTurn,
	Usage:      schema.Usage{InputTokens: 5, OutputTokens: 2},
	Model:      "test-model",
}

// TestInstrumentProvider_CapturesReasoning verifies the thinking parts
// land on gen_ai.reasoning when WithCaptureReasoning is on.
func TestInstrumentProvider_CapturesReasoning(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	p := InstrumentProvider(fakeProvider{resp: &reasoningResp}, tr, WithCaptureReasoning(true))

	if _, err := p.Generate(context.Background(), runIDTestReq); err != nil {
		t.Fatal(err)
	}
	_ = tp.ForceFlush(context.Background())

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}
	got, ok := findAttr(spans[0].Attributes, AttrGenAIReasoning)
	if !ok {
		t.Fatalf("gen_ai.reasoning absent: %+v", spans[0].Attributes)
	}
	want := `["first I considered X, then Y"]`
	if got != want {
		t.Errorf("gen_ai.reasoning = %q, want %q", got, want)
	}
}

// TestInstrumentProvider_ReasoningOptIn verifies the attribute is
// absent without the flag, even when the message carries a thinking
// part — reasoning must be opt-in.
func TestInstrumentProvider_ReasoningOptIn(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	p := InstrumentProvider(fakeProvider{resp: &reasoningResp}, tr) // no WithCaptureReasoning

	if _, err := p.Generate(context.Background(), runIDTestReq); err != nil {
		t.Fatal(err)
	}
	_ = tp.ForceFlush(context.Background())

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}
	if _, ok := findAttr(spans[0].Attributes, AttrGenAIReasoning); ok {
		t.Error("gen_ai.reasoning should be absent without WithCaptureReasoning")
	}
}

// TestInstrumentProvider_ReasoningIndependentOfContent verifies you can
// capture reasoning without capturing the full prompt/completion.
func TestInstrumentProvider_ReasoningIndependentOfContent(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	p := InstrumentProvider(fakeProvider{resp: &reasoningResp}, tr, WithCaptureReasoning(true))

	if _, err := p.Generate(context.Background(), runIDTestReq); err != nil {
		t.Fatal(err)
	}
	_ = tp.ForceFlush(context.Background())

	attrs := exp.GetSpans()[0].Attributes
	if _, ok := findAttr(attrs, AttrGenAIReasoning); !ok {
		t.Error("reasoning should be captured")
	}
	if _, ok := findAttr(attrs, AttrGenAICompletion); ok {
		t.Error("completion should be absent when only WithCaptureReasoning is set")
	}
}

// TestInstrumentProvider_StreamCapturesReasoning verifies that a
// reasoning-only terminal Message (the streaming-capture shape produced
// by ExtractThinkingBlocks / native reasoning providers) lands on
// gen_ai.reasoning while gen_ai.completion stays the clean streamed text.
func TestInstrumentProvider_StreamCapturesReasoning(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	inner := &scriptedStream{events: []provider.Event{
		{Type: provider.EventMessageStart, Model: "m"},
		{Type: provider.EventContentDelta, ContentDelta: "the "},
		{Type: provider.EventContentDelta, ContentDelta: "answer"},
		{Type: provider.EventMessageStop, StopReason: schema.StopReasonEndTurn,
			Usage: schema.Usage{InputTokens: 4, OutputTokens: 2},
			Message: &schema.Message{
				Role:    schema.RoleAssistant,
				Content: []schema.ContentPart{schema.ThinkingPart("my reasoning")},
			}},
	}}
	p := InstrumentProvider(fakeProvider{stream: inner}, tr,
		WithCaptureContent(true), WithCaptureReasoning(true))
	r, err := p.Stream(context.Background(), provider.Request{Model: "req"})
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, err := r.Recv(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	_ = tp.ForceFlush(context.Background())

	attrs := exp.GetSpans()[0].Attributes
	reasoning, ok := findAttr(attrs, AttrGenAIReasoning)
	if !ok || reasoning != `["my reasoning"]` {
		t.Errorf("gen_ai.reasoning = %q (ok=%v), want [\"my reasoning\"]", reasoning, ok)
	}
	// Completion must be the clean streamed text, NOT displaced by the
	// reasoning-only terminal message.
	completion, ok := findAttr(attrs, AttrGenAICompletion)
	if !ok {
		t.Fatal("gen_ai.completion absent")
	}
	if !strings.Contains(completion, "the answer") {
		t.Errorf("completion lost streamed text: %q", completion)
	}
	if strings.Contains(completion, "my reasoning") {
		t.Errorf("reasoning leaked into completion: %q", completion)
	}
}
