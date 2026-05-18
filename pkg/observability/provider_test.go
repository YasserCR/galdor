package observability

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// newRecorder builds a TracerProvider with an in-memory exporter and
// returns the exporter so tests can read back the spans.
func newRecorder(t *testing.T) (*tracetest.InMemoryExporter, *sdktrace.TracerProvider) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
	)
	return exp, tp
}

// fakeProvider returns canned responses; useful for instrumentation
// tests that don't want to mock HTTP.
type fakeProvider struct {
	resp     *provider.Response
	err      error
	stream   provider.StreamReader
	streamEr error
}

func (fakeProvider) Name() string                        { return "fake" }
func (fakeProvider) Capabilities() provider.Capabilities { return provider.Capabilities{} }
func (f fakeProvider) Generate(_ context.Context, _ provider.Request) (*provider.Response, error) {
	return f.resp, f.err
}
func (f fakeProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return f.stream, f.streamEr
}

func TestInstrumentProvider_GenerateHappyPath(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")

	p := InstrumentProvider(fakeProvider{
		resp: &provider.Response{
			Message:    schema.AssistantMessage("hi"),
			StopReason: schema.StopReasonEndTurn,
			Usage:      schema.Usage{InputTokens: 10, OutputTokens: 5},
			Model:      "model-x",
		},
	}, tr)

	if _, err := p.Generate(context.Background(), provider.Request{Model: "req-m"}); err != nil {
		t.Fatal(err)
	}
	_ = tp.ForceFlush(context.Background())

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}
	got := spans[0]
	if got.Name != SpanProviderGenerate {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Status.Code != codes.Unset {
		t.Errorf("Status = %v", got.Status.Code)
	}
	if !hasAttr(got.Attributes, AttrGenAIRequestModel, "req-m") {
		t.Errorf("missing request model attr: %+v", got.Attributes)
	}
	if !hasIntAttr(got.Attributes, AttrGenAIUsageInputTokens, 10) {
		t.Errorf("missing input tokens attr: %+v", got.Attributes)
	}
}

func TestInstrumentProvider_GenerateRecordsError(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	boom := errors.New("boom")
	p := InstrumentProvider(fakeProvider{err: boom}, tr)
	if _, err := p.Generate(context.Background(), provider.Request{Model: "x"}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	_ = tp.ForceFlush(context.Background())
	spans := exp.GetSpans()
	if len(spans) != 1 || spans[0].Status.Code != codes.Error {
		t.Fatalf("status = %v, events = %+v", spans[0].Status.Code, spans[0].Events)
	}
}

// scriptedStream returns a fixed list of events then io.EOF.
type scriptedStream struct {
	events []provider.Event
	idx    int
	closed bool
}

func (s *scriptedStream) Recv(_ context.Context) (provider.Event, error) {
	if s.idx >= len(s.events) {
		return provider.Event{}, io.EOF
	}
	ev := s.events[s.idx]
	s.idx++
	return ev, nil
}
func (s *scriptedStream) Close() error { s.closed = true; return nil }

func TestInstrumentProvider_StreamCapturesTerminalUsage(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	inner := &scriptedStream{events: []provider.Event{
		{Type: provider.EventMessageStart, Model: "model-x"},
		{Type: provider.EventContentDelta, ContentDelta: "hi"},
		{Type: provider.EventMessageStop, StopReason: schema.StopReasonEndTurn,
			Usage: schema.Usage{InputTokens: 4, OutputTokens: 2}},
	}}
	p := InstrumentProvider(fakeProvider{stream: inner}, tr)
	r, err := p.Stream(context.Background(), provider.Request{Model: "req"})
	if err != nil {
		t.Fatal(err)
	}
	// Drain.
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
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}
	if !hasIntAttr(spans[0].Attributes, AttrGenAIUsageOutputTokens, 2) {
		t.Errorf("missing output tokens: %+v", spans[0].Attributes)
	}
}

func TestInstrumentProvider_StreamCloseEndsSpanIdempotently(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	inner := &scriptedStream{events: []provider.Event{
		{Type: provider.EventContentDelta, ContentDelta: "x"},
	}}
	p := InstrumentProvider(fakeProvider{stream: inner}, tr)
	r, err := p.Stream(context.Background(), provider.Request{Model: "req"})
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	_ = r.Close() // second Close must not panic
	_ = tp.ForceFlush(context.Background())
	if len(exp.GetSpans()) != 1 {
		t.Errorf("close should end the span exactly once, got %d spans", len(exp.GetSpans()))
	}
}

func TestInstrumentProvider_StreamConstructionErrorRecorded(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	p := InstrumentProvider(fakeProvider{streamEr: provider.ErrUnsupported}, tr)
	if _, err := p.Stream(context.Background(), provider.Request{Model: "x"}); err == nil {
		t.Fatal("expected error")
	}
	_ = tp.ForceFlush(context.Background())
	spans := exp.GetSpans()
	if len(spans) != 1 || spans[0].Status.Code != codes.Error {
		t.Fatalf("status = %v", spans[0].Status.Code)
	}
}

func TestInstrumentProvider_NameAndCapabilitiesPassthrough(t *testing.T) {
	t.Parallel()
	_, tp := newRecorder(t)
	tr := tp.Tracer("test")
	p := InstrumentProvider(fakeProvider{}, tr)
	if p.Name() != "fake" {
		t.Errorf("Name = %q", p.Name())
	}
	_ = p.Capabilities() // exercising pass-through; just confirm no panic.
}

// hasAttr / hasIntAttr scan a span's attributes for a (key, value)
// pair.
func hasAttr(attrs []attribute.KeyValue, key, value string) bool {
	for _, a := range attrs {
		if string(a.Key) == key && a.Value.AsString() == value {
			return true
		}
	}
	return false
}

// findAttr returns the raw attribute value as a string.
func findAttr(attrs []attribute.KeyValue, key string) (string, bool) {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value.AsString(), true
		}
	}
	return "", false
}

func TestInstrumentProvider_CaptureContentOnGenerate(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	p := InstrumentProvider(fakeProvider{
		resp: &provider.Response{
			Message:    schema.AssistantMessage("the answer is 5"),
			StopReason: schema.StopReasonEndTurn,
			Usage:      schema.Usage{InputTokens: 30, OutputTokens: 7},
			Model:      "model-x",
		},
	}, tr, WithCaptureContent(true))

	req := provider.Request{
		Model:    "req-m",
		Messages: []schema.Message{schema.UserMessage("what is 2+3?")},
	}
	if _, err := p.Generate(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	_ = tp.ForceFlush(context.Background())

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}
	prompt, ok := findAttr(spans[0].Attributes, AttrGenAIPrompt)
	if !ok {
		t.Fatalf("missing prompt attribute: %+v", spans[0].Attributes)
	}
	if !strings.Contains(prompt, "what is 2+3?") {
		t.Errorf("prompt missing user message: %q", prompt)
	}
	completion, ok := findAttr(spans[0].Attributes, AttrGenAICompletion)
	if !ok {
		t.Fatalf("missing completion attribute")
	}
	if !strings.Contains(completion, "the answer is 5") {
		t.Errorf("completion missing assistant text: %q", completion)
	}
}

func TestInstrumentProvider_CaptureContentOffByDefault(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	p := InstrumentProvider(fakeProvider{
		resp: &provider.Response{Message: schema.AssistantMessage("hi")},
	}, tr)
	_, _ = p.Generate(context.Background(), provider.Request{
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	_ = tp.ForceFlush(context.Background())
	spans := exp.GetSpans()
	if _, ok := findAttr(spans[0].Attributes, AttrGenAIPrompt); ok {
		t.Error("prompt should NOT be captured by default")
	}
	if _, ok := findAttr(spans[0].Attributes, AttrGenAICompletion); ok {
		t.Error("completion should NOT be captured by default")
	}
}

func TestInstrumentProvider_CaptureContentOnStream(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	inner := &scriptedStream{events: []provider.Event{
		{Type: provider.EventMessageStart, Model: "m"},
		{Type: provider.EventContentDelta, ContentDelta: "hello "},
		{Type: provider.EventContentDelta, ContentDelta: "world"},
		{Type: provider.EventMessageStop, StopReason: schema.StopReasonEndTurn,
			Usage: schema.Usage{InputTokens: 3, OutputTokens: 2}},
	}}
	p := InstrumentProvider(fakeProvider{stream: inner}, tr, WithCaptureContent(true))
	r, err := p.Stream(context.Background(), provider.Request{
		Model:    "m",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
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
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}
	prompt, ok := findAttr(spans[0].Attributes, AttrGenAIPrompt)
	if !ok || !strings.Contains(prompt, "hi") {
		t.Errorf("prompt = %q", prompt)
	}
	completion, ok := findAttr(spans[0].Attributes, AttrGenAICompletion)
	if !ok || !strings.Contains(completion, "hello world") {
		t.Errorf("completion = %q", completion)
	}
}

func hasIntAttr(attrs []attribute.KeyValue, key string, value int) bool {
	for _, a := range attrs {
		if string(a.Key) == key && a.Value.AsInt64() == int64(value) {
			return true
		}
	}
	return false
}
