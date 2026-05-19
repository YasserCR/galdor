package graph_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/YasserCR/galdor/pkg/graph"
)

type tState struct{ N int }

func TestInvokeWith_RunTimeoutAborts(t *testing.T) {
	t.Parallel()
	slowNode := func(ctx context.Context, s tState) (tState, error) {
		select {
		case <-time.After(500 * time.Millisecond):
			return s, nil
		case <-ctx.Done():
			return s, ctx.Err()
		}
	}
	g := graph.New[tState]().
		AddNode("slow", slowNode).
		AddEdge(graph.START, "slow").
		AddEdge("slow", graph.END)
	r, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = r.InvokeWith(context.Background(), tState{}, graph.RunOptions[tState]{
		Timeout: 50 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("run took %v, expected ~50ms", elapsed)
	}
}

func TestInvokeWith_NodeTimeoutAbortsSingleNode(t *testing.T) {
	t.Parallel()
	// First node is fast, second node is slow. With NodeTimeout=50ms
	// the slow one aborts and the run errors out at that node.
	fast := func(_ context.Context, s tState) (tState, error) { return s, nil }
	slow := func(ctx context.Context, s tState) (tState, error) {
		select {
		case <-time.After(500 * time.Millisecond):
			return s, nil
		case <-ctx.Done():
			return s, ctx.Err()
		}
	}
	g := graph.New[tState]().
		AddNode("fast", fast).
		AddNode("slow", slow).
		AddEdge(graph.START, "fast").
		AddEdge("fast", "slow").
		AddEdge("slow", graph.END)
	r, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = r.InvokeWith(context.Background(), tState{}, graph.RunOptions[tState]{
		NodeTimeout: 50 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("run took %v, expected ~50ms (just the slow node's budget)", elapsed)
	}
}

func TestInvokeWith_NoTimeoutWhenZero(t *testing.T) {
	t.Parallel()
	// Both Timeout=0 and NodeTimeout=0 mean "no limit" — verify the
	// run completes its normal work.
	work := func(_ context.Context, s tState) (tState, error) {
		s.N++
		return s, nil
	}
	g := graph.New[tState]().
		AddNode("work", work).
		AddEdge(graph.START, "work").
		AddEdge("work", graph.END)
	r, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	final, err := r.InvokeWith(context.Background(), tState{}, graph.RunOptions[tState]{})
	if err != nil {
		t.Fatal(err)
	}
	if final.N != 1 {
		t.Errorf("N = %d, want 1", final.N)
	}
}

func TestInvokeWith_RunTimeoutInheritsParentCancel(t *testing.T) {
	t.Parallel()
	// Parent ctx is cancelled before the run finishes — parent
	// cancellation must propagate even when an additional Timeout
	// is set (whichever fires first wins).
	slow := func(ctx context.Context, s tState) (tState, error) {
		select {
		case <-time.After(500 * time.Millisecond):
			return s, nil
		case <-ctx.Done():
			return s, ctx.Err()
		}
	}
	g := graph.New[tState]().
		AddNode("slow", slow).
		AddEdge(graph.START, "slow").
		AddEdge("slow", graph.END)
	r, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	_, err = r.InvokeWith(ctx, tState{}, graph.RunOptions[tState]{
		Timeout: time.Second, // much longer than the parent cancel
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want Canceled (parent wins)", err)
	}
}
