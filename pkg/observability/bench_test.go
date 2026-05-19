package observability_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/YasserCR/galdor/pkg/observability"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// noopProvider is the cheapest possible Provider — returns a tiny
// fixed Response. Used to isolate the instrumentation's overhead
// from anything the real provider would do.
type noopProvider struct{}

func (noopProvider) Name() string                        { return "noop" }
func (noopProvider) Capabilities() provider.Capabilities { return provider.Capabilities{} }
func (noopProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}
func (noopProvider) Generate(_ context.Context, _ provider.Request) (*provider.Response, error) {
	return &provider.Response{
		Message:    schema.AssistantMessage("ok"),
		StopReason: schema.StopReasonEndTurn,
		Usage:      schema.Usage{InputTokens: 1, OutputTokens: 1},
	}, nil
}

// BenchmarkRaw_Generate measures the noop provider on its own —
// the baseline we subtract from the instrumented versions to get
// the actual instrumentation cost.
func BenchmarkRaw_Generate(b *testing.B) {
	p := noopProvider{}
	ctx := context.Background()
	req := provider.Request{Model: "x", Messages: []schema.Message{schema.UserMessage("hi")}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = p.Generate(ctx, req)
	}
}

// BenchmarkInstrumented_Generate measures Generate through
// InstrumentProvider with prompt/completion capture OFF (the
// production-default mode — light, attribute-only span).
func BenchmarkInstrumented_Generate(b *testing.B) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	tracer := tp.Tracer("bench")
	p := observability.InstrumentProvider(noopProvider{}, tracer)
	ctx := context.Background()
	req := provider.Request{Model: "x", Messages: []schema.Message{schema.UserMessage("hi")}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = p.Generate(ctx, req)
	}
}

// BenchmarkInstrumented_GenerateWithCapture measures Generate
// through InstrumentProvider with prompt/completion capture ON.
// This is the heavy-but-debuggable mode used to populate fixtures
// for the replay engine.
func BenchmarkInstrumented_GenerateWithCapture(b *testing.B) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	tracer := tp.Tracer("bench")
	p := observability.InstrumentProvider(noopProvider{}, tracer, observability.WithCaptureContent(true))
	ctx := context.Background()
	req := provider.Request{Model: "x", Messages: []schema.Message{schema.UserMessage("hi")}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = p.Generate(ctx, req)
	}
}
