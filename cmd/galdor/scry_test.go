package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/internal/store"
)

// seedDB creates a temporary DB file and populates it with a tiny
// run + two child spans. Returns the file path.
func seedDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "traces.db")
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	spans := []store.Span{
		{
			SpanID: "root", TraceID: "t", ParentSpanID: "",
			Name:              "galdor.graph.run",
			StartTimeUnixNano: 1_000_000_000,
			EndTimeUnixNano:   1_500_000_000,
			StatusCode:        "ok",
			Attributes:        map[string]any{"galdor.run.id": "run-A"},
			RunID:             "run-A",
		},
		{
			SpanID: "node1", TraceID: "t", ParentSpanID: "root",
			Name:              "galdor.graph.node",
			StartTimeUnixNano: 1_100_000_000,
			EndTimeUnixNano:   1_300_000_000,
			StatusCode:        "ok",
			Attributes: map[string]any{
				"galdor.run.id":    "run-A",
				"galdor.node.name": "model",
			},
			RunID: "run-A",
		},
		{
			SpanID: "gen1", TraceID: "t", ParentSpanID: "node1",
			Name:              "galdor.provider.generate",
			StartTimeUnixNano: 1_150_000_000,
			EndTimeUnixNano:   1_280_000_000,
			StatusCode:        "ok",
			Attributes: map[string]any{
				"galdor.run.id":              "run-A",
				"galdor.provider.name":       "anthropic",
				"gen_ai.usage.input_tokens":  float64(30),
				"gen_ai.usage.output_tokens": float64(7),
			},
			RunID: "run-A",
		},
	}
	if err := s.InsertSpans(context.Background(), spans); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScry_Usage(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	if code := scry(context.Background(), nil, &out, &errOut); code != 64 {
		t.Errorf("code = %d", code)
	}
	if !strings.Contains(errOut.String(), "galdor scry") {
		t.Errorf("errOut = %q", errOut.String())
	}
}

func TestScry_HelpExits0(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	if code := scry(context.Background(), []string{"help"}, &out, &errOut); code != 0 {
		t.Errorf("code = %d", code)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Errorf("out = %q", out.String())
	}
}

func TestScry_UnknownSubcommand(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	if code := scry(context.Background(), []string{"weird"}, &out, &errOut); code != 64 {
		t.Errorf("code = %d", code)
	}
	if !strings.Contains(errOut.String(), "unknown subcommand") {
		t.Errorf("errOut = %q", errOut.String())
	}
}

func TestScry_ListText(t *testing.T) {
	t.Parallel()
	db := seedDB(t)
	var out, errOut bytes.Buffer
	code := scry(context.Background(), []string{"list", "--db", db}, &out, &errOut)
	if code != 0 {
		t.Fatalf("code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "run-A") {
		t.Errorf("out = %q", out.String())
	}
	if !strings.Contains(out.String(), "RUN ID") {
		t.Errorf("missing header: %q", out.String())
	}
}

func TestScry_ListJSON(t *testing.T) {
	t.Parallel()
	db := seedDB(t)
	var out, errOut bytes.Buffer
	if code := scry(context.Background(), []string{"list", "--db", db, "--format", "json"}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d, err = %s", code, errOut.String())
	}
	var runs []store.RunSummary
	if err := json.Unmarshal(out.Bytes(), &runs); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if len(runs) != 1 || runs[0].RunID != "run-A" {
		t.Errorf("runs = %+v", runs)
	}
}

func TestScry_ListEmpty(t *testing.T) {
	t.Parallel()
	// An existing but empty DB shows "no runs". (A non-existent path is a
	// different case — see TestScry_MissingDBErrors.)
	db := filepath.Join(t.TempDir(), "empty.db")
	s, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	var out, errOut bytes.Buffer
	if code := scry(context.Background(), []string{"list", "--db", db}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "no runs recorded") {
		t.Errorf("out = %q", out.String())
	}
}

// Regression for audit M22: a mistyped --db must NOT silently create an
// empty database and report "no runs" — a read command must fail clearly
// and leave no file behind.
func TestScry_MissingDBErrors(t *testing.T) {
	t.Parallel()
	db := filepath.Join(t.TempDir(), "does-not-exist.db")
	var out, errOut bytes.Buffer
	code := scry(context.Background(), []string{"list", "--db", db}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected non-zero exit for a missing DB, got 0 (regression of M22); out=%q", out.String())
	}
	if !strings.Contains(errOut.String(), "does not exist") {
		t.Errorf("expected a 'does not exist' error, got: %s", errOut.String())
	}
	if _, err := os.Stat(db); err == nil {
		t.Error("a read command created the database file (regression of M22)")
	}
}

func TestScry_ShowTree(t *testing.T) {
	t.Parallel()
	db := seedDB(t)
	var out, errOut bytes.Buffer
	if code := scry(context.Background(), []string{"show", "--db", db, "run-A"}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d, err = %s", code, errOut.String())
	}
	got := out.String()
	for _, want := range []string{
		"run run-A",
		"galdor.graph.run",
		"galdor.graph.node",
		"galdor.provider.generate",
		"node=model",
		"provider=anthropic",
		"in=30",
		"out=7",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in tree output:\n%s", want, got)
		}
	}
}

func TestScry_ShowJSON(t *testing.T) {
	t.Parallel()
	db := seedDB(t)
	var out, errOut bytes.Buffer
	if code := scry(context.Background(), []string{"show", "--db", db, "--format", "json", "run-A"}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d, err = %s", code, errOut.String())
	}
	var spans []store.Span
	if err := json.Unmarshal(out.Bytes(), &spans); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(spans) != 3 {
		t.Errorf("spans = %d", len(spans))
	}
}

// Regression for audit H14: flags placed AFTER the <run-id> must be
// honored. The documented usage is `scry show <run-id> [--db PATH]`, but
// stdlib flag stops at the first positional, so before the fix `--db`
// here was silently ignored and the command read the default database
// (the wrong one) instead of the seeded temp DB.
func TestScry_ShowFlagsAfterRunID(t *testing.T) {
	t.Parallel()
	db := seedDB(t)
	var out, errOut bytes.Buffer
	// run-id first, then the flags — the shape the usage string documents.
	code := scry(context.Background(), []string{"show", "run-A", "--db", db, "--format", "json"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("code = %d, err = %s (flags after run-id were dropped — regression of H14)", code, errOut.String())
	}
	var spans []store.Span
	if err := json.Unmarshal(out.Bytes(), &spans); err != nil {
		t.Fatalf("json: %v (output: %s)", err, out.String())
	}
	if len(spans) != 3 {
		t.Errorf("spans = %d, want 3 (did --db point at the seeded DB?)", len(spans))
	}
}

func TestScry_ShowMissingRunID(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	if code := scry(context.Background(), []string{"show"}, &out, &errOut); code != 64 {
		t.Errorf("code = %d", code)
	}
}

func TestScry_ShowUnknownRun(t *testing.T) {
	t.Parallel()
	db := seedDB(t)
	var out, errOut bytes.Buffer
	if code := scry(context.Background(), []string{"show", "--db", db, "missing-run"}, &out, &errOut); code != 1 {
		t.Errorf("code = %d", code)
	}
}

func TestResolveDBPath_FlagWins(t *testing.T) {
	t.Setenv("GALDOR_DB", "/should/be/ignored")
	got, err := resolveDBPath("/explicit/path.db")
	if err != nil || got != "/explicit/path.db" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestResolveDBPath_EnvFallback(t *testing.T) {
	t.Setenv("GALDOR_DB", "/from/env.db")
	got, err := resolveDBPath("")
	if err != nil || got != "/from/env.db" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestFormatDuration_Buckets(t *testing.T) {
	t.Parallel()
	cases := map[int64]string{
		0:             "—",
		500:           "500ns",
		1_500:         "1.5µs",
		1_500_000:     "1.5ms",
		1_500_000_000: "1.50s",
	}
	for in, want := range cases {
		if got := formatDuration(in); got != want {
			t.Errorf("formatDuration(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()
	if truncate("short", 10) != "short" {
		t.Error("short string unchanged")
	}
	if got := truncate("aaaaaaaaaa", 5); got != "aaaa…" {
		t.Errorf("got %q", got)
	}
	// Guards the replay fingerprint path: a failed Fingerprint() can
	// yield "" (or a sub-12 string), which a raw [:12] slice would
	// panic on. truncate must return it unchanged without panicking.
	if got := truncate("", 12); got != "" {
		t.Errorf("empty input: got %q, want \"\"", got)
	}
	if got := truncate("abc", 12); got != "abc" {
		t.Errorf("sub-n input: got %q, want \"abc\"", got)
	}
}
