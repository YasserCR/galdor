package observability

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func newSQLiteExporter(t *testing.T) *SQLiteExporter {
	t.Helper()
	dir := t.TempDir()
	exp, err := NewSQLiteExporter(filepath.Join(dir, "spans.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exp.Shutdown(context.Background()) })
	return exp
}

func TestSQLiteExporter_OpenAndShutdownIdempotent(t *testing.T) {
	t.Parallel()
	exp := newSQLiteExporter(t)
	if err := exp.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := exp.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteExporter_RejectsAfterShutdown(t *testing.T) {
	t.Parallel()
	exp := newSQLiteExporter(t)
	if err := exp.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := exp.ExportSpans(context.Background(), nil)
	if !errors.Is(err, ErrExporterShutdown) {
		t.Fatalf("err = %v, want ErrExporterShutdown", err)
	}
}

func TestSQLiteExporter_EmptyBatchIsNoop(t *testing.T) {
	t.Parallel()
	exp := newSQLiteExporter(t)
	if err := exp.ExportSpans(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	n, _ := exp.Store().SpanCount(context.Background())
	if n != 0 {
		t.Errorf("SpanCount = %d", n)
	}
}

// emitSampleSpans runs a tracer with the given exporter and emits
// a parent + child span so end-to-end conversion is exercised.
// Spans are flushed synchronously (no tp.Shutdown — that would also
// shut the exporter down, and the caller still wants to query it).
func emitSampleSpans(t *testing.T, exp sdktrace.SpanExporter) (parentSpanID, childSpanID string) {
	t.Helper()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	tr := tp.Tracer("test")

	ctx, parent := tr.Start(context.Background(), "galdor.graph.run",
		trace.WithAttributes(
			attribute.String(AttrGaldorRunID, "run-x"),
			attribute.String(AttrGaldorStateGo, "MyState"),
		),
	)
	_, child := tr.Start(ctx, "galdor.graph.node",
		trace.WithAttributes(
			attribute.String(AttrGaldorRunID, "run-x"),
			attribute.String(AttrGaldorNode, "model"),
			attribute.Int(AttrGaldorStep, 1),
		),
	)
	child.End()
	parent.End()
	_ = tp.ForceFlush(context.Background())
	return parent.SpanContext().SpanID().String(), child.SpanContext().SpanID().String()
}

func TestSQLiteExporter_EndToEnd(t *testing.T) {
	t.Parallel()
	exp := newSQLiteExporter(t)
	_, _ = emitSampleSpans(t, exp)

	n, err := exp.Store().SpanCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("SpanCount = %d, want 2", n)
	}

	runs, err := exp.Store().ListRuns(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != "run-x" || runs[0].SpanCount != 2 {
		t.Fatalf("runs = %+v", runs)
	}

	spans, err := exp.Store().SpansForRun(context.Background(), "run-x")
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 2 {
		t.Fatalf("spans = %d", len(spans))
	}
	// Root span (galdor.graph.run) starts first.
	if spans[0].Name != "galdor.graph.run" {
		t.Errorf("first span = %q, want root", spans[0].Name)
	}
	// Child should reference parent.
	if spans[1].ParentSpanID == "" {
		t.Error("child should have parent span id")
	}
	if v, ok := spans[1].Attributes[AttrGaldorNode].(string); !ok || v != "model" {
		t.Errorf("missing/wrong galdor.node.name attr: %+v", spans[1].Attributes)
	}
}

func TestNewSQLiteExporter_RejectsEmptyPath(t *testing.T) {
	t.Parallel()
	if _, err := NewSQLiteExporter(""); err == nil {
		t.Fatal("expected error")
	}
}

func TestAttrValueToGo_PrimitiveTypes(t *testing.T) {
	t.Parallel()
	if got, ok := attrValueToGo(attribute.StringValue("hi")); !ok || got != "hi" {
		t.Errorf("string: %v, %v", got, ok)
	}
	if got, ok := attrValueToGo(attribute.BoolValue(true)); !ok || got != true {
		t.Errorf("bool: %v, %v", got, ok)
	}
	if got, ok := attrValueToGo(attribute.Int64Value(42)); !ok || got != int64(42) {
		t.Errorf("int64: %v, %v", got, ok)
	}
	if got, ok := attrValueToGo(attribute.Float64Value(1.5)); !ok || got != 1.5 {
		t.Errorf("float64: %v, %v", got, ok)
	}
}

func TestAttrValueToGo_SliceTypes(t *testing.T) {
	t.Parallel()
	got, ok := attrValueToGo(attribute.StringSliceValue([]string{"a", "b"}))
	if !ok {
		t.Fatal("not ok")
	}
	if ss, ok := got.([]string); !ok || len(ss) != 2 || ss[0] != "a" {
		t.Errorf("got %v", got)
	}
}

func TestAttrValueToGo_InvalidDropped(t *testing.T) {
	t.Parallel()
	if _, ok := attrValueToGo(attribute.Value{}); ok {
		t.Error("invalid attribute should be dropped")
	}
}
