// Package graph implements the generic graph runtime that powers galdor agents.
//
// A Graph[S] is a collection of Nodes and Edges over a user-defined state
// type S. Nodes execute in their own goroutines; edges are conditional
// transitions; state is passed immutably between nodes.
//
// Status: stub (Phase 3).
package graph
