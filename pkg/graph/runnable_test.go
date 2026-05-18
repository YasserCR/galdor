package graph

import (
	"context"
	"errors"
	"testing"
	"time"
)

// buildCounter assembles a tiny graph that increments N until it
// reaches Limit, then exits. It exercises a conditional self-loop
// plus the END sink — the smallest non-trivial graph that proves
// the runtime handles loops correctly.
func buildCounter(t *testing.T) *Runnable[counter] {
	t.Helper()
	r, err := New[counter]().
		AddNode("inc", func(_ context.Context, c counter) (counter, error) {
			c.N++
			return c, nil
		}).
		AddEdge(START, "inc").
		AddConditionalEdge("inc", func(c counter) string {
			if c.N >= c.Limit {
				return END
			}
			return "inc"
		}).
		Compile()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestInvoke_LinearGraph(t *testing.T) {
	t.Parallel()
	r, err := New[counter]().
		AddNode("a", func(_ context.Context, c counter) (counter, error) { c.N += 1; return c, nil }).
		AddNode("b", func(_ context.Context, c counter) (counter, error) { c.N += 10; return c, nil }).
		AddEdge(START, "a").
		AddEdge("a", "b").
		AddEdge("b", END).
		Compile()
	if err != nil {
		t.Fatal(err)
	}
	final, err := r.Invoke(context.Background(), counter{})
	if err != nil {
		t.Fatal(err)
	}
	if final.N != 11 {
		t.Errorf("N = %d, want 11", final.N)
	}
}

func TestInvoke_ConditionalLoop(t *testing.T) {
	t.Parallel()
	r := buildCounter(t)
	final, err := r.Invoke(context.Background(), counter{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if final.N != 5 {
		t.Errorf("N = %d", final.N)
	}
}

func TestInvoke_MaxStepsGuards(t *testing.T) {
	t.Parallel()
	r := buildCounter(t)
	r.MaxSteps = 3
	final, err := r.Invoke(context.Background(), counter{Limit: 100})
	if !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("err = %v, want ErrMaxSteps", err)
	}
	if final.N != 3 {
		t.Errorf("partial state should report 3 increments, got N=%d", final.N)
	}
}

func TestInvoke_NodeErrorPropagated(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	r, err := New[counter]().
		AddNode("fail", func(_ context.Context, _ counter) (counter, error) { return counter{}, boom }).
		AddEdge(START, "fail").
		AddEdge("fail", END).
		Compile()
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Invoke(context.Background(), counter{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestInvoke_ContextCanceledMidRun(t *testing.T) {
	t.Parallel()
	r, err := New[counter]().
		AddNode("slow", func(ctx context.Context, c counter) (counter, error) {
			<-ctx.Done()
			return c, ctx.Err()
		}).
		AddEdge(START, "slow").
		AddEdge("slow", END).
		Compile()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = r.Invoke(ctx, counter{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestInvoke_RouterEmptyResult(t *testing.T) {
	t.Parallel()
	r, err := New[counter]().
		AddNode("x", noop).
		AddEdge(START, "x").
		AddConditionalEdge("x", func(_ counter) string { return "" }).
		Compile()
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Invoke(context.Background(), counter{})
	if !errors.Is(err, ErrEmptyRouterResult) {
		t.Fatalf("err = %v", err)
	}
}

func TestInvoke_RouterUnknownTarget(t *testing.T) {
	t.Parallel()
	r, err := New[counter]().
		AddNode("x", noop).
		AddEdge(START, "x").
		AddConditionalEdge("x", func(_ counter) string { return "ghost" }).
		Compile()
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Invoke(context.Background(), counter{})
	if !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("err = %v", err)
	}
}

func TestInvoke_EmptyGraphTerminatesImmediately(t *testing.T) {
	t.Parallel()
	// An edge directly from START -> END is unusual but legal; it
	// produces a graph that returns the initial state untouched.
	r, err := New[counter]().AddEdge(START, END).Compile()
	if err != nil {
		t.Fatal(err)
	}
	final, err := r.Invoke(context.Background(), counter{N: 7})
	if err != nil {
		t.Fatal(err)
	}
	if final.N != 7 {
		t.Errorf("initial state should pass through unchanged, got %d", final.N)
	}
}

func TestStream_EmitsExpectedEventSequence(t *testing.T) {
	t.Parallel()
	r := buildCounter(t)
	ch := r.Stream(context.Background(), counter{Limit: 2})
	var (
		seenStart, seenEnd bool
		nodeEnds           int
		lastState          counter
	)
	for ev := range ch {
		switch ev.Type {
		case EventRunStart:
			seenStart = true
		case EventNodeEnd:
			nodeEnds++
			lastState = ev.State
		case EventRunEnd:
			seenEnd = true
			lastState = ev.State
		case EventError:
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
	}
	if !seenStart || !seenEnd {
		t.Errorf("missing terminal events: start=%v end=%v", seenStart, seenEnd)
	}
	if nodeEnds != 2 {
		t.Errorf("expected 2 NodeEnd events for limit=2, got %d", nodeEnds)
	}
	if lastState.N != 2 {
		t.Errorf("final N = %d", lastState.N)
	}
}

func TestStream_PropagatesNodeError(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	r, err := New[counter]().
		AddNode("fail", func(_ context.Context, _ counter) (counter, error) { return counter{}, boom }).
		AddEdge(START, "fail").
		AddEdge("fail", END).
		Compile()
	if err != nil {
		t.Fatal(err)
	}
	ch := r.Stream(context.Background(), counter{})
	var errEv *Event[counter]
	for ev := range ch {
		if ev.Type == EventError {
			ev := ev
			errEv = &ev
		}
	}
	if errEv == nil || !errors.Is(errEv.Err, boom) {
		t.Fatalf("missing or wrong error event: %+v", errEv)
	}
}

func TestStream_ContextCancelClosesChannel(t *testing.T) {
	t.Parallel()
	r, err := New[counter]().
		AddNode("slow", func(ctx context.Context, c counter) (counter, error) {
			<-ctx.Done()
			return c, ctx.Err()
		}).
		AddEdge(START, "slow").
		AddEdge("slow", END).
		Compile()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := r.Stream(ctx, counter{})
	cancel()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // channel closed — success
			}
		case <-timeout:
			t.Fatal("stream did not close after context cancel")
		}
	}
}

func TestStream_MaxStepsErrorEvent(t *testing.T) {
	t.Parallel()
	r := buildCounter(t)
	r.MaxSteps = 2
	ch := r.Stream(context.Background(), counter{Limit: 100})
	var sawErr bool
	for ev := range ch {
		if ev.Type == EventError && errors.Is(ev.Err, ErrMaxSteps) {
			sawErr = true
		}
	}
	if !sawErr {
		t.Error("expected ErrMaxSteps event")
	}
}
