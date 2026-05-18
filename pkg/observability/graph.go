package observability

import (
	"context"
	"reflect"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/YasserCR/galdor/pkg/graph"
)

// TraceHooks returns a graph.Hooks[S] that emits one root span per
// run and one child span per node hop. The hooks are designed to be
// dropped straight into graph.RunOptions[S].Hooks:
//
//	hooks := observability.TraceHooks[MyState](tracer)
//	final, err := r.InvokeWith(ctx, initial, graph.RunOptions[MyState]{
//	    Hooks: hooks,
//	    RunID: "abc-123",
//	})
//
// Span layout:
//
//	galdor.graph.run               (BeforeRun → AfterRun)
//	├── galdor.graph.node          (BeforeNode → AfterNode for node 1)
//	├── galdor.graph.node          (...                       node 2)
//	└── galdor.graph.node          (...                       node N)
//
// Spans from InstrumentProvider and InstrumentTool nest inside the
// node span automatically because BeforeNode returns a ctx carrying
// the node span.
func TraceHooks[S any](tracer trace.Tracer) graph.Hooks[S] {
	if tracer == nil {
		panic("observability: nil tracer")
	}
	runSpanKey := &spanKey{}
	nodeSpanKey := &spanKey{}

	return graph.Hooks[S]{
		BeforeRun: func(ctx context.Context, runID string, _ S) context.Context {
			var zero S
			ctx, span := tracer.Start(ctx, SpanGraphRun, trace.WithAttributes(
				attribute.String(AttrGaldorRunID, runID),
				attribute.String(AttrGaldorStateGo, reflect.TypeOf(zero).String()),
			))
			return context.WithValue(ctx, runSpanKey, span)
		},
		AfterRun: func(ctx context.Context, _ string, _ S, err error) {
			span, ok := ctx.Value(runSpanKey).(trace.Span)
			if !ok || span == nil {
				return
			}
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			}
			span.End()
		},
		BeforeNode: func(ctx context.Context, runID, node string, step int, _ S) context.Context {
			ctx, span := tracer.Start(ctx, SpanGraphNode, trace.WithAttributes(
				attribute.String(AttrGaldorRunID, runID),
				attribute.String(AttrGaldorNode, node),
				attribute.Int(AttrGaldorStep, step),
			))
			return context.WithValue(ctx, nodeSpanKey, span)
		},
		AfterNode: func(ctx context.Context, _, _ string, _ int, _ S, err error) {
			span, ok := ctx.Value(nodeSpanKey).(trace.Span)
			if !ok || span == nil {
				return
			}
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			}
			span.End()
		},
	}
}

// spanKey is the ctx-key type. We make a fresh value per TraceHooks
// invocation so different hook sets don't collide when nested.
type spanKey struct{}
