package observability

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// InstrumentOption configures the provider instrumentation. Use
// WithCaptureContent to opt into recording prompt + completion
// payloads on each span.
type InstrumentOption func(*instrumentOpts)

type instrumentOpts struct {
	captureContent   bool
	captureReasoning bool
}

// WithCaptureContent enables recording of the request messages
// (`gen_ai.prompt`) and the response message (`gen_ai.completion`)
// as JSON-encoded span attributes. Off by default because prompts
// frequently contain user PII, secrets or proprietary data that
// shouldn't end up in a shared trace store without explicit
// consent. Turn it on for local debugging and eval runs.
func WithCaptureContent(enabled bool) InstrumentOption {
	return func(o *instrumentOpts) { o.captureContent = enabled }
}

// WithCaptureReasoning enables recording the model's reasoning /
// chain-of-thought (`gen_ai.reasoning`) as a JSON-encoded span
// attribute. The reasoning is read from the schema.ContentTypeThinking
// parts on the response message, so it only appears when an upstream
// provider surfaces them (e.g. provider.ExtractThinkingBlocks, or a
// native reasoning provider).
//
// Independent of WithCaptureContent: you can capture reasoning without
// the full prompt/completion, or vice versa. Off by default because
// reasoning is frequently more sensitive than the final answer.
func WithCaptureReasoning(enabled bool) InstrumentOption {
	return func(o *instrumentOpts) { o.captureReasoning = enabled }
}

// InstrumentProvider returns a provider.Provider that wraps p and
// emits a span around every Generate / Stream call. Spans carry the
// gen_ai.* attributes from the request and response (model, token
// counts, stop reason) and galdor.provider.name for filtering.
//
// The underlying provider's Name() and Capabilities() pass through
// without instrumentation overhead.
func InstrumentProvider(p provider.Provider, tracer trace.Tracer, opts ...InstrumentOption) provider.Provider {
	if p == nil {
		panic("observability: nil provider")
	}
	if tracer == nil {
		panic("observability: nil tracer")
	}
	cfg := instrumentOpts{}
	for _, o := range opts {
		o(&cfg)
	}
	return &instrumentedProvider{inner: p, tracer: tracer, opts: cfg}
}

type instrumentedProvider struct {
	inner  provider.Provider
	tracer trace.Tracer
	opts   instrumentOpts
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
	if runID := resolveRunID(ctx); runID != "" {
		span.SetAttributes(attribute.String(AttrGaldorRunID, runID))
	}
	if label := SpanLabelFromContext(ctx); label != "" {
		span.SetAttributes(attribute.String(AttrGaldorSpanLabel, label))
	}
	if i.opts.captureContent {
		if v := encodeMessages(req.Messages); v != "" {
			span.SetAttributes(attribute.String(AttrGenAIPrompt, v))
		}
	}
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
			if i.opts.captureContent {
				if v := encodeMessage(resp.Message); v != "" {
					span.SetAttributes(attribute.String(AttrGenAICompletion, v))
				}
			}
			if i.opts.captureReasoning {
				if v := encodeThinking(resp.Message); v != "" {
					span.SetAttributes(attribute.String(AttrGenAIReasoning, v))
				}
			}
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
	if runID := resolveRunID(ctx); runID != "" {
		span.SetAttributes(attribute.String(AttrGaldorRunID, runID))
	}
	if label := SpanLabelFromContext(ctx); label != "" {
		span.SetAttributes(attribute.String(AttrGaldorSpanLabel, label))
	}
	if i.opts.captureContent {
		if v := encodeMessages(req.Messages); v != "" {
			span.SetAttributes(attribute.String(AttrGenAIPrompt, v))
		}
	}
	reader, err := i.inner.Stream(ctx, req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		return nil, err
	}
	return &instrumentedStream{
		inner:            reader,
		span:             span,
		captureContent:   i.opts.captureContent,
		captureReasoning: i.opts.captureReasoning,
	}, nil
}

// instrumentedStream wraps a StreamReader so the span ends only when
// the consumer finishes draining the stream (or closes it). Final
// usage and stop reason — both observed on the terminal event — are
// recorded as span attributes.
type instrumentedStream struct {
	inner            provider.StreamReader
	span             trace.Span
	captureContent   bool
	captureReasoning bool
	collectedText    []byte          // text fragments concatenated when captureContent is on
	reasoningParts   []string        // thinking parts pulled from the terminal event's Message
	finalMessage     *schema.Message // populated from the terminal event when it carries non-reasoning content
	final            struct {
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
	case ev.Type == provider.EventContentDelta && s.captureContent:
		s.collectedText = append(s.collectedText, ev.ContentDelta...)
	case ev.Type == provider.EventMessageStop:
		s.final.stop = ev.StopReason
		s.final.usage = ev.Usage
		s.final.hasStop = true
		if (s.captureContent || s.captureReasoning) && ev.Message != nil {
			// Pull reasoning out regardless of message shape.
			for _, p := range ev.Message.Content {
				if p.Type == schema.ContentTypeThinking && p.Text != "" {
					s.reasoningParts = append(s.reasoningParts, p.Text)
				}
			}
			// Adopt the terminal Message as the authoritative completion
			// only when it carries non-reasoning content (text / tool
			// calls). A reasoning-only message (the streaming-capture
			// case) must NOT displace the text reassembled from deltas.
			if messageHasNonThinking(ev.Message) {
				cp := *ev.Message
				s.finalMessage = &cp
			}
		}
	}
	return ev, err
}

func (s *instrumentedStream) Close() error {
	s.endSpan(nil)
	return s.inner.Close()
}

// assembledMessage returns the final assistant message captured
// during streaming. If the adapter surfaced a complete Message on
// EventMessageStop we use it verbatim; otherwise we synthesize one
// from the concatenated text deltas.
func (s *instrumentedStream) assembledMessage() schema.Message {
	if s.finalMessage != nil {
		return *s.finalMessage
	}
	if len(s.collectedText) == 0 && len(s.reasoningParts) == 0 {
		return schema.Message{}
	}
	msg := schema.Message{Role: schema.RoleAssistant}
	if len(s.collectedText) > 0 {
		msg.Content = append(msg.Content, schema.TextPart(string(s.collectedText)))
	}
	for _, r := range s.reasoningParts {
		msg.Content = append(msg.Content, schema.ThinkingPart(r))
	}
	return msg
}

// messageHasNonThinking reports whether m carries any content other than
// reasoning (text, images, tool calls) — i.e. whether it can stand in as
// the authoritative completion message.
func messageHasNonThinking(m *schema.Message) bool {
	if len(m.ToolCalls) > 0 {
		return true
	}
	for _, p := range m.Content {
		if p.Type != schema.ContentTypeThinking {
			return true
		}
	}
	return false
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
	if s.captureContent || s.captureReasoning {
		msg := s.assembledMessage()
		if s.captureContent {
			if v := encodeMessage(msg); v != "" {
				s.span.SetAttributes(attribute.String(AttrGenAICompletion, v))
			}
		}
		if s.captureReasoning {
			if v := encodeThinking(msg); v != "" {
				s.span.SetAttributes(attribute.String(AttrGenAIReasoning, v))
			}
		}
	}
	s.span.End()
}

// encodeMessages marshals a slice of schema.Message to a compact
// JSON string suitable for stuffing in a span attribute. Empty
// input returns "" so callers can branch on the result.
func encodeMessages(msgs []schema.Message) string {
	if len(msgs) == 0 {
		return ""
	}
	b, err := json.Marshal(msgs)
	if err != nil {
		return ""
	}
	return string(b)
}

// encodeMessage marshals the completion message to JSON, EXCLUDING any
// reasoning (thinking) parts — those are captured separately under
// gen_ai.reasoning so gen_ai.completion stays the clean answer. Used only
// for the completion attribute.
func encodeMessage(m schema.Message) string {
	if m.Role == "" && len(m.Content) == 0 && len(m.ToolCalls) == 0 {
		return ""
	}
	// Drop thinking parts so completion mirrors what downstream consumers
	// expect (the answer, no chain-of-thought).
	if hasThinking(m) {
		clean := m
		clean.Content = make([]schema.ContentPart, 0, len(m.Content))
		for _, p := range m.Content {
			if p.Type != schema.ContentTypeThinking {
				clean.Content = append(clean.Content, p)
			}
		}
		m = clean
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

// hasThinking reports whether m carries any reasoning part.
func hasThinking(m schema.Message) bool {
	for _, p := range m.Content {
		if p.Type == schema.ContentTypeThinking {
			return true
		}
	}
	return false
}

// encodeThinking marshals the reasoning text of every thinking part on
// m to a compact JSON string array, or "" when there is none. Kept
// separate from gen_ai.completion so reasoning can be captured (and
// queried) on its own.
func encodeThinking(m schema.Message) string {
	var parts []string
	for _, p := range m.Content {
		if p.Type == schema.ContentTypeThinking && p.Text != "" {
			parts = append(parts, p.Text)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	b, err := json.Marshal(parts)
	if err != nil {
		return ""
	}
	return string(b)
}
