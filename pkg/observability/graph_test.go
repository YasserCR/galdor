package observability

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/codes"

	"github.com/YasserCR/galdor/pkg/graph"
)

type counter struct{ N int }

func TestTraceHooks_RunPlusNodeSpans(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	hooks := TraceHooks[counter](tr)

	r, err := graph.New[counter]().
		AddNode("a", func(_ context.Context, c counter) (counter, error) { c.N += 1; return c, nil }).
		AddNode("b", func(_ context.Context, c counter) (counter, error) { c.N += 10; return c, nil }).
		AddEdge(graph.START, "a").
		AddEdge("a", "b").
		AddEdge("b", graph.END).
		Compile()
	if err != nil {
		t.Fatal(err)
	}
	final, err := r.InvokeWith(context.Background(), counter{}, graph.RunOptions[counter]{
		RunID: "run-1",
		Hooks: hooks,
	})
	if err != nil {
		t.Fatal(err)
	}
	if final.N != 11 {
		t.Errorf("N = %d", final.N)
	}
	_ = tp.ForceFlush(context.Background())

	spans := exp.GetSpans()
	// 1 run + 2 nodes = 3
	if len(spans) != 3 {
		t.Fatalf("spans = %d (%+v)", len(spans), spans)
	}
	var runs, nodes int
	for _, s := range spans {
		switch s.Name {
		case SpanGraphRun:
			runs++
			if !hasAttr(s.Attributes, AttrGaldorRunID, "run-1") {
				t.Errorf("run span missing run.id: %+v", s.Attributes)
			}
		case SpanGraphNode:
			nodes++
		}
	}
	if runs != 1 || nodes != 2 {
		t.Errorf("runs=%d nodes=%d", runs, nodes)
	}
}

func TestTraceHooks_RecordsNodeError(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	hooks := TraceHooks[counter](tr)

	boom := errors.New("boom")
	r, err := graph.New[counter]().
		AddNode("fail", func(_ context.Context, _ counter) (counter, error) { return counter{}, boom }).
		AddEdge(graph.START, "fail").
		AddEdge("fail", graph.END).
		Compile()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.InvokeWith(context.Background(), counter{}, graph.RunOptions[counter]{
		RunID: "err-run",
		Hooks: hooks,
	}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	_ = tp.ForceFlush(context.Background())

	var runStatus, nodeStatus codes.Code
	for _, s := range exp.GetSpans() {
		switch s.Name {
		case SpanGraphRun:
			runStatus = s.Status.Code
		case SpanGraphNode:
			nodeStatus = s.Status.Code
		}
	}
	if nodeStatus != codes.Error {
		t.Errorf("node status = %v", nodeStatus)
	}
	if runStatus != codes.Error {
		t.Errorf("run status = %v (run hook should mark the error too)", runStatus)
	}
}
