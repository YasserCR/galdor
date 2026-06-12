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
	"github.com/YasserCR/galdor/pkg/graph"
)

type weaveState struct{ N int }

// seedRun writes the topology of a small graph under runID into a fresh
// store at a temp path, and returns that path. The graph is the canonical
// ReAct shape (model ⇄ tools) so the spec has both static and the
// model→tools cycle.
func seedRun(t *testing.T, runID string) string {
	t.Helper()
	g := graph.New[weaveState]().
		AddNode("model", func(_ context.Context, s weaveState) (weaveState, error) { return s, nil }).
		AddNode("tools", func(_ context.Context, s weaveState) (weaveState, error) { return s, nil }).
		AddEdge(graph.START, "model").
		AddEdge("model", "tools").
		AddEdge("tools", "model")
	r, err := g.Compile()
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	specJSON, err := json.Marshal(r.Inspect())
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "traces.db")
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetGraphSpec(context.Background(), runID, specJSON); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	return path
}

func TestWeave_RendersSVG(t *testing.T) {
	t.Parallel()
	path := seedRun(t, "run-1")
	var out, errOut bytes.Buffer
	code := weave(context.Background(), []string{"run-1", "--db", path}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errOut.String())
	}
	svg := out.String()
	if !strings.Contains(svg, "<svg") || !strings.Contains(svg, "</svg>") {
		t.Fatalf("output is not an SVG document: %q", svg[:min(80, len(svg))])
	}
	// Node labels must appear in the rendered graph.
	if !strings.Contains(svg, "model") || !strings.Contains(svg, "tools") {
		t.Errorf("SVG missing node labels")
	}
}

func TestWeave_WritesFile(t *testing.T) {
	t.Parallel()
	path := seedRun(t, "run-1")
	svgPath := filepath.Join(t.TempDir(), "graph.svg")
	var out, errOut bytes.Buffer
	code := weave(context.Background(), []string{"run-1", "--db", path, "-o", svgPath}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout should be empty when -o is used, got %q", out.String())
	}
	data, err := os.ReadFile(svgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<svg") {
		t.Errorf("file is not an SVG")
	}
}

func TestWeave_JSONFormat(t *testing.T) {
	t.Parallel()
	path := seedRun(t, "run-1")
	var out, errOut bytes.Buffer
	code := weave(context.Background(), []string{"run-1", "--db", path, "--format", "json"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errOut.String())
	}
	var spec graph.Spec
	if err := json.Unmarshal(out.Bytes(), &spec); err != nil {
		t.Fatalf("output is not valid Spec JSON: %v", err)
	}
	if spec.Entry != "model" || len(spec.Nodes) != 2 {
		t.Errorf("decoded spec wrong: %+v", spec)
	}
}

func TestWeave_CheckOK(t *testing.T) {
	t.Parallel()
	path := seedRun(t, "run-1")
	var out, errOut bytes.Buffer
	code := weave(context.Background(), []string{"run-1", "--check", "--db", path}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "topology OK") {
		t.Errorf("check output = %q", out.String())
	}
}

func TestWeave_CheckDetectsDanglingEdge(t *testing.T) {
	t.Parallel()
	// Hand-craft a spec with an edge to a node that doesn't exist.
	bad := graph.Spec{
		Entry:       "a",
		Nodes:       []graph.NodeSpec{{Name: "a"}},
		StaticEdges: []graph.EdgeSpec{{From: graph.START, To: "a"}, {From: "a", To: "ghost"}},
	}
	specJSON, _ := json.Marshal(bad)
	path := filepath.Join(t.TempDir(), "traces.db")
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.SetGraphSpec(context.Background(), "run-bad", specJSON)
	_ = s.Close()

	var out, errOut bytes.Buffer
	code := weave(context.Background(), []string{"run-bad", "--check", "--db", path}, &out, &errOut)
	if code != 1 {
		t.Fatalf("expected exit 1 for dangling edge, got %d", code)
	}
	if !strings.Contains(errOut.String(), "ghost") {
		t.Errorf("check should name the unknown node, got %q", errOut.String())
	}
}

func TestWeave_MissingTopology(t *testing.T) {
	t.Parallel()
	path := seedRun(t, "run-1")
	var out, errOut bytes.Buffer
	// A run id with no recorded spec.
	code := weave(context.Background(), []string{"absent", "--db", path}, &out, &errOut)
	if code != 1 {
		t.Fatalf("expected exit 1 for missing topology, got %d", code)
	}
	if !strings.Contains(errOut.String(), "no graph topology") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestWeave_MissingRunIDArg(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	code := weave(context.Background(), []string{"--db", "/tmp/x.db"}, &out, &errOut)
	if code != 64 {
		t.Fatalf("expected exit 64 for missing run-id, got %d", code)
	}
}
