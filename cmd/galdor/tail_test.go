package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YasserCR/galdor/internal/store"
)

// skipIfTimingSensitive bails out on Windows. The tail tests poll
// on millisecond intervals and assert that newly-inserted spans
// show up within a few hundred milliseconds. Windows' scheduler
// quantum and SQLite-on-Windows latency make those assertions
// non-deterministic enough that CI fails maybe once every few
// runs; behavior is identical to Linux/macOS, the *test* is the
// fragile part.
func skipIfTimingSensitive(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("skipping timing-sensitive tail test on windows; behavior is platform-independent, only the test is fragile")
	}
}

// tailDB sets up an empty DB and returns its path. The test then
// inserts spans concurrently while `scry tail` is polling.
func tailDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tail.db")
	// Touch the DB so MaxSpanStart returns 0 cleanly.
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScryTail_PicksUpNewSpan(t *testing.T) {
	t.Parallel()
	skipIfTimingSensitive(t)
	path := tailDB(t)

	// Start tail in a goroutine, capped at a few iterations so the
	// test cannot hang.
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = scryTail(ctx, []string{
			"--db", path,
			"--interval", "20ms",
			"--_max-iterations", "10",
		}, &out, &errOut)
	}()

	// Give tail one tick to start, then write a span.
	time.Sleep(40 * time.Millisecond)
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	now := time.Now().UnixNano()
	if err := s.InsertSpans(context.Background(), []store.Span{{
		SpanID:            "s1",
		TraceID:           "t1",
		Name:              "galdor.graph.run",
		StartTimeUnixNano: now,
		EndTimeUnixNano:   now + 50_000,
		StatusCode:        "ok",
		Attributes:        map[string]any{"galdor.run.id": "tail-run"},
		RunID:             "tail-run",
	}}); err != nil {
		t.Fatal(err)
	}

	wg.Wait()
	got := out.String()
	for _, want := range []string{"tail-run", "galdor.graph.run"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in tail output:\n%s", want, got)
		}
	}
}

func TestScryTail_JSONOutput(t *testing.T) {
	t.Parallel()
	skipIfTimingSensitive(t)
	path := tailDB(t)
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = scryTail(ctx, []string{
			"--db", path,
			"--interval", "20ms",
			"--format", "json",
			"--_max-iterations", "10",
		}, &out, &errOut)
	}()

	time.Sleep(40 * time.Millisecond)
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.InsertSpans(context.Background(), []store.Span{{
		SpanID: "s2", TraceID: "t2", Name: "x",
		StartTimeUnixNano: time.Now().UnixNano(),
		EndTimeUnixNano:   time.Now().UnixNano() + 1000,
		StatusCode:        "ok",
	}}); err != nil {
		t.Fatal(err)
	}

	wg.Wait()
	// Decode each line — there might be more than one as the cursor
	// catches up.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		var sp store.Span
		if err := json.Unmarshal([]byte(line), &sp); err != nil {
			t.Errorf("not valid JSON: %s\n%v", line, err)
		}
	}
}

func TestScryTail_RejectsTooSmallInterval(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	if code := scryTail(context.Background(), []string{"--interval", "1ms"}, &out, &errOut); code != 64 {
		t.Errorf("code = %d", code)
	}
	if !strings.Contains(errOut.String(), "interval too small") {
		t.Errorf("errOut = %q", errOut.String())
	}
}

func TestScryTail_ContextCancelExits(t *testing.T) {
	skipIfTimingSensitive(t)
	t.Parallel()
	path := tailDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	var out, errOut bytes.Buffer

	done := make(chan int, 1)
	go func() {
		done <- scryTail(ctx, []string{"--db", path, "--interval", "20ms"}, &out, &errOut)
	}()
	time.Sleep(40 * time.Millisecond)
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("code = %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scry tail did not exit on ctx cancel")
	}
}
