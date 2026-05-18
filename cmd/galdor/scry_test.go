package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	// Open an empty DB by pointing at a fresh path.
	dir := t.TempDir()
	db := filepath.Join(dir, "empty.db")
	var out, errOut bytes.Buffer
	if code := scry(context.Background(), []string{"list", "--db", db}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "no runs recorded") {
		t.Errorf("out = %q", out.String())
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
}
