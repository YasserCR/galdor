package ui

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YasserCR/galdor/internal/store"
)

func TestStreamRuns_EmitsNewRunAfterInsert(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := store.Open(context.Background(), filepath.Join(dir, "traces.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	srv, err := NewServer(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// Insert one run AFTER the stream connects: short interval keeps
	// the test fast; max-iterations caps the stream at 4 ticks so
	// the goroutine always exits.
	url := ts.URL + "/api/stream/runs?interval=50ms&_max-iterations=10"
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q", ct)
	}

	// Drive the producer: insert a run shortly after subscribing.
	var insertOnce sync.Once
	insert := func() {
		insertOnce.Do(func() {
			_ = s.InsertSpans(context.Background(), []store.Span{
				{
					SpanID: "r-live", TraceID: "t-live", Name: "galdor.graph.run",
					StartTimeUnixNano: time.Now().UnixNano(),
					EndTimeUnixNano:   time.Now().Add(1 * time.Millisecond).UnixNano(),
					StatusCode:        "ok",
					RunID:             "run-live",
					Attributes:        map[string]any{"galdor.run.id": "run-live"},
				},
			})
		})
	}

	sawRun := false
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: heartbeat") {
			// First heartbeat confirms the loop is live; insert now.
			insert()
		}
		if strings.HasPrefix(line, "event: run") {
			sawRun = true
		}
		if strings.HasPrefix(line, "data: ") && sawRun && strings.Contains(line, `"RunID":"run-live"`) {
			cancel()
			break
		}
	}
	if !sawRun {
		t.Fatal("never received `event: run` for the new insert")
	}
}

func TestStreamRuns_HeartbeatWithoutData(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := store.Open(context.Background(), filepath.Join(dir, "traces.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	srv, err := NewServer(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	// Drain after 3 iterations so the test isn't open-ended.
	req := httptest.NewRequest(http.MethodGet, "/api/stream/runs?interval=10ms&_max-iterations=3", nil)
	srv.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "event: heartbeat") {
		t.Errorf("expected at least one heartbeat; body=%q", body)
	}
	if strings.Contains(body, "event: run") {
		t.Errorf("should not emit `run` event for empty store; body=%q", body)
	}
}

func TestParsePositiveDuration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", time.Second},
		{"500ms", 500 * time.Millisecond},
		{"2s", 2 * time.Second},
		{"250", 250 * time.Millisecond},
		{"-5s", time.Second},
		{"abc", time.Second},
	}
	for _, c := range cases {
		if got := parsePositiveDuration(c.in, time.Second); got != c.want {
			t.Errorf("parsePositiveDuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestBuildTimeline(t *testing.T) {
	t.Parallel()
	spans := []store.Span{
		{SpanID: "a", Name: "root", StartTimeUnixNano: 0, EndTimeUnixNano: 1000, StatusCode: "ok"},
		{SpanID: "b", Name: "child1", ParentSpanID: "a", StartTimeUnixNano: 100, EndTimeUnixNano: 500, StatusCode: "ok"},
		{SpanID: "c", Name: "child2", ParentSpanID: "a", StartTimeUnixNano: 600, EndTimeUnixNano: 900, StatusCode: "error"},
	}
	tl := buildTimeline(spans)
	if len(tl.Bars) != 3 {
		t.Fatalf("bars = %d", len(tl.Bars))
	}
	if tl.Bars[0].SpanID != "a" {
		t.Errorf("first bar should be root; got %s", tl.Bars[0].SpanID)
	}
	if tl.Width <= 0 || tl.Height <= 0 {
		t.Errorf("dimensions = %dx%d", tl.Width, tl.Height)
	}
	// The error child should be marked unhappy.
	for _, b := range tl.Bars {
		if b.SpanID == "c" && b.OK {
			t.Errorf("error span should not be marked OK")
		}
	}
}

func TestBuildTimeline_Empty(t *testing.T) {
	t.Parallel()
	tl := buildTimeline(nil)
	if len(tl.Bars) != 0 || tl.Height != 0 {
		t.Errorf("empty timeline: bars=%d height=%d", len(tl.Bars), tl.Height)
	}
}

func TestRunPage_RendersTimelineSVG(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runs/run-happy", nil))
	body := rec.Body.String()
	for _, want := range []string{`<svg `, `class="bar`, `class="timeline"`} {
		if !strings.Contains(body, want) {
			t.Errorf("run page missing %q", want)
		}
	}
}

func TestStaticLiveJS(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/live.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "EventSource") {
		t.Errorf("live.js body unexpected: %s", rec.Body.String()[:100])
	}
}
