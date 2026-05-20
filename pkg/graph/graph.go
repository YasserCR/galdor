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

	// branchMaps[from] is the label -> target-node map installed by
	// AddConditionalEdges. The router for `from` returns a label;
	// the runtime looks it up here to get the actual next node.
	// Stored separately from conditionalEdges so Compile can
	// validate every label resolves to a real node and Spec can
	// surface the labels for visualization.
	branchMaps map[string]map[string]string

	// interruptBefore is the set of node names whose execution must
	// be preceded by a pause. When the runtime reaches one of these
	// it saves a checkpoint and returns ErrInterrupted instead of
	// running the node. A subsequent Resume bypasses the gate for
	// the immediately following hop.
	interruptBefore map[string]struct{}

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
		branchMaps:       map[string]map[string]string{},
		interruptBefore:  map[string]struct{}{},
	}
}

// InterruptBefore marks the named nodes as interrupt-gated. When a
// run reaches one, the runtime saves a checkpoint and returns
// ErrInterrupted. Callers detect this with errors.Is, optionally
// inspect or modify the saved state, and call Resume to continue.
//
// Names are validated at Compile time: unknown nodes and reserved
// sentinels (START / END) are rejected.
func (g *Graph[S]) InterruptBefore(names ...string) *Graph[S] {
	for _, name := range names {
		if name == "" {
			g.errs = append(g.errs, errors.New("graph: InterruptBefore: empty name"))
			continue
		}
		if name == START || name == END {
			g.errs = append(g.errs, fmt.Errorf("graph: InterruptBefore: %q is a reserved name", name))
			continue
		}
		g.interruptBefore[name] = struct{}{}
	}
	return g
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

// AddConditionalEdges installs a router-driven transition from `from`
// where the router returns a semantic label and `branchMap` resolves
// that label to the actual next node name. Use this when the router's
// decision domain (e.g. "approve" / "reject" / "needs_human") should
// be decoupled from the node names — matching LangGraph's
// add_conditional_edges shape.
//
// branchMap values may be node names previously registered with
// AddNode or the reserved END sentinel. An empty or nil branchMap is
// rejected at Compile time. At runtime, a label that isn't present in
// branchMap surfaces as ErrUnknownBranchLabel.
//
// Like AddConditionalEdge, only one outgoing edge (static or any
// flavor of conditional) is allowed per from-node.
func (g *Graph[S]) AddConditionalEdges(from string, router Router[S], branchMap map[string]string) *Graph[S] {
	if from == "" {
		g.errs = append(g.errs, errors.New("graph: AddConditionalEdges: empty from"))
		return g
	}
	if from == END {
		g.errs = append(g.errs, errors.New("graph: AddConditionalEdges: cannot install router on END"))
		return g
	}
	if router == nil {
		g.errs = append(g.errs, fmt.Errorf("graph: AddConditionalEdges(%q): router is nil", from))
		return g
	}
	if len(branchMap) == 0 {
		g.errs = append(g.errs, fmt.Errorf("graph: AddConditionalEdges(%q): branchMap is empty", from))
		return g
	}
	if existing, ok := g.staticEdges[from]; ok {
		g.errs = append(g.errs, fmt.Errorf("graph: AddConditionalEdges: %q already has static edge -> %q", from, existing))
		return g
	}
	if _, ok := g.conditionalEdges[from]; ok {
		g.errs = append(g.errs, fmt.Errorf("graph: AddConditionalEdges: %q already has a router", from))
		return g
	}
	// Defensive copy so post-hoc mutation by the caller can't change
	// the compiled graph's behavior.
	bm := make(map[string]string, len(branchMap))
	for k, v := range branchMap {
		bm[k] = v
	}
	g.conditionalEdges[from] = router
	g.branchMaps[from] = bm
	return g
}
