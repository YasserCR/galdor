package observability

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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

// emitManySpans writes a configurable number of small spans into
// the exporter, enough to grow the -wal noticeably. Used by the
// checkpoint tests.
func emitManySpans(t *testing.T, exp sdktrace.SpanExporter, n int, runID string) {
	t.Helper()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	tr := tp.Tracer("test")
	for i := 0; i < n; i++ {
		_, sp := tr.Start(context.Background(), "galdor.graph.node",
			trace.WithAttributes(attribute.String(AttrGaldorRunID, runID)),
		)
		sp.End()
	}
	_ = tp.ForceFlush(context.Background())
}

// TestSQLiteExporter_PeriodicCheckpointShrinksWAL verifies the
// background goroutine is actually folding the -wal sidecar back
// into the main .db file. Without this, daemons that hold the
// exporter open forever would silently accumulate spans in -wal
// while the dashboard reads an empty .db. Retro feedback #2.
func TestSQLiteExporter_PeriodicCheckpointShrinksWAL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "spans.db")
	exp, err := NewSQLiteExporter(dbPath, WithCheckpointInterval(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exp.Shutdown(context.Background()) })

	emitManySpans(t, exp, 50, "wal-test")

	// Give the checkpointer two ticks to fold the -wal into the
	// main file. The empty SQLite header is ~4 KB; after the
	// checkpoint, 50 spans should push it well past 8 KB.
	time.Sleep(200 * time.Millisecond)

	st, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() < 8*1024 {
		t.Errorf("main .db size = %d, expected > 8 KB after periodic checkpoint", st.Size())
	}
}

// TestSQLiteExporter_ShutdownTruncatesWAL verifies the final
// TRUNCATE checkpoint in Shutdown leaves the .wal file empty
// (or absent). Without this, deploy artifacts include a stale
// -wal that confuses out-of-process readers.
func TestSQLiteExporter_ShutdownTruncatesWAL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "spans.db")
	exp, err := NewSQLiteExporter(dbPath, WithCheckpointInterval(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	emitManySpans(t, exp, 20, "trunc-test")
	if shutErr := exp.Shutdown(context.Background()); shutErr != nil {
		t.Fatal(shutErr)
	}

	// After Shutdown the -wal should be 0 bytes (or absent — modernc
	// keeps a 0-byte file).
	wal, err := os.Stat(dbPath + "-wal")
	if err == nil && wal.Size() > 0 {
		t.Errorf(".db-wal size = %d after Shutdown, expected 0", wal.Size())
	}
}

// TestSQLiteExporter_CheckpointDisabled verifies that passing
// WithCheckpointInterval(0) keeps the goroutine from starting
// (advanced opt-out for callers who run their own checkpointer).
func TestSQLiteExporter_CheckpointDisabled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exp, err := NewSQLiteExporter(filepath.Join(dir, "spans.db"), WithCheckpointInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	if exp.ckptDone != nil {
		t.Error("checkpoint goroutine should not be started when interval is 0")
	}
	if err := exp.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Regression for audit M22: the documented default lives at
// ~/.galdor/traces.db, whose parent dir doesn't exist on a fresh
// machine. NewSQLiteExporter must create the missing parent dir rather
// than fail with a cryptic "unable to open database file (14)".
func TestNewSQLiteExporter_CreatesMissingParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "traces.db")
	exp, err := NewSQLiteExporter(path)
	if err != nil {
		t.Fatalf("exporter should create the missing parent dir, got: %v", err)
	}
	defer func() { _ = exp.Shutdown(context.Background()) }()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("db not created at %s: %v", path, err)
	}
}

// Regression for audit M21: Shutdown must wait for in-flight ExportSpans
// before closing the DB. Run under -race: concurrent Export + Shutdown.
func TestSQLiteExporter_ConcurrentExportAndShutdown(t *testing.T) {
	exp, err := NewSQLiteExporter(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = exp.ExportSpans(context.Background(), nil) }()
	}
	_ = exp.Shutdown(context.Background())
	wg.Wait()
}
