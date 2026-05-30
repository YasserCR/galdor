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

// TestStream_RecoversNodePanic is the regression for the Stream/Invoke
// parity gap: a node panic on the streaming path used to crash the whole
// process. It must now surface as a terminal EventError wrapping ErrPanic.
func TestStream_RecoversNodePanic(t *testing.T) {
	t.Parallel()
	r, err := New[counter]().
		AddNode("boom", func(_ context.Context, _ counter) (counter, error) {
			panic("stream kaboom")
		}).
		AddEdge(START, "boom").
		AddEdge("boom", END).
		Compile()
	if err != nil {
		t.Fatal(err)
	}
	var panicEv *Event[counter]
	for ev := range r.Stream(context.Background(), counter{}) {
		if ev.Type == EventError {
			ev := ev
			panicEv = &ev
		}
	}
	if panicEv == nil {
		t.Fatal("expected an EventError from the panicking node")
	}
	if !errors.Is(panicEv.Err, ErrPanic) {
		t.Fatalf("err = %v, want ErrPanic", panicEv.Err)
	}
	var pe *PanicError
	if !errors.As(panicEv.Err, &pe) {
		t.Fatalf("err = %v, want *PanicError", panicEv.Err)
	}
}

// TestStreamWith_CheckpointsAndHooks verifies the streaming path now
// drives Checkpointer and Hooks, exactly like InvokeWith.
func TestStreamWith_CheckpointsAndHooks(t *testing.T) {
	t.Parallel()
	r := buildCounter(t)
	cp := NewMemoryCheckpointer[counter]()
	var beforeNodes, afterNodes int
	hooks := Hooks[counter]{
		BeforeNode: func(ctx context.Context, _, _ string, _ int, _ counter) context.Context {
			beforeNodes++
			return ctx
		},
		AfterNode: func(_ context.Context, _, _ string, _ int, _ counter, _ error) {
			afterNodes++
		},
	}
	ch := r.StreamWith(context.Background(), counter{Limit: 3}, RunOptions[counter]{
		Checkpointer: cp, RunID: "stream-cp", Hooks: hooks,
	})
	var ended bool
	for ev := range ch {
		if ev.Type == EventRunEnd {
			ended = true
		}
		if ev.Type == EventError {
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
	}
	if !ended {
		t.Fatal("missing EventRunEnd")
	}
	if beforeNodes != 3 || afterNodes != 3 {
		t.Errorf("hook counts: before=%d after=%d, want 3/3", beforeNodes, afterNodes)
	}
	hist := cp.History("stream-cp")
	if len(hist) == 0 {
		t.Fatal("no checkpoints saved on the streaming path")
	}
	if last := hist[len(hist)-1]; last.Node != END || last.Reason != CheckpointReasonEnd {
		t.Errorf("last checkpoint = %+v, want END/end", last)
	}
}

// TestStreamWith_InterruptEmitsErrInterrupted verifies interrupt gating on
// the streaming path: a gated node saves an interrupt checkpoint and ends
// the stream with an ErrInterrupted EventError, and Resume continues.
func TestStreamWith_InterruptEmitsErrInterrupted(t *testing.T) {
	t.Parallel()
	r, err := New[counter]().
		AddNode("a", func(_ context.Context, c counter) (counter, error) { c.N += 1; return c, nil }).
		AddNode("b", func(_ context.Context, c counter) (counter, error) { c.N += 10; return c, nil }).
		AddEdge(START, "a").
		AddEdge("a", "b").
		AddEdge("b", END).
		InterruptBefore("b").
		Compile()
	if err != nil {
		t.Fatal(err)
	}
	cp := NewMemoryCheckpointer[counter]()
	var interruptErr error
	for ev := range r.StreamWith(context.Background(), counter{}, RunOptions[counter]{
		Checkpointer: cp, RunID: "stream-int",
	}) {
		if ev.Type == EventError {
			interruptErr = ev.Err
		}
	}
	if !errors.Is(interruptErr, ErrInterrupted) {
		t.Fatalf("err = %v, want ErrInterrupted", interruptErr)
	}
	last, _, _ := cp.Load(context.Background(), "stream-int")
	if last.Node != "b" || last.Reason != CheckpointReasonInterrupt {
		t.Errorf("interrupt checkpoint = %+v", last)
	}
	final, err := r.Resume(context.Background(), RunOptions[counter]{
		Checkpointer: cp, RunID: "stream-int",
	})
	if err != nil {
		t.Fatal(err)
	}
	if final.N != 11 {
		t.Errorf("final.N = %d, want 11 (1+10)", final.N)
	}
}
