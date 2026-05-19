package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/internal/store"
)

// seedStepsStore builds a store with one run that has the full
// shape time-travel needs: a run-level span containing two graph
// nodes, each with one provider.generate child and one of them
// also with a tool.execute child. Captured content is included on
// the first provider span so we exercise the "has captured" path.
func seedStepsStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(context.Background(), filepath.Join(dir, "traces.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	const runID = "run-steps"
	spans := []store.Span{
		{
			SpanID: "root", TraceID: "t", Name: "galdor.graph.run", RunID: runID,
			StartTimeUnixNano: 1_000_000_000, EndTimeUnixNano: 5_000_000_000,
			StatusCode: "ok",
			Attributes: map[string]any{"galdor.run.id": runID},
		},
		// Step 1: model node, with one provider call (captured) + a tool call.
		{
			SpanID: "node1", TraceID: "t", ParentSpanID: "root",
			Name: "galdor.graph.node", RunID: runID,
			StartTimeUnixNano: 1_500_000_000, EndTimeUnixNano: 2_500_000_000,
			StatusCode: "ok",
			Attributes: map[string]any{"galdor.run.id": runID, "galdor.node.name": "model"},
		},
		{
			SpanID: "gen1", TraceID: "t", ParentSpanID: "node1",
			Name: "galdor.provider.generate", RunID: runID,
			StartTimeUnixNano: 1_550_000_000, EndTimeUnixNano: 2_400_000_000,
			StatusCode: "ok",
			Attributes: map[string]any{
				"galdor.run.id":              runID,
				"gen_ai.request.model":       "claude-haiku-4-5",
				"gen_ai.response.model":      "claude-haiku-4-5",
				"gen_ai.usage.input_tokens":  float64(42),
				"gen_ai.usage.output_tokens": float64(7),
				"gen_ai.prompt":              `[{"role":"user","content":[{"type":"text","text":"hello there"}]}]`,
				"gen_ai.completion":          `{"role":"assistant","content":[{"type":"text","text":"hi back"}]}`,
			},
		},
		{
			SpanID: "tool1", TraceID: "t", ParentSpanID: "node1",
			Name: "galdor.tool.execute", RunID: runID,
			StartTimeUnixNano: 2_100_000_000, EndTimeUnixNano: 2_200_000_000,
			StatusCode: "ok",
			Attributes: map[string]any{
				"galdor.run.id":                 runID,
				"gen_ai.tool.name":              "calculator",
				"gen_ai.tool.input_size_bytes":  float64(18),
				"gen_ai.tool.output_size_bytes": float64(5),
			},
		},
		// Step 2: tools node, one provider call WITHOUT captured content.
		{
			SpanID: "node2", TraceID: "t", ParentSpanID: "root",
			Name: "galdor.graph.node", RunID: runID,
			StartTimeUnixNano: 3_000_000_000, EndTimeUnixNano: 4_000_000_000,
			StatusCode: "ok",
			Attributes: map[string]any{"galdor.run.id": runID, "galdor.node.name": "tools"},
		},
		{
			SpanID: "gen2", TraceID: "t", ParentSpanID: "node2",
			Name: "galdor.provider.generate", RunID: runID,
			StartTimeUnixNano: 3_100_000_000, EndTimeUnixNano: 3_800_000_000,
			StatusCode: "ok",
			Attributes: map[string]any{
				"galdor.run.id":              runID,
				"gen_ai.request.model":       "claude-haiku-4-5",
				"gen_ai.usage.input_tokens":  float64(15),
				"gen_ai.usage.output_tokens": float64(3),
				// No gen_ai.prompt / gen_ai.completion — capture was off.
			},
		},
	}
	if err := s.InsertSpans(context.Background(), spans); err != nil {
		t.Fatal(err)
	}
	return s
}

func newStepsServer(t *testing.T) *Server {
	t.Helper()
	srv, err := NewServer(seedStepsStore(t), Options{DBPath: "/tmp/test.db"})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestBuildStepsView_OrderAndChildren(t *testing.T) {
	t.Parallel()
	s := seedStepsStore(t)
	spans, err := s.SpansForRun(context.Background(), "run-steps")
	if err != nil {
		t.Fatal(err)
	}
	view := buildStepsView(spans, "run-steps")
	if len(view.Steps) != 2 {
		t.Fatalf("got %d steps, want 2", len(view.Steps))
	}
	if view.Steps[0].NodeName != "model" || view.Steps[1].NodeName != "tools" {
		t.Errorf("step ordering wrong: %+v", []string{view.Steps[0].NodeName, view.Steps[1].NodeName})
	}
	// Step 1 should have one provider call AND one tool call.
	if len(view.Steps[0].Provider) != 1 {
		t.Errorf("step 1 provider count = %d", len(view.Steps[0].Provider))
	}
	if len(view.Steps[0].Tools) != 1 || view.Steps[0].Tools[0].Name != "calculator" {
		t.Errorf("step 1 tools = %+v", view.Steps[0].Tools)
	}
	// Step 1 provider call has captured content.
	if !view.Steps[0].Provider[0].HasCaptured {
		t.Error("step 1 provider should be HasCaptured")
	}
	if view.HasCaptured != true {
		t.Error("view.HasCaptured should be true (one provider has captures)")
	}
	// Step 2 provider call does not have captured content.
	if view.Steps[1].Provider[0].HasCaptured {
		t.Error("step 2 provider should NOT be HasCaptured")
	}
}

func TestBuildStepsView_RendersTurns(t *testing.T) {
	t.Parallel()
	s := seedStepsStore(t)
	spans, _ := s.SpansForRun(context.Background(), "run-steps")
	view := buildStepsView(spans, "run-steps")

	prov := view.Steps[0].Provider[0]
	if len(prov.Prompt) != 1 {
		t.Fatalf("prompt turns = %d", len(prov.Prompt))
	}
	if prov.Prompt[0].Role != "user" || !strings.Contains(prov.Prompt[0].Text, "hello there") {
		t.Errorf("prompt turn wrong: %+v", prov.Prompt[0])
	}
	if prov.Completion == nil || !strings.Contains(prov.Completion.Text, "hi back") {
		t.Errorf("completion wrong: %+v", prov.Completion)
	}
}

func TestBuildStepsView_ReplayHintWhenNoCaptures(t *testing.T) {
	t.Parallel()
	// Build a tiny store with one node + one provider span, no captures.
	dir := t.TempDir()
	s, _ := store.Open(context.Background(), filepath.Join(dir, "t.db"))
	defer s.Close()
	_ = s.InsertSpans(context.Background(), []store.Span{
		{SpanID: "n", Name: "galdor.graph.node", RunID: "r", TraceID: "t",
			StartTimeUnixNano: 1, EndTimeUnixNano: 2,
			Attributes: map[string]any{"galdor.run.id": "r", "galdor.node.name": "x"}},
		{SpanID: "g", Name: "galdor.provider.generate", ParentSpanID: "n", RunID: "r", TraceID: "t",
			StartTimeUnixNano: 1, EndTimeUnixNano: 2,
			Attributes: map[string]any{"galdor.run.id": "r"}},
	})
	spans, _ := s.SpansForRun(context.Background(), "r")
	view := buildStepsView(spans, "r")
	if view.HasCaptured {
		t.Error("HasCaptured should be false")
	}
	if view.ReplayHint == "" {
		t.Error("ReplayHint should be populated when nothing was captured")
	}
}

func TestRenderTurn_FoldsToolCallsAndResults(t *testing.T) {
	t.Parallel()
	prompt := `[{"role":"assistant","tool_calls":[{"id":"c1","name":"add","arguments":"{\"a\":2,\"b\":3}"}]}]`
	turns := decodePrompt(prompt)
	if len(turns) != 1 {
		t.Fatalf("turns = %d", len(turns))
	}
	if !strings.Contains(turns[0].Text, "→ add(") {
		t.Errorf("tool call not folded: %q", turns[0].Text)
	}

	result := `[{"role":"tool","tool_call_id":"c1","content":[{"type":"text","text":"5"}]}]`
	turns = decodePrompt(result)
	if !strings.Contains(turns[0].Text, "← result for c1") {
		t.Errorf("tool result marker missing: %q", turns[0].Text)
	}
	if !strings.Contains(turns[0].Text, "5") {
		t.Errorf("tool result body missing: %q", turns[0].Text)
	}
}

// ---- Handler-level tests ----

func TestHandleRunSteps_HappyPath(t *testing.T) {
	t.Parallel()
	srv := newStepsServer(t)
	req := httptest.NewRequest(http.MethodGet, "/runs/run-steps/steps", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Run run-steps", "#1", "#2", "model", "tools",
		"hello there", "hi back", "calculator", "Replayable",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestHandleRunSteps_UnknownRun(t *testing.T) {
	t.Parallel()
	srv := newStepsServer(t)
	req := httptest.NewRequest(http.MethodGet, "/runs/ghost/steps", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleRunSteps_LinkedFromRunDetail(t *testing.T) {
	t.Parallel()
	srv := newStepsServer(t)
	req := httptest.NewRequest(http.MethodGet, "/runs/run-steps", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("run page status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/runs/run-steps/steps") {
		t.Error("run detail page should link to the steps view")
	}
}
