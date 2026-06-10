// Package graph is galdor's generic graph runtime.
//
// A Graph[S] is a directed graph of named Nodes connected by Edges,
// parameterized over a user-defined state type S. The graph is built
// with a small fluent API:
//
//	g := graph.New[State]()
//	g.AddNode("plan",    plan).
//	  AddNode("act",     act).
//	  AddEdge(graph.START, "plan").
//	  AddEdge("plan", "act").
//	  AddEdge("act", graph.END)
//
//	r, err := g.Compile()
//	if err != nil { ... }
//	final, err := r.Invoke(ctx, State{...})
//
// Each node is `func(ctx context.Context, state S) (S, error)`. The
// node returns the next state — galdor treats the state as immutable
// across hops, which makes time-travel debugging tractable.
// Concurrency, cancellation and the streaming event channel are
// driven by `context.Context` (see ADR-005 for the rationale).
//
// Conditional edges allow data-driven branching: `AddConditionalEdge`
// installs a router function that picks the next node's name from
// the current state. A single node may have either one static edge
// or one conditional edge — never both.
package graph
