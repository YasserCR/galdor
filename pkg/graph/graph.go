package graph

import (
	"context"
	"errors"
	"fmt"
)

// Special node names. START marks the implicit entry point; an edge
// from START to a real node names that node as the first to run. END
// is the terminal sentinel; an edge to END causes execution to stop
// and return the current state as the result.
const (
	START = "__start__"
	END   = "__end__"
)

// NodeFunc is the body of a graph node. It receives the current state
// and returns the next state (or an error to halt the run).
//
// State is treated as a value: nodes should return a new S rather
// than mutating the receiver. The runtime does not deep-copy S between
// hops — that responsibility lives with the node author for any
// mutable substructure that must not bleed across iterations.
type NodeFunc[S any] func(ctx context.Context, state S) (S, error)

// Router resolves the next node's name from the current state when a
// node has a conditional edge installed.
type Router[S any] func(state S) string

// Graph is a builder for a directed graph over the state type S.
// Build with New, then chain AddNode / AddEdge / AddConditionalEdge,
// and finalize with Compile.
type Graph[S any] struct {
	nodes map[string]NodeFunc[S]

	// staticEdges[name] = next-node-name; mutually exclusive with
	// conditionalEdges for the same key. START lives in staticEdges
	// (or in conditionalEdges if the user installs a router on it,
	// though that's uncommon).
	staticEdges      map[string]string
	conditionalEdges map[string]Router[S]

	// errs accumulates problems found while building; surfaced at
	// Compile time so the builder API stays chainable.
	errs []error
}

// New returns an empty graph builder.
func New[S any]() *Graph[S] {
	return &Graph[S]{
		nodes:            map[string]NodeFunc[S]{},
		staticEdges:      map[string]string{},
		conditionalEdges: map[string]Router[S]{},
	}
}

// AddNode registers fn under name. Returns the graph for chaining.
//
// name must be non-empty, non-START/END, and not previously
// registered. Failures are captured and surfaced at Compile time.
func (g *Graph[S]) AddNode(name string, fn NodeFunc[S]) *Graph[S] {
	if name == "" {
		g.errs = append(g.errs, errors.New("graph: AddNode: name is empty"))
		return g
	}
	if name == START || name == END {
		g.errs = append(g.errs, fmt.Errorf("graph: AddNode: %q is a reserved name", name))
		return g
	}
	if fn == nil {
		g.errs = append(g.errs, fmt.Errorf("graph: AddNode(%q): fn is nil", name))
		return g
	}
	if _, ok := g.nodes[name]; ok {
		g.errs = append(g.errs, fmt.Errorf("graph: AddNode: duplicate node %q", name))
		return g
	}
	g.nodes[name] = fn
	return g
}

// AddEdge installs an unconditional transition from -> to. Both names
// may be node names previously registered with AddNode, or the
// reserved sentinels START / END (only END is a valid sink; only
// START is a valid source from a non-node).
//
// At most one outgoing edge (static or conditional) is allowed per
// from-node. Duplicates and conflicts are reported at Compile time.
func (g *Graph[S]) AddEdge(from, to string) *Graph[S] {
	if from == "" || to == "" {
		g.errs = append(g.errs, errors.New("graph: AddEdge: empty endpoint"))
		return g
	}
	if from == END {
		g.errs = append(g.errs, errors.New("graph: AddEdge: cannot have an edge OUT of END"))
		return g
	}
	if to == START {
		g.errs = append(g.errs, errors.New("graph: AddEdge: cannot have an edge INTO START"))
		return g
	}
	if existing, ok := g.staticEdges[from]; ok {
		g.errs = append(g.errs, fmt.Errorf("graph: AddEdge: %q already has static edge -> %q", from, existing))
		return g
	}
	if _, ok := g.conditionalEdges[from]; ok {
		g.errs = append(g.errs, fmt.Errorf("graph: AddEdge: %q already has a conditional edge", from))
		return g
	}
	g.staticEdges[from] = to
	return g
}

// AddConditionalEdge installs a router-driven transition from. The
// router function picks the next node's name (which may be END) based
// on the current state. Returning an empty string or an unknown name
// is treated as a runtime error at Invoke time.
func (g *Graph[S]) AddConditionalEdge(from string, router Router[S]) *Graph[S] {
	if from == "" {
		g.errs = append(g.errs, errors.New("graph: AddConditionalEdge: empty from"))
		return g
	}
	if from == END {
		g.errs = append(g.errs, errors.New("graph: AddConditionalEdge: cannot install router on END"))
		return g
	}
	if router == nil {
		g.errs = append(g.errs, fmt.Errorf("graph: AddConditionalEdge(%q): router is nil", from))
		return g
	}
	if existing, ok := g.staticEdges[from]; ok {
		g.errs = append(g.errs, fmt.Errorf("graph: AddConditionalEdge: %q already has static edge -> %q", from, existing))
		return g
	}
	if _, ok := g.conditionalEdges[from]; ok {
		g.errs = append(g.errs, fmt.Errorf("graph: AddConditionalEdge: %q already has a router", from))
		return g
	}
	g.conditionalEdges[from] = router
	return g
}
