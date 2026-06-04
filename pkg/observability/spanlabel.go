package observability

import "context"

// spanLabelCtxKey is the context-key type. Unexported so callers must
// go through WithSpanLabel / SpanLabelFromContext.
type spanLabelCtxKey struct{}

// WithSpanLabel returns a ctx carrying a human-readable label. The
// instrumented provider (InstrumentProvider) and tool (InstrumentTool)
// stamp it on each span they create as the `galdor.span.label`
// attribute, which the dashboard timeline and the scry CLI show next to
// the otherwise-generic span type. That lets you tell apart, say, two
// `provider.generate` calls ("draft" vs "critique") straight from the
// timeline — without opening each span.
//
// The label is context-scoped, exactly like WithRunID: every
// instrumented span created under the returned ctx (and its
// descendants) inherits it, so you can set it per node, per step, or
// per individual call. An empty label returns ctx unchanged.
func WithSpanLabel(ctx context.Context, label string) context.Context {
	if label == "" {
		return ctx
	}
	return context.WithValue(ctx, spanLabelCtxKey{}, label)
}

// SpanLabelFromContext returns the label carried by ctx, or "" if none
// is set.
func SpanLabelFromContext(ctx context.Context) string {
	v, _ := ctx.Value(spanLabelCtxKey{}).(string)
	return v
}
