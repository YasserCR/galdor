package graph_test

import (
	"context"
	"testing"

	"github.com/YasserCR/galdor/pkg/graph"
)

type benchState struct{ N int }

// noOpNode is the cheapest possible body: a single field update.
// Used to isolate the runtime's own overhead from anything the
// user code does inside a node.
func noOpNode(_ context.Context, s benchState) (benchState, error) {
	s.N++
	return s, nil
}

// BenchmarkInvoke_SingleNode measures the per-call cost of a
// minimal graph: START -> work -> END. This is the floor of what
// the runtime adds on top of the user's node body.
func BenchmarkInvoke_SingleNode(b *testing.B) {
	g := graph.New[benchState]().
		AddNode("work", noOpNode).
		AddEdge(graph.START, "work").
		AddEdge("work", graph.END)
	r, err := g.Compile()
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Invoke(ctx, benchState{}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkInvoke_TenNodes measures the cost of a longer chain
// (10 nodes). The marginal cost over BenchmarkInvoke_SingleNode
// divided by 9 is roughly the per-transition overhead.
func BenchmarkInvoke_TenNodes(b *testing.B) {
	g := graph.New[benchState]()
	for i := 0; i < 10; i++ {
		name := nodeName(i)
		g = g.AddNode(name, noOpNode)
	}
	g = g.AddEdge(graph.START, nodeName(0))
	for i := 0; i < 9; i++ {
		g = g.AddEdge(nodeName(i), nodeName(i+1))
	}
	g = g.AddEdge(nodeName(9), graph.END)
	r, err := g.Compile()
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Invoke(ctx, benchState{}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkInvoke_ConditionalEdge measures the cost of a router-
// driven transition. Conditional edges add a function call per
// hop on top of the static-edge cost.
func BenchmarkInvoke_ConditionalEdge(b *testing.B) {
	router := func(_ benchState) string { return graph.END }
	g := graph.New[benchState]().
		AddNode("work", noOpNode).
		AddEdge(graph.START, "work").
		AddConditionalEdge("work", router)
	r, err := g.Compile()
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Invoke(ctx, benchState{}); err != nil {
			b.Fatal(err)
		}
	}
}

// nodeName returns a short stable name for benchmark setup.
func nodeName(i int) string {
	return [...]string{"n0", "n1", "n2", "n3", "n4", "n5", "n6", "n7", "n8", "n9"}[i]
}
