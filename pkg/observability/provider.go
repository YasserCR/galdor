package observability

import (
	"context"
	"errors"
	"io"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// InstrumentProvider returns a provider.Provider that wraps p and
// emits a span around every Generate / Stream call. Spans carry the
// gen_ai.* attributes from the request and response (model, token
// counts, stop reason) and galdor.provider.name for filtering.
//
// The underlying provider's Name() and Capabilities() pass through
// without instrumentation overhead.
func InstrumentProvider(p provider.Provider, tracer trace.Tracer) provider.Provider {
	if p == nil {
		panic("observability: nil provider")
	}
	if tracer == nil {
		panic("observability: nil tracer")
	}
	return &instrumentedProvider{inner: p, tracer: tracer}
}

type instrumentedProvider struct {
	inner  provider.Provider
	tracer trace.Tracer
}

func (i *instrumentedProvider) Name() string                        { return i.inner.Name() }
func (i *instrumentedProvider) Capabilities() provider.Capabilities { return i.inner.Capabilities() }

func (i *instrumentedProvider) Generate(ctx context.Context, req provider.Request) (resp *provider.Response, err error) {
	ctx, span := i.tracer.Start(ctx, SpanProviderGenerate, trace.WithAttributes(
		attribute.String(AttrGenAISystem, i.inner.Name()),
		attribute.String(AttrGaldorProvider, i.inner.Name()),
		attribute.String(AttrGenAIRequestModel, req.Model),
		attribute.Bool(AttrGaldorStreaming, false),
	))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else if resp != nil {
			span.SetAttributes(
				attribute.String(AttrGenAIResponseModel, resp.Model),
				attribute.String(AttrGenAIResponseFinish, string(resp.StopReason)),
				attribute.Int(AttrGenAIUsageInputTokens, resp.Usage.InputTokens),
				attribute.Int(AttrGenAIUsageOutputTokens, resp.Usage.OutputTokens),
			)
		}
		span.End()
	}()
	return i.inner.Generate(ctx, req)
}

func (i *instrumentedProvider) Stream(ctx context.Context, req provider.Request) (provider.StreamReader, error) {
	ctx, span := i.tracer.Start(ctx, SpanProviderStream, trace.WithAttributes(
		attribute.String(AttrGenAISystem, i.inner.Name()),
		attribute.String(AttrGaldorProvider, i.inner.Name()),
		attribute.String(AttrGenAIRequestModel, req.Model),
		attribute.Bool(AttrGaldorStreaming, true),
	))
	reader, err := i.inner.Stream(ctx, req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		return nil, err
	}
	return &instrumentedStream{inner: reader, span: span}, nil
}

// instrumentedStream wraps a StreamReader so the span ends only when
// the consumer finishes draining the stream (or closes it). Final
// usage and stop reason — both observed on the terminal event — are
// recorded as span attributes.
type instrumentedStream struct {
	inner provider.StreamReader
	span  trace.Span
	final struct {
		stop    schema.StopReason
		usage   schema.Usage
		hasStop bool
	}
	ended bool
}

func (s *instrumentedStream) Recv(ctx context.Context) (provider.Event, error) {
	ev, err := s.inner.Recv(ctx)
	switch {
	case errors.Is(err, io.EOF):
		s.endSpan(nil)
	case err != nil:
		s.endSpan(err)
	case ev.Type == provider.EventMessageStop:
		s.final.stop = ev.StopReason
		s.final.usage = ev.Usage
		s.final.hasStop = true
	}
	return ev, err
}

func (s *instrumentedStream) Close() error {
	s.endSpan(nil)
	return s.inner.Close()
}

func (s *instrumentedStream) endSpan(err error) {
	if s.ended {
		return
	}
	s.ended = true
	if err != nil {
		s.span.RecordError(err)
		s.span.SetStatus(codes.Error, err.Error())
	}
	if s.final.hasStop {
		s.span.SetAttributes(
			attribute.String(AttrGenAIResponseFinish, string(s.final.stop)),
			attribute.Int(AttrGenAIUsageInputTokens, s.final.usage.InputTokens),
			attribute.Int(AttrGenAIUsageOutputTokens, s.final.usage.OutputTokens),
		)
	}
	s.span.End()
}
