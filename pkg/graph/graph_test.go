package graph

import (
	"context"
	"errors"
	"testing"
)

type counter struct {
	N     int
	Limit int
}

func TestBuilder_RejectsReservedNames(t *testing.T) {
	t.Parallel()
	g := New[counter]().
		AddNode(START, noop).
		AddNode(END, noop).
		AddEdge(START, "x")
	_, err := g.Compile()
	if err == nil {
		t.Fatal("expected compile error")
	}
	var ce *CompileError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v", err)
	}
}

func TestBuilder_EmptyAndNilGuards(t *testing.T) {
	t.Parallel()
	g := New[counter]().
		AddNode("", noop).
		AddNode("x", nil)
	_, err := g.Compile()
	if !errors.Is(err, ErrCompile) {
		t.Fatalf("err = %v", err)
	}
}

func TestBuilder_DuplicateNode(t *testing.T) {
	t.Parallel()
	g := New[counter]().AddNode("a", noop).AddNode("a", noop)
	_, err := g.Compile()
	if !errors.Is(err, ErrCompile) {
		t.Fatal("expected error")
	}
}

func TestBuilder_RejectsEdgeIntoStartAndOutOfEnd(t *testing.T) {
	t.Parallel()
	g := New[counter]().
		AddNode("x", noop).
		AddEdge("x", START).
		AddEdge(END, "x")
	_, err := g.Compile()
	if !errors.Is(err, ErrCompile) {
		t.Fatal("expected error")
	}
}

func TestBuilder_ConflictingEdges(t *testing.T) {
	t.Parallel()
	g := New[counter]().
		AddNode("x", noop).
		AddEdge("x", END).
		AddConditionalEdge("x", func(_ counter) string { return END })
	_, err := g.Compile()
	if !errors.Is(err, ErrCompile) {
		t.Fatal("expected error")
	}
}

func TestCompile_MissingEntry(t *testing.T) {
	t.Parallel()
	_, err := New[counter]().AddNode("x", noop).Compile()
	if !errors.Is(err, ErrCompile) {
		t.Fatal("expected error for missing entry")
	}
}

func TestCompile_NodeWithoutOutgoingEdge(t *testing.T) {
	t.Parallel()
	g := New[counter]().AddNode("x", noop).AddEdge(START, "x")
	_, err := g.Compile()
	if !errors.Is(err, ErrCompile) {
		t.Fatal("expected error: x has no outgoing edge")
	}
}

func TestCompile_UnknownEdgeTarget(t *testing.T) {
	t.Parallel()
	g := New[counter]().
		AddNode("x", noop).
		AddEdge(START, "x").
		AddEdge("x", "y") // y doesn't exist
	_, err := g.Compile()
	if !errors.Is(err, ErrCompile) {
		t.Fatal("expected error")
	}
}

func TestCompile_EntryUnknown(t *testing.T) {
	t.Parallel()
	g := New[counter]().AddEdge(START, "ghost")
	_, err := g.Compile()
	if !errors.Is(err, ErrCompile) {
		t.Fatal("expected error")
	}
}

func TestCompileError_Format(t *testing.T) {
	t.Parallel()
	one := &CompileError{Problems: []error{errors.New("only one")}}
	if got := one.Error(); got != "graph: compile error: only one" {
		t.Errorf("single: %q", got)
	}
	many := &CompileError{Problems: []error{errors.New("a"), errors.New("b")}}
	if got := many.Error(); got == "" {
		t.Errorf("multi: %q", got)
	}
	var nilErr *CompileError
	if got := nilErr.Error(); got != "graph: compile error" {
		t.Errorf("nil: %q", got)
	}
	empty := &CompileError{}
	if got := empty.Error(); got != "graph: compile error" {
		t.Errorf("empty: %q", got)
	}
}

// noop is a do-nothing node used in builder tests.
func noop(_ context.Context, c counter) (counter, error) { return c, nil }

func TestAddConditionalEdges_HappyPath(t *testing.T) {
	t.Parallel()
	g := New[counter]().
		AddNode("inc", func(_ context.Context, c counter) (counter, error) {
			c.N++
			return c, nil
		}).
		AddEdge(START, "inc").
		AddConditionalEdges("inc",
			func(c counter) string {
				if c.N >= c.Limit {
					return "done"
				}
				return "again"
			},
			map[string]string{
				"again": "inc",
				"done":  END,
			})
	r, err := g.Compile()
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	final, err := r.Invoke(context.Background(), counter{Limit: 3})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if final.N != 3 {
		t.Errorf("final.N = %d, want 3", final.N)
	}
}

func TestAddConditionalEdges_LabelToEND(t *testing.T) {
	t.Parallel()
	g := New[counter]().
		AddNode("x", noop).
		AddEdge(START, "x").
		AddConditionalEdges("x",
			func(_ counter) string { return "stop" },
			map[string]string{"stop": END})
	r, err := g.Compile()
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := r.Invoke(context.Background(), counter{}); err != nil {
		t.Fatalf("invoke: %v", err)
	}
}

func TestAddConditionalEdges_UnknownLabelAtRuntime(t *testing.T) {
	t.Parallel()
	g := New[counter]().
		AddNode("x", noop).
		AddEdge(START, "x").
		AddConditionalEdges("x",
			func(_ counter) string { return "ghost" },
			map[string]string{"ok": END})
	r, err := g.Compile()
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = r.Invoke(context.Background(), counter{})
	if !errors.Is(err, ErrUnknownBranchLabel) {
		t.Fatalf("err = %v, want ErrUnknownBranchLabel", err)
	}
}

func TestAddConditionalEdges_EmptyBranchMapRejectedAtCompile(t *testing.T) {
	t.Parallel()
	g := New[counter]().
		AddNode("x", noop).
		AddEdge(START, "x").
		AddConditionalEdges("x",
			func(_ counter) string { return "ok" },
			map[string]string{})
	_, err := g.Compile()
	if !errors.Is(err, ErrCompile) {
		t.Fatalf("err = %v, want ErrCompile", err)
	}
}

func TestAddConditionalEdges_NilBranchMapRejectedAtCompile(t *testing.T) {
	t.Parallel()
	g := New[counter]().
		AddNode("x", noop).
		AddEdge(START, "x").
		AddConditionalEdges("x",
			func(_ counter) string { return "ok" },
			nil)
	_, err := g.Compile()
	if !errors.Is(err, ErrCompile) {
		t.Fatalf("err = %v, want ErrCompile", err)
	}
}

func TestAddConditionalEdges_UnknownTargetRejectedAtCompile(t *testing.T) {
	t.Parallel()
	g := New[counter]().
		AddNode("x", noop).
		AddEdge(START, "x").
		AddConditionalEdges("x",
			func(_ counter) string { return "ok" },
			map[string]string{"ok": "ghost"})
	_, err := g.Compile()
	if !errors.Is(err, ErrCompile) {
		t.Fatal("expected compile error for unknown branch target")
	}
}

func TestAddConditionalEdges_ConflictsWithAddConditionalEdge(t *testing.T) {
	t.Parallel()
	g := New[counter]().
		AddNode("x", noop).
		AddEdge(START, "x").
		AddConditionalEdge("x", func(_ counter) string { return END }).
		AddConditionalEdges("x",
			func(_ counter) string { return "ok" },
			map[string]string{"ok": END})
	_, err := g.Compile()
	if !errors.Is(err, ErrCompile) {
		t.Fatalf("err = %v, want ErrCompile", err)
	}
}

func TestAddConditionalEdges_ConflictsWithAddEdge(t *testing.T) {
	t.Parallel()
	g := New[counter]().
		AddNode("x", noop).
		AddEdge(START, "x").
		AddEdge("x", END).
		AddConditionalEdges("x",
			func(_ counter) string { return "ok" },
			map[string]string{"ok": END})
	_, err := g.Compile()
	if !errors.Is(err, ErrCompile) {
		t.Fatalf("err = %v, want ErrCompile", err)
	}
}

func TestAddConditionalEdges_BranchMapCopiedDefensively(t *testing.T) {
	t.Parallel()
	bm := map[string]string{"go": END}
	g := New[counter]().
		AddNode("x", noop).
		AddEdge(START, "x").
		AddConditionalEdges("x",
			func(_ counter) string { return "go" }, bm)
	// Mutate caller's map after install — must not affect the graph.
	bm["go"] = "ghost"
	r, err := g.Compile()
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := r.Invoke(context.Background(), counter{}); err != nil {
		t.Fatalf("invoke: %v", err)
	}
}
