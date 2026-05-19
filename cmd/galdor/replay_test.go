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

// seedReplayDB creates a store seeded with one provider.generate
// span whose prompt + completion are captured, so the replay
// loader has everything it needs.
func seedReplayDB(t *testing.T, runID string, captureContent bool) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "traces.db")
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	attrs := map[string]any{
		"galdor.run.id":                  runID,
		"galdor.provider.name":           "scripted",
		"gen_ai.request.model":           "demo-1",
		"gen_ai.response.model":          "demo-1",
		"gen_ai.response.finish_reasons": "end_turn",
		"gen_ai.usage.input_tokens":      float64(8),
		"gen_ai.usage.output_tokens":     float64(3),
	}
	if captureContent {
		attrs["gen_ai.prompt"] = `[{"role":"user","content":[{"type":"text","text":"hi"}]}]`
		attrs["gen_ai.completion"] = `{"role":"assistant","content":[{"type":"text","text":"hello"}]}`
	}

	spans := []store.Span{
		{
			SpanID: "root", TraceID: "t", ParentSpanID: "",
			Name:              "galdor.graph.run",
			StartTimeUnixNano: 1_000_000_000,
			EndTimeUnixNano:   1_500_000_000,
			StatusCode:        "ok",
			Attributes:        map[string]any{"galdor.run.id": runID},
			RunID:             runID,
		},
		{
			SpanID: "gen1", TraceID: "t", ParentSpanID: "root",
			Name:              "galdor.provider.generate",
			StartTimeUnixNano: 1_100_000_000,
			EndTimeUnixNano:   1_300_000_000,
			StatusCode:        "ok",
			Attributes:        attrs,
			RunID:             runID,
		},
	}
	if err := s.InsertSpans(context.Background(), spans); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScryReplay_Summary(t *testing.T) {
	t.Parallel()
	db := seedReplayDB(t, "rec-1", true)
	var out, errOut bytes.Buffer
	code := scry(context.Background(), []string{"replay", "--db", db, "rec-1"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("code = %d, errOut = %q", code, errOut.String())
	}
	s := out.String()
	for _, want := range []string{"rec-1", "calls: 1", "demo-1", "hello"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q in:\n%s", want, s)
		}
	}
}

func TestScryReplay_WritesFixture(t *testing.T) {
	t.Parallel()
	db := seedReplayDB(t, "rec-2", true)
	fixture := filepath.Join(t.TempDir(), "fixture.json")
	var out, errOut bytes.Buffer
	code := scry(context.Background(), []string{"replay", "--db", db, "-o", fixture, "--note", "v1", "rec-2"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("code = %d, errOut = %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Saved") {
		t.Errorf("expected 'Saved' confirmation in:\n%s", out.String())
	}
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var loaded struct {
		Version int    `json:"version"`
		RunID   string `json:"run_id"`
		Note    string `json:"note"`
		Calls   []any  `json:"calls"`
	}
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 1 || loaded.RunID != "rec-2" || loaded.Note != "v1" {
		t.Errorf("fixture metadata wrong: %+v", loaded)
	}
	if len(loaded.Calls) != 1 {
		t.Errorf("calls = %d, want 1", len(loaded.Calls))
	}
}

func TestScryReplay_MissingContent(t *testing.T) {
	t.Parallel()
	db := seedReplayDB(t, "rec-3", false) // no captured content
	var out, errOut bytes.Buffer
	code := scry(context.Background(), []string{"replay", "--db", db, "rec-3"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected non-zero exit for missing captured content")
	}
	if !strings.Contains(errOut.String(), "cannot be replayed") {
		t.Errorf("expected explanation in errOut, got: %q", errOut.String())
	}
}

func TestScryReplay_UnknownRun(t *testing.T) {
	t.Parallel()
	db := seedReplayDB(t, "rec-known", true)
	var out, errOut bytes.Buffer
	code := scry(context.Background(), []string{"replay", "--db", db, "ghost"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected non-zero exit for unknown run")
	}
}

func TestScryReplay_MissingArg(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	code := scry(context.Background(), []string{"replay"}, &out, &errOut)
	if code != 64 {
		t.Errorf("code = %d, want 64", code)
	}
}
