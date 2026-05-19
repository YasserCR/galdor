package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// openBenchStore opens a temp store + cleans up at test end.
// Reused across benchmarks so each one starts from an empty DB.
func openBenchStore(b *testing.B) *Store {
	b.Helper()
	dir := b.TempDir()
	s, err := Open(context.Background(), filepath.Join(dir, "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	return s
}

// makeSpans builds n synthetic spans for the same run. The shape
// matches what observability.SQLiteExporter actually writes. The
// spanPrefix keeps SpanIDs unique across benchmark iterations
// (the store enforces a UNIQUE constraint on SpanID).
func makeSpans(runID, spanPrefix string, n int) []Span {
	spans := make([]Span, n)
	for i := range spans {
		spans[i] = Span{
			SpanID:            fmt.Sprintf("%s-s%d", spanPrefix, i),
			TraceID:           "t1",
			ParentSpanID:      "",
			Name:              "galdor.provider.generate",
			StartTimeUnixNano: int64(i) * 1_000_000,
			EndTimeUnixNano:   int64(i)*1_000_000 + 100,
			StatusCode:        "ok",
			RunID:             runID,
			Attributes: map[string]any{
				"galdor.run.id":              runID,
				"gen_ai.request.model":       "claude-haiku-4-5",
				"gen_ai.usage.input_tokens":  float64(42),
				"gen_ai.usage.output_tokens": float64(7),
			},
		}
	}
	return spans
}

// BenchmarkInsertSpans_1 measures the single-span insert cost —
// the floor for what InsertSpans adds per span.
func BenchmarkInsertSpans_1(b *testing.B) {
	s := openBenchStore(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		prefix := fmt.Sprintf("r%d", i)
		spans := makeSpans(prefix, prefix, 1)
		if err := s.InsertSpans(ctx, spans); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkInsertSpans_100 measures the cost of a batched insert
// (100 spans per call). Normalized by ns/op divided by 100 = the
// amortized per-span cost in a realistic batch.
func BenchmarkInsertSpans_100(b *testing.B) {
	s := openBenchStore(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		prefix := fmt.Sprintf("r%d", i)
		spans := makeSpans(prefix, prefix, 100)
		if err := s.InsertSpans(ctx, spans); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSpansForRun_1k measures the read side: how fast can we
// reconstruct a run's full span list from disk. 1000 spans is a
// realistic upper bound for a single moderately-sized agent run.
func BenchmarkSpansForRun_1k(b *testing.B) {
	s := openBenchStore(b)
	ctx := context.Background()
	const runID = "bench-run"
	if err := s.InsertSpans(ctx, makeSpans(runID, "seed", 1000)); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := s.SpansForRun(ctx, runID)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) != 1000 {
			b.Fatalf("got %d, want 1000", len(got))
		}
	}
}
