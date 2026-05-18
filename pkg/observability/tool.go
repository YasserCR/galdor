package observability

import (
	"context"
	"encoding/json"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/YasserCR/galdor/internal/jsonschema"
	"github.com/YasserCR/galdor/pkg/tool"
)

// InstrumentTool returns a tool.AnyTool that wraps t and emits a
// span around every ExecuteJSON call. Span attributes carry the
// tool name plus input / output byte sizes; errors are recorded
// via OpenTelemetry's RecordError + SetStatus(Error, ...).
//
// The wrapper exposes the same Name / Description / Schema as the
// underlying tool so registries can hold either form without code
// changes elsewhere.
func InstrumentTool(t tool.AnyTool, tracer trace.Tracer) tool.AnyTool {
	if t == nil {
		panic("observability: nil tool")
	}
	if tracer == nil {
		panic("observability: nil tracer")
	}
	return &instrumentedTool{inner: t, tracer: tracer}
}

// InstrumentRegistry returns a fresh registry whose tools are each
// wrapped through InstrumentTool. The original registry is left
// untouched. Returns an error only if the wrapped registry rejects
// a duplicate name (which the caller's own AnyTool implementations
// would have to produce — galdor's typedTool always uses the
// underlying tool's Name).
func InstrumentRegistry(r *tool.Registry, tracer trace.Tracer) (*tool.Registry, error) {
	if r == nil {
		panic("observability: nil registry")
	}
	if tracer == nil {
		panic("observability: nil tracer")
	}
	originals := r.Tools()
	wrapped := make([]tool.AnyTool, 0, len(originals))
	for _, t := range originals {
		wrapped = append(wrapped, InstrumentTool(t, tracer))
	}
	return tool.NewRegistry(wrapped...)
}

type instrumentedTool struct {
	inner  tool.AnyTool
	tracer trace.Tracer
}

func (i *instrumentedTool) Name() string               { return i.inner.Name() }
func (i *instrumentedTool) Description() string        { return i.inner.Description() }
func (i *instrumentedTool) Schema() *jsonschema.Schema { return i.inner.Schema() }

func (i *instrumentedTool) ExecuteJSON(ctx context.Context, raw json.RawMessage) (out json.RawMessage, err error) {
	ctx, span := i.tracer.Start(ctx, SpanToolExecute, trace.WithAttributes(
		attribute.String(AttrGenAIToolName, i.inner.Name()),
		attribute.Int(AttrGenAIToolInputSize, len(raw)),
	))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetAttributes(attribute.Int(AttrGenAIToolOutputSize, len(out)))
		}
		span.End()
	}()
	return i.inner.ExecuteJSON(ctx, raw)
}
