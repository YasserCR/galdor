package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/internal/store"
)

// statsDB seeds a temp DB with spans that produce predictable
// rollups for both provider and model groupings.
func statsDB(t *testing.T) string {
	t.Helper()
	path := seedDB(t) // reuses the helper from scry_test.go
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	// Add two more provider spans so the percentile path has > 1
	// sample and the openai key shows up.
	more := []store.Span{
		{
			SpanID: "o1", TraceID: "t", ParentSpanID: "node1",
			Name:              "galdor.provider.generate",
			StartTimeUnixNano: 1_200_000_000,
			EndTimeUnixNano:   2_500_000_000,
			StatusCode:        "error", StatusMessage: "x",
			Attributes: map[string]any{
				"galdor.provider.name": "openai",
				"gen_ai.request.model": "gpt-4o-mini",
			},
			RunID: "",
		},
	}
	if err := s.InsertSpans(context.Background(), more); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScryStats_Overall(t *testing.T) {
	t.Parallel()
	db := statsDB(t)
	var out, errOut bytes.Buffer
	if code := scry(context.Background(), []string{"stats", "--db", db}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d, err = %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "GROUP") || !strings.Contains(got, "ALL") {
		t.Errorf("missing overall headers: %s", got)
	}
}

func TestScryStats_ByProvider(t *testing.T) {
	t.Parallel()
	db := statsDB(t)
	var out, errOut bytes.Buffer
	if code := scry(context.Background(), []string{"stats", "--db", db, "--by", "provider"}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d, err = %s", code, errOut.String())
	}
	got := out.String()
	for _, want := range []string{"PROVIDER", "anthropic", "openai"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
}

func TestScryStats_ByModel(t *testing.T) {
	t.Parallel()
	db := statsDB(t)
	var out, errOut bytes.Buffer
	if code := scry(context.Background(), []string{"stats", "--db", db, "--by", "model"}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "MODEL") {
		t.Errorf("missing MODEL header: %s", out.String())
	}
}

func TestScryStats_JSONOutput(t *testing.T) {
	t.Parallel()
	db := statsDB(t)
	var out, errOut bytes.Buffer
	if code := scry(context.Background(), []string{"stats", "--db", db, "--by", "provider", "--format", "json"}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d", code)
	}
	var rows []store.Stats
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if len(rows) < 1 {
		t.Errorf("rows = %d", len(rows))
	}
}

func TestScryStats_UnknownBy(t *testing.T) {
	t.Parallel()
	db := statsDB(t)
	var out, errOut bytes.Buffer
	if code := scry(context.Background(), []string{"stats", "--db", db, "--by", "wat"}, &out, &errOut); code != 64 {
		t.Errorf("code = %d", code)
	}
}

func TestScryStats_EmptyDB(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := dir + "/empty.db"
	var out, errOut bytes.Buffer
	if code := scry(context.Background(), []string{"stats", "--db", db, "--by", "provider"}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "no spans recorded") {
		t.Errorf("got %s", out.String())
	}
}
