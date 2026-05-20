package observability

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

var (
	runIDTestResp = provider.Response{
		Message:    schema.AssistantMessage("ok"),
		StopReason: schema.StopReasonEndTurn,
		Usage:      schema.Usage{InputTokens: 5, OutputTokens: 2},
		Model:      "test-model",
	}
	runIDTestReq = provider.Request{Model: "test-model"}
)

func TestWithRunID_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if got := RunIDFromContext(ctx); got != "" {
		t.Errorf("empty ctx returned %q", got)
	}
	ctx = WithRunID(ctx, "abc-123")
	if got := RunIDFromContext(ctx); got != "abc-123" {
		t.Errorf("RunIDFromContext = %q, want %q", got, "abc-123")
	}
}

func TestWithRunID_EmptyIsNoOp(t *testing.T) {
	t.Parallel()
	parent := WithRunID(context.Background(), "set")
	child := WithRunID(parent, "")
	if got := RunIDFromContext(child); got != "set" {
		t.Errorf("WithRunID(_, \"\") should not overwrite, got %q", got)
	}
}

func TestResolveRunID_ExplicitWins(t *testing.T) {
	t.Parallel()
	tp := sdktrace.NewTracerProvider()
	tr := tp.Tracer("test")
	ctx, span := tr.Start(context.Background(), "test")
	defer span.End()
	ctx = WithRunID(ctx, "explicit")
	if got := resolveRunID(ctx); got != "explicit" {
		t.Errorf("explicit run id should win over trace fallback, got %q", got)
	}
}

func TestResolveRunID_FallsBackToTraceID(t *testing.T) {
	t.Parallel()
	tp := sdktrace.NewTracerProvider()
	tr := tp.Tracer("test")
	ctx, span := tr.Start(context.Background(), "test")
	defer span.End()
	got := resolveRunID(ctx)
	want := span.SpanContext().TraceID().String()
	if got != want {
		t.Errorf("resolveRunID = %q, want trace id %q", got, want)
	}
}

func TestResolveRunID_EmptyWhenNoSpanNoID(t *testing.T) {
	t.Parallel()
	if got := resolveRunID(context.Background()); got != "" {
		t.Errorf("empty ctx should resolve to \"\", got %q", got)
	}
}

// TestInstrumentProvider_AutoStampsRunIDFromContext verifies that
// callers can drop the explicit graph wiring and still get spans
// grouped into runs by setting the id on the ctx.
func TestInstrumentProvider_AutoStampsRunIDFromContext(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	p := InstrumentProvider(fakeProvider{resp: &runIDTestResp}, tr)

	ctx := WithRunID(context.Background(), "explicit-run")
	if _, err := p.Generate(ctx, runIDTestReq); err != nil {
		t.Fatal(err)
	}
	_ = tp.ForceFlush(context.Background())
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}
	got, ok := findAttr(spans[0].Attributes, AttrGaldorRunID)
	if !ok || got != "explicit-run" {
		t.Errorf("galdor.run.id = %q, ok=%v, want \"explicit-run\"", got, ok)
	}
}

// TestInstrumentProvider_FallsBackToTraceID verifies that when no
// run id is set explicitly the instrumentation stamps the active
// trace id so the dashboard still sees the spans grouped.
func TestInstrumentProvider_FallsBackToTraceID(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	p := InstrumentProvider(fakeProvider{resp: &runIDTestResp}, tr)

	ctx, parent := tr.Start(context.Background(), "outer")
	defer parent.End()
	if _, err := p.Generate(ctx, runIDTestReq); err != nil {
		t.Fatal(err)
	}
	parent.End()
	_ = tp.ForceFlush(context.Background())

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans")
	}
	got, ok := findAttr(spans[0].Attributes, AttrGaldorRunID)
	want := parent.SpanContext().TraceID().String()
	if !ok || got != want {
		t.Errorf("galdor.run.id = %q (ok=%v), want trace id %q", got, ok, want)
	}
}
