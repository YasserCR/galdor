package graph

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

type probeState struct {
	N int
}

// Regression for the cyclic-RenderSVG hang (audit C4). ReAct
// (tools->model) and Plan-Execute (execute->replan->execute) are both
// cyclic, so this is the ordinary shape. Before the fix, layeredPositions'
// longest-path BFS relaxed depth upward without bound on a static cycle
// and never returned. The select guard makes a regression fail in ~2s
// instead of hanging the suite for the default test timeout.
func TestRenderSVG_CyclicGraphDoesNotHang(t *testing.T) {
	g := New[probeState]().
		AddNode("model", func(_ context.Context, s probeState) (probeState, error) { return s, nil }).
		AddNode("tools", func(_ context.Context, s probeState) (probeState, error) { return s, nil }).
		AddEdge(START, "model").
		AddEdge("model", "tools").
		AddEdge("tools", "model") // the cycle

	r, err := g.Compile()
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- r.Inspect().RenderSVG(io.Discard) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RenderSVG on cyclic graph returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RenderSVG on a cyclic graph hung (regression of C4)")
	}
}

// Regression for the router-panic escape (audit H1). safeCallNode wraps
// node bodies; before the fix resolveNext -> router was called bare in
// runLoop, so a panicking router escaped the synchronous Invoke path and
// crashed the process. We assert the panic is contained as an error
// (and reported as a *PanicError) rather than propagated.
func TestInvoke_ContainsRouterPanic(t *testing.T) {
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("router panic escaped Invoke (regression of H1): %v", rec)
		}
	}()

	g := New[probeState]().
		AddNode("a", func(_ context.Context, s probeState) (probeState, error) { return s, nil }).
		AddEdge(START, "a").
		AddConditionalEdge("a", func(_ probeState) string { panic("router boom") })

	r, err := g.Compile()
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	_, err = r.Invoke(context.Background(), probeState{})
	if err == nil {
		t.Fatal("expected an error from the panicking router, got nil")
	}
	var pe *PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("expected a *PanicError, got %T: %v", err, err)
	}
}
