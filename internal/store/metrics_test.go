package store

import (
	"context"
	"testing"
)

// seedMetricsDB inserts a small but varied set of spans so the
// stats tests have something predictable to assert on.
func seedMetricsDB(t *testing.T, s *Store) {
	t.Helper()
	spans := []Span{
		// Root run span (no provider).
		{
			SpanID: "r1", TraceID: "t1", Name: "galdor.graph.run",
			StartTimeUnixNano: 0, EndTimeUnixNano: 1000,
			StatusCode: "ok", RunID: "run-A",
			Attributes: map[string]any{"galdor.run.id": "run-A"},
		},
		// Two anthropic provider spans, fast.
		{
			SpanID: "a1", TraceID: "t1", ParentSpanID: "r1",
			Name:              "galdor.provider.generate",
			StartTimeUnixNano: 10, EndTimeUnixNano: 110,
			StatusCode: "ok",
			Attributes: map[string]any{
				"galdor.provider.name":       "anthropic",
				"gen_ai.request.model":       "claude-haiku-4-5",
				"gen_ai.usage.input_tokens":  30,
				"gen_ai.usage.output_tokens": 7,
			},
		},
		{
			SpanID: "a2", TraceID: "t1", ParentSpanID: "r1",
			Name:              "galdor.provider.generate",
			StartTimeUnixNano: 120, EndTimeUnixNano: 220,
			StatusCode: "ok",
			Attributes: map[string]any{
				"galdor.provider.name":       "anthropic",
				"gen_ai.request.model":       "claude-haiku-4-5",
				"gen_ai.usage.input_tokens":  50,
				"gen_ai.usage.output_tokens": 12,
			},
		},
		// One openai span with longer latency + error.
		{
			SpanID: "o1", TraceID: "t1", ParentSpanID: "r1",
			Name:              "galdor.provider.generate",
			StartTimeUnixNano: 250, EndTimeUnixNano: 1250,
			StatusCode: "error", StatusMessage: "rate limited",
			Attributes: map[string]any{
				"galdor.provider.name": "openai",
				"gen_ai.request.model": "gpt-4o-mini",
			},
		},
		// A tool span (excluded from provider rollups).
		{
			SpanID: "tool1", TraceID: "t1", ParentSpanID: "r1",
			Name:              "galdor.tool.execute",
			StartTimeUnixNano: 200, EndTimeUnixNano: 240,
			StatusCode: "ok",
			Attributes: map[string]any{"gen_ai.tool.name": "math"},
		},
	}
	if err := s.InsertSpans(context.Background(), spans); err != nil {
		t.Fatal(err)
	}
}

func TestOverallStats(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	seedMetricsDB(t, s)

	stats, err := s.OverallStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.SpanCount != 5 {
		t.Errorf("SpanCount = %d, want 5", stats.SpanCount)
	}
	if stats.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", stats.ErrorCount)
	}
	if stats.InputTokens != 80 || stats.OutputTokens != 19 {
		t.Errorf("tokens in=%d out=%d, want 80/19", stats.InputTokens, stats.OutputTokens)
	}
	if stats.LatencyMaxNs != 1000 {
		t.Errorf("LatencyMaxNs = %d", stats.LatencyMaxNs)
	}
	if stats.LatencyP50Ns <= 0 || stats.LatencyP95Ns <= 0 {
		t.Errorf("percentiles = %d/%d/%d", stats.LatencyP50Ns, stats.LatencyP95Ns, stats.LatencyP99Ns)
	}
}

func TestStatsByProvider(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	seedMetricsDB(t, s)

	rows, err := s.StatsByProvider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	// Sorted alphabetically: anthropic, openai.
	if rows[0].Key != "anthropic" || rows[1].Key != "openai" {
		t.Errorf("keys = %q, %q", rows[0].Key, rows[1].Key)
	}
	if rows[0].SpanCount != 2 || rows[0].ErrorCount != 0 {
		t.Errorf("anthropic: %+v", rows[0])
	}
	if rows[1].SpanCount != 1 || rows[1].ErrorCount != 1 {
		t.Errorf("openai: %+v", rows[1])
	}
	if rows[0].InputTokens != 80 || rows[0].OutputTokens != 19 {
		t.Errorf("anthropic tokens = %d/%d", rows[0].InputTokens, rows[0].OutputTokens)
	}
}

func TestStatsByModel(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	seedMetricsDB(t, s)

	rows, err := s.StatsByModel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	keys := map[string]bool{rows[0].Key: true, rows[1].Key: true}
	if !keys["claude-haiku-4-5"] || !keys["gpt-4o-mini"] {
		t.Errorf("keys = %+v", keys)
	}
}

func TestStatsByProvider_EmptyStore(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	rows, err := s.StatsByProvider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %d", len(rows))
	}
}

// Regression for audit H13: SpansSince cursors on rowid (ingestion order),
// not start_time. A span ingested AFTER the cursor but with a LOWER start
// time — e.g. a long parent span that starts first but ends (and exports)
// last — must still be surfaced. A start-time cursor would miss it.
func TestSpansSince_RowidCursorSurfacesLateLowStartSpan(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()

	// First batch: a child span with a high start time.
	if err := s.InsertSpans(ctx, []Span{
		{SpanID: "child", TraceID: "t", Name: "child", StartTimeUnixNano: 500, EndTimeUnixNano: 600},
	}); err != nil {
		t.Fatal(err)
	}
	_, cursor, err := s.SpansSince(ctx, 0, 100) // consume it; advance the cursor
	if err != nil {
		t.Fatal(err)
	}

	// Second batch: the PARENT, ingested later but with a LOWER start time.
	if err = s.InsertSpans(ctx, []Span{
		{SpanID: "parent", TraceID: "t", Name: "parent", StartTimeUnixNano: 100, EndTimeUnixNano: 1000},
	}); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.SpansSince(ctx, cursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SpanID != "parent" {
		t.Fatalf("rowid cursor must surface a late-ingested low-start span (regression of H13), got %+v", got)
	}
}

func TestSpansSince_LimitClamps(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()
	spans := make([]Span, 5)
	for i := range spans {
		spans[i] = Span{
			SpanID: string(rune('a' + i)), TraceID: "t",
			Name: "n", StartTimeUnixNano: int64(i + 1), EndTimeUnixNano: int64(i + 2),
		}
	}
	if err := s.InsertSpans(ctx, spans); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.SpansSince(ctx, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("limit not honored: %d", len(got))
	}
}

func TestMaxSpanRowid(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()
	v, err := s.MaxSpanRowid(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v != 0 {
		t.Errorf("empty MaxSpanRowid = %d", v)
	}
	if insErr := s.InsertSpans(ctx, []Span{
		{SpanID: "a", TraceID: "t", Name: "n", StartTimeUnixNano: 42, EndTimeUnixNano: 50},
		{SpanID: "b", TraceID: "t", Name: "n", StartTimeUnixNano: 99, EndTimeUnixNano: 100},
	}); insErr != nil {
		t.Fatal(insErr)
	}
	v, err = s.MaxSpanRowid(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v != 2 { // two rows inserted -> max rowid 2
		t.Errorf("MaxSpanRowid = %d, want 2", v)
	}
}

func TestPercentile_EdgeCases(t *testing.T) {
	t.Parallel()
	if percentile(nil, 0.5) != 0 {
		t.Error("empty slice should return 0")
	}
	sorted := []int64{1, 2, 3, 4, 5}
	if percentile(sorted, 0.0) != 1 {
		t.Error("p0 should be min")
	}
	if percentile(sorted, 1.0) != 5 {
		t.Error("p100 should be max")
	}
	if percentile(sorted, 0.5) != 3 {
		t.Error("p50 of 1..5 should be 3")
	}
	// Out-of-range fractions clamp.
	if percentile(sorted, -1.0) != 1 {
		t.Error("negative should clamp to min")
	}
	if percentile(sorted, 2.0) != 5 {
		t.Error(">1 should clamp to max")
	}
}

func TestExtractAttrAndInt(t *testing.T) {
	t.Parallel()
	js := `{"a":"hello","b":42,"c":"with \"quotes\"","d":3.5}`
	if v, ok := extractAttr(js, "a"); !ok || v != "hello" {
		t.Errorf("a = %q, %v", v, ok)
	}
	if v, ok := extractAttr(js, "c"); !ok || v != `with "quotes"` {
		t.Errorf("c = %q, %v", v, ok)
	}
	if _, ok := extractAttr(js, "missing"); ok {
		t.Error("missing key should be not ok")
	}
	// extractAttr on a numeric value returns ok=false.
	if _, ok := extractAttr(js, "b"); ok {
		t.Error("numeric value should not match string extractor")
	}
	if n, ok := extractInt(js, "b"); !ok || n != 42 {
		t.Errorf("b = %d, %v", n, ok)
	}
	if n, ok := extractInt(js, "d"); !ok || n != 3 {
		t.Errorf("d = %d, %v (decimal portion stripped)", n, ok)
	}
}

func TestUnescapeJSONString(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"plain":          "plain",
		`with\"quotes\"`: `with"quotes"`,
		`line\nbreak`:    "line\nbreak",
		`tab\there`:      "tab\there",
		`back\\slash`:    `back\slash`,
	}
	for in, want := range cases {
		if got := unescapeJSONString(in); got != want {
			t.Errorf("unescape(%q) = %q, want %q", in, got, want)
		}
	}
}
