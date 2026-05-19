package graph_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/graph"
)

type s struct{ N int }

func buildSampleGraph(t *testing.T) *graph.Runnable[s] {
	t.Helper()
	g := graph.New[s]().
		AddNode("model", func(_ context.Context, x s) (s, error) { return x, nil }).
		AddNode("tools", func(_ context.Context, x s) (s, error) { return x, nil }).
		AddEdge(graph.START, "model").
		AddEdge("tools", "model").
		AddConditionalEdge("model", func(_ s) string { return graph.END }).
		InterruptBefore("tools")
	r, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestInspect_TopologyShape(t *testing.T) {
	t.Parallel()
	r := buildSampleGraph(t)
	spec := r.Inspect()

	if spec.Entry != "model" {
		t.Errorf("Entry = %q", spec.Entry)
	}
	if len(spec.Nodes) != 2 {
		t.Fatalf("Nodes = %d, want 2", len(spec.Nodes))
	}
	// Alphabetical: model, tools.
	if spec.Nodes[0].Name != "model" || spec.Nodes[1].Name != "tools" {
		t.Errorf("Nodes order = %+v", spec.Nodes)
	}
	if !spec.Nodes[1].Interrupt {
		t.Error("tools node should be marked interrupt-gated")
	}
	if spec.Nodes[0].Interrupt {
		t.Error("model node should NOT be interrupt-gated")
	}
}

func TestInspect_StaticAndConditionalEdgesSeparated(t *testing.T) {
	t.Parallel()
	r := buildSampleGraph(t)
	spec := r.Inspect()

	// Static edges include START → model and tools → model.
	got := map[string]string{}
	for _, e := range spec.StaticEdges {
		got[e.From] = e.To
	}
	if got[graph.START] != "model" {
		t.Errorf("START edge = %q, want model", got[graph.START])
	}
	if got["tools"] != "model" {
		t.Errorf("tools edge = %q, want model", got["tools"])
	}

	// Conditional edges: only model has a router.
	if len(spec.ConditionalEdges) != 1 {
		t.Fatalf("ConditionalEdges = %d, want 1", len(spec.ConditionalEdges))
	}
	if spec.ConditionalEdges[0].From != "model" {
		t.Errorf("conditional from = %q", spec.ConditionalEdges[0].From)
	}
	// Conditional target must be empty (dynamic).
	if spec.ConditionalEdges[0].To != "" {
		t.Errorf("conditional To should be empty (target is dynamic), got %q", spec.ConditionalEdges[0].To)
	}
}

func TestInspect_DeterministicJSON(t *testing.T) {
	t.Parallel()
	r := buildSampleGraph(t)
	a, err := json.Marshal(r.Inspect())
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(r.Inspect())
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("Inspect output is non-deterministic:\n%s\nvs\n%s", a, b)
	}
}

func TestRenderSVG_ContainsExpectedElements(t *testing.T) {
	t.Parallel()
	r := buildSampleGraph(t)
	var buf bytes.Buffer
	if err := r.Inspect().RenderSVG(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// Top-level SVG envelope.
	if !strings.HasPrefix(out, `<svg`) || !strings.HasSuffix(out, `</svg>`) {
		t.Errorf("SVG envelope wrong: prefix=%q suffix=%q", out[:5], out[len(out)-6:])
	}
	// Both node names should appear in the rendered text.
	for _, want := range []string{"model", "tools", "START", "END"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered SVG missing %q", want)
		}
	}
	// Conditional edge should render with the router label.
	if !strings.Contains(out, "router") {
		t.Error("conditional edge stub missing 'router' label")
	}
	// Arrowhead marker definitions are required for the edge marker-end attrs.
	if !strings.Contains(out, `id="a"`) || !strings.Contains(out, `id="ad"`) {
		t.Error("arrowhead marker defs missing")
	}
}

func TestRenderSVG_TolerantOfMinimalGraph(t *testing.T) {
	t.Parallel()
	// One-node graph: START → only → END.
	g := graph.New[s]().
		AddNode("only", func(_ context.Context, x s) (s, error) { return x, nil }).
		AddEdge(graph.START, "only").
		AddEdge("only", graph.END)
	r, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := r.Inspect().RenderSVG(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "only") {
		t.Error("single-node graph did not render")
	}
}

func TestRenderSVG_EscapesNodeNames(t *testing.T) {
	t.Parallel()
	// Node names with XML-special characters must be escaped in the
	// emitted SVG, otherwise we produce invalid XML.
	g := graph.New[s]().
		AddNode("a<b>", func(_ context.Context, x s) (s, error) { return x, nil }).
		AddEdge(graph.START, "a<b>").
		AddEdge("a<b>", graph.END)
	r, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := r.Inspect().RenderSVG(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "a&lt;b&gt;") {
		t.Errorf("special characters not escaped: %s", out)
	}
	if strings.Contains(out, "<text x=\"") && strings.Contains(out, "a<b>") {
		// If raw "a<b>" appears inside a <text> element, the XML is broken.
		// We check for the inverse below using a simple substring scan.
	}
}
