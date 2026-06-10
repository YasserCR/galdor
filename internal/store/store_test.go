package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openTempStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(context.Background(), filepath.Join(dir, "traces.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpen_RejectsEmptyPath(t *testing.T) {
	t.Parallel()
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestStore_RoundTrip(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()

	start := time.Now().UnixNano()
	spans := []Span{
		{
			SpanID:            "root",
			TraceID:           "trace-1",
			ParentSpanID:      "",
			Name:              "galdor.graph.run",
			StartTimeUnixNano: start,
			EndTimeUnixNano:   start + 1_000_000,
			StatusCode:        "ok",
			Attributes:        map[string]any{"galdor.run.id": "r1", "galdor.state.type": "MyState"},
			RunID:             "r1",
		},
		{
			SpanID:            "child",
			TraceID:           "trace-1",
			ParentSpanID:      "root",
			Name:              "galdor.graph.node",
			StartTimeUnixNano: start + 100_000,
			EndTimeUnixNano:   start + 500_000,
			StatusCode:        "ok",
			Attributes:        map[string]any{"galdor.run.id": "r1", "galdor.node.name": "model"},
			RunID:             "r1",
		},
	}
	if err := s.InsertSpans(ctx, spans); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.SpanCount(ctx); n != 2 {
		t.Errorf("SpanCount = %d, want 2", n)
	}

	got, err := s.SpansForRun(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d spans, want 2", len(got))
	}
	if got[0].SpanID != "root" || got[1].SpanID != "child" {
		t.Errorf("order not preserved: %v", []string{got[0].SpanID, got[1].SpanID})
	}
	if got[0].Attributes["galdor.run.id"] != "r1" {
		t.Errorf("attrs not round-tripped: %+v", got[0].Attributes)
	}
}

// Regression for audit M20: a duplicate span_id must be ignored, not fail
// the whole batch — the OTel batcher doesn't retry, so failing the batch
// would drop a batch of unrelated spans.
func TestInsertSpans_DuplicateIgnoredNotBatchFailing(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()
	if err := s.InsertSpans(ctx, []Span{{SpanID: "dup", TraceID: "t", Name: "x", StartTimeUnixNano: 1, EndTimeUnixNano: 2}}); err != nil {
		t.Fatal(err)
	}
	// A batch with the duplicate AND a fresh span must succeed and land
	// the fresh one.
	batch := []Span{
		{SpanID: "dup", TraceID: "t", Name: "x", StartTimeUnixNano: 1, EndTimeUnixNano: 2},
		{SpanID: "fresh", TraceID: "t", Name: "y", StartTimeUnixNano: 3, EndTimeUnixNano: 4},
	}
	if err := s.InsertSpans(ctx, batch); err != nil {
		t.Fatalf("a duplicate span must not fail the batch (regression of M20): %v", err)
	}
	n, err := s.SpanCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 spans (dup ignored, fresh inserted), got %d", n)
	}
}

// Regression for audit M19: an in-memory store must pin the pool to a
// single connection, or concurrent queries hit a fresh (empty) connection
// and fail with "no such table".
func TestOpen_InMemoryConcurrentQueries(t *testing.T) {
	t.Parallel()
	s, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.SpanCount(context.Background()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("in-memory concurrent query failed (regression of M19): %v", err)
	}
}

func TestListRuns_AggregatesAndOrders(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()

	base := time.Now().UnixNano()
	// Two runs; r2 starts later so should come first when sorted DESC.
	spans := []Span{
		{SpanID: "a1", TraceID: "t1", Name: "n", StartTimeUnixNano: base, EndTimeUnixNano: base + 1000, RunID: "r1", StatusCode: "ok"},
		{SpanID: "a2", TraceID: "t1", Name: "n", StartTimeUnixNano: base + 50, EndTimeUnixNano: base + 500, RunID: "r1", StatusCode: "ok"},
		{SpanID: "b1", TraceID: "t2", Name: "n", StartTimeUnixNano: base + 1500, EndTimeUnixNano: base + 2000, RunID: "r2", StatusCode: "error"},
		// A span with no RunID: must NOT appear in ListRuns.
		{SpanID: "x1", TraceID: "t3", Name: "n", StartTimeUnixNano: base, EndTimeUnixNano: base + 10},
	}
	if err := s.InsertSpans(ctx, spans); err != nil {
		t.Fatal(err)
	}

	runs, err := s.ListRuns(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %d (%+v)", len(runs), runs)
	}
	if runs[0].RunID != "r2" {
		t.Errorf("first should be r2 (most recent), got %q", runs[0].RunID)
	}
	if runs[0].ErrorCount != 1 || runs[0].Status() != "error" {
		t.Errorf("r2 status = %s, errCount = %d", runs[0].Status(), runs[0].ErrorCount)
	}
	if runs[1].RunID != "r1" || runs[1].SpanCount != 2 {
		t.Errorf("r1 = %+v", runs[1])
	}
	if runs[1].Status() != "ok" {
		t.Errorf("r1 status = %s", runs[1].Status())
	}
}

func TestSpansForRun_EmptyRunIDRejected(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	if _, err := s.SpansForRun(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty runID")
	}
}

func TestInsertSpans_EmptySliceIsNoop(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	if err := s.InsertSpans(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestStore_CloseIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := Open(context.Background(), filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSpan_DurationAndRunDuration(t *testing.T) {
	t.Parallel()
	sp := Span{StartTimeUnixNano: 100, EndTimeUnixNano: 250}
	if sp.Duration() != 150 {
		t.Errorf("Span.Duration = %d", sp.Duration())
	}
	if (Span{}).Duration() != 0 {
		t.Error("zero span should report 0")
	}
	r := RunSummary{StartTimeUnixNano: 10, EndTimeUnixNano: 60}
	if r.Duration() != 50 {
		t.Errorf("Run.Duration = %d", r.Duration())
	}
	if (RunSummary{}).Duration() != 0 {
		t.Error("zero run should report 0")
	}
}

func TestNormalizeStatus(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"OK":      "ok",
		"Error":   "error",
		"":        "unset",
		"unknown": "unset",
	}
	for in, want := range cases {
		if got := normalizeStatus(in); got != want {
			t.Errorf("normalizeStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

// Regression for audit M24: when a runID is reused across traces,
// SpansForRun must resolve deterministically (the most recent trace), not
// an arbitrary one.
func TestSpansForRun_ReusedRunIDPicksLatestTrace(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()
	if err := s.InsertSpans(ctx, []Span{
		{SpanID: "old", TraceID: "tOld", Name: "galdor.graph.run", StartTimeUnixNano: 100, EndTimeUnixNano: 200, RunID: "shared"},
		{SpanID: "new", TraceID: "tNew", Name: "galdor.graph.run", StartTimeUnixNano: 900, EndTimeUnixNano: 1000, RunID: "shared"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.SpansForRun(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TraceID != "tNew" {
		t.Fatalf("reused runID must resolve to the latest trace (regression of M24), got %+v", got)
	}
}
