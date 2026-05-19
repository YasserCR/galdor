package graph_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/graph"
)

type pState struct{ N int }

func TestRun_NodePanicSurfacedAsError(t *testing.T) {
	t.Parallel()
	g := graph.New[pState]().
		AddNode("bomb", func(_ context.Context, s pState) (pState, error) {
			panic("kaboom")
		}).
		AddEdge(graph.START, "bomb").
		AddEdge("bomb", graph.END)
	r, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Invoke(context.Background(), pState{})
	if err == nil {
		t.Fatal("expected panic to surface as error")
	}
	var pe *graph.PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *graph.PanicError", err)
	}
	if pe.Value != "kaboom" {
		t.Errorf("Value = %v, want \"kaboom\"", pe.Value)
	}
	if !errors.Is(err, graph.ErrPanic) {
		t.Errorf("errors.Is(err, graph.ErrPanic) = false")
	}
	if len(pe.Stack) == 0 {
		t.Error("Stack snapshot should be non-empty")
	}
	if !strings.Contains(string(pe.Stack), "bomb") && !strings.Contains(string(pe.Stack), "TestRun_NodePanicSurfacedAsError") {
		// At least one of: the panicking closure frame or this
		// test function should appear in the stack.
		t.Errorf("Stack snapshot looks unrelated:\n%s", pe.Stack)
	}
}

func TestRun_PanicWithErrorValueUnwraps(t *testing.T) {
	t.Parallel()
	inner := errors.New("inner boom")
	g := graph.New[pState]().
		AddNode("bomb", func(_ context.Context, _ pState) (pState, error) {
			panic(inner)
		}).
		AddEdge(graph.START, "bomb").
		AddEdge("bomb", graph.END)
	r, _ := g.Compile()
	_, err := r.Invoke(context.Background(), pState{})
	if !errors.Is(err, inner) {
		t.Errorf("errors.Is(err, inner) = false; panic with an error value must unwrap")
	}
}

func TestRun_HookPanicDoesNotFailRun(t *testing.T) {
	t.Parallel()
	// A panicking hook is an instrumentation bug, not a logic bug.
	// The run must still complete with the node's normal output.
	g := graph.New[pState]().
		AddNode("ok", func(_ context.Context, s pState) (pState, error) {
			s.N = 99
			return s, nil
		}).
		AddEdge(graph.START, "ok").
		AddEdge("ok", graph.END)
	r, _ := g.Compile()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	final, err := r.InvokeWith(context.Background(), pState{}, graph.RunOptions[pState]{
		Logger: logger,
		Hooks: graph.Hooks[pState]{
			BeforeNode: func(ctx context.Context, _, _ string, _ int, _ pState) context.Context {
				panic("instrumentation bug")
			},
		},
	})
	if err != nil {
		t.Fatalf("hook panic must not fail the run: %v", err)
	}
	if final.N != 99 {
		t.Errorf("N = %d, want 99 (node still ran)", final.N)
	}
	if !strings.Contains(buf.String(), "recovered panic in hook") {
		t.Errorf("expected the logger to record the recovered hook panic; log:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "instrumentation bug") {
		t.Errorf("expected the panic value to appear in the log; got:\n%s", buf.String())
	}
}

func TestRun_NodePanicGoesToLogger(t *testing.T) {
	t.Parallel()
	g := graph.New[pState]().
		AddNode("bomb", func(_ context.Context, _ pState) (pState, error) {
			panic("trace me")
		}).
		AddEdge(graph.START, "bomb").
		AddEdge("bomb", graph.END)
	r, _ := g.Compile()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	_, _ = r.InvokeWith(context.Background(), pState{}, graph.RunOptions[pState]{Logger: logger})

	out := buf.String()
	if !strings.Contains(out, "recovered panic in node") {
		t.Errorf("expected a 'recovered panic in node' log line, got:\n%s", out)
	}
	if !strings.Contains(out, "trace me") {
		t.Errorf("log should include the panic value; got:\n%s", out)
	}
	if !strings.Contains(out, "node=bomb") {
		t.Errorf("log should include node=bomb attribute; got:\n%s", out)
	}
}

func TestRun_NoLoggerStillRecoversPanic(t *testing.T) {
	t.Parallel()
	// Logger=nil must be a no-op, not a crash.
	g := graph.New[pState]().
		AddNode("bomb", func(_ context.Context, _ pState) (pState, error) {
			panic("silent")
		}).
		AddEdge(graph.START, "bomb").
		AddEdge("bomb", graph.END)
	r, _ := g.Compile()
	_, err := r.InvokeWith(context.Background(), pState{}, graph.RunOptions[pState]{Logger: nil})
	if err == nil {
		t.Fatal("expected error even without a logger")
	}
	if !errors.Is(err, graph.ErrPanic) {
		t.Errorf("err = %v, want ErrPanic", err)
	}
}
