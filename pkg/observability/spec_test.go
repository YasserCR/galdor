package observability

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/YasserCR/galdor/pkg/graph"
)

// newTracerForExporter wires a TracerProvider that flushes through
// exp synchronously. Tests use it when they need to assert that
// spans land in the same SQLite store the test queries later.
func newTracerForExporter(exp *SQLiteExporter) *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
}

func TestRecordGraphSpec_PersistsOnBeforeRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exp, err := NewSQLiteExporter(filepath.Join(dir, "spans.db"),
		WithCheckpointInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exp.Shutdown(context.Background()) })

	type S struct{ N int }
	r, err := graph.New[S]().
		AddNode("inc", func(_ context.Context, s S) (S, error) { s.N++; return s, nil }).
		AddEdge(graph.START, "inc").
		AddEdge("inc", graph.END).
		Compile()
	if err != nil {
		t.Fatal(err)
	}

	hooks := RecordGraphSpec[S](exp, r)
	if _, invErr := r.InvokeWith(context.Background(), S{}, graph.RunOptions[S]{
		RunID: "spec-run",
		Hooks: hooks,
	}); invErr != nil {
		t.Fatal(invErr)
	}

	got, err := exp.Store().GetGraphSpec(context.Background(), "spec-run")
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("expected spec to be persisted")
	}
	if !strings.Contains(got, `"entry":"inc"`) {
		t.Errorf("spec missing entry: %s", got)
	}
	if !strings.Contains(got, `"inc"`) {
		t.Errorf("spec missing node: %s", got)
	}
}

func TestRecordGraphSpec_ComposesWithTraceHooks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exp, err := NewSQLiteExporter(filepath.Join(dir, "spans.db"),
		WithCheckpointInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exp.Shutdown(context.Background()) })

	// Route TraceHooks spans through the same exporter so we can
	// assert both effects (spans persisted + spec persisted) against
	// one store.
	tp := newTracerForExporter(exp)
	defer func() { _ = tp.Shutdown(context.Background()) }()
	tracer := tp.Tracer("test")

	type S struct{}
	r, err := graph.New[S]().
		AddNode("a", func(_ context.Context, s S) (S, error) { return s, nil }).
		AddEdge(graph.START, "a").
		AddEdge("a", graph.END).
		Compile()
	if err != nil {
		t.Fatal(err)
	}

	hooks := graph.MergeHooks(
		TraceHooks[S](tracer),
		RecordGraphSpec[S](exp, r),
	)
	if _, invErr := r.InvokeWith(context.Background(), S{}, graph.RunOptions[S]{
		RunID: "merged-run",
		Hooks: hooks,
	}); invErr != nil {
		t.Fatal(invErr)
	}

	got, _ := exp.Store().GetGraphSpec(context.Background(), "merged-run")
	if got == "" {
		t.Error("merged hooks did not record spec")
	}
	if n, _ := exp.Store().SpanCount(context.Background()); n == 0 {
		t.Error("merged hooks did not emit spans")
	}
}
