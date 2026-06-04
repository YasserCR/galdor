package observability

import (
	"context"
	"testing"
)

func TestWithSpanLabel_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if got := SpanLabelFromContext(ctx); got != "" {
		t.Errorf("empty ctx returned %q", got)
	}
	ctx = WithSpanLabel(ctx, "review code")
	if got := SpanLabelFromContext(ctx); got != "review code" {
		t.Errorf("SpanLabelFromContext = %q, want %q", got, "review code")
	}
}

func TestWithSpanLabel_EmptyIsNoOp(t *testing.T) {
	t.Parallel()
	parent := WithSpanLabel(context.Background(), "set")
	child := WithSpanLabel(parent, "")
	if got := SpanLabelFromContext(child); got != "set" {
		t.Errorf("WithSpanLabel(_, \"\") should not overwrite, got %q", got)
	}
}

// TestInstrumentProvider_StampsSpanLabel verifies the label set on the
// context lands on the provider span as galdor.span.label.
func TestInstrumentProvider_StampsSpanLabel(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	p := InstrumentProvider(fakeProvider{resp: &runIDTestResp}, tr)

	ctx := WithSpanLabel(context.Background(), "draft")
	if _, err := p.Generate(ctx, runIDTestReq); err != nil {
		t.Fatal(err)
	}
	_ = tp.ForceFlush(context.Background())

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}
	got, ok := findAttr(spans[0].Attributes, AttrGaldorSpanLabel)
	if !ok || got != "draft" {
		t.Errorf("galdor.span.label = %q (ok=%v), want \"draft\"", got, ok)
	}
}

// TestInstrumentProvider_NoLabelNoAttr verifies the attribute is absent
// when no label is set, so unlabeled spans stay clean.
func TestInstrumentProvider_NoLabelNoAttr(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	p := InstrumentProvider(fakeProvider{resp: &runIDTestResp}, tr)

	if _, err := p.Generate(context.Background(), runIDTestReq); err != nil {
		t.Fatal(err)
	}
	_ = tp.ForceFlush(context.Background())

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}
	if _, ok := findAttr(spans[0].Attributes, AttrGaldorSpanLabel); ok {
		t.Error("galdor.span.label should be absent when no label is set")
	}
}
