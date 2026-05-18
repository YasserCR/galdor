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
