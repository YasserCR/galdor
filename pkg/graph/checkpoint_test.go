package graph

import (
	"context"
	"errors"
	"testing"
)

// sliceState carries a reference type (slice) to exercise the
// checkpoint deep-copy contract.
type sliceState struct {
	Items []int
}

// TestMemoryCheckpointer_SaveSnapshotsState is the regression for
// checkpoint state aliasing: Save must capture an independent snapshot so
// a later in-place mutation of the live state cannot corrupt an
// already-saved checkpoint.
func TestMemoryCheckpointer_SaveSnapshotsState(t *testing.T) {
	t.Parallel()
	cp := NewMemoryCheckpointer[sliceState]()
	ctx := context.Background()

	st := sliceState{Items: []int{1, 2, 3}}
	if err := cp.Save(ctx, Checkpoint[sliceState]{
		RunID: "r", Step: 1, Node: "a", State: st,
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate a later node mutating the shared backing array in place.
	st.Items[0] = 999

	got, ok, _ := cp.Load(ctx, "r")
	if !ok {
		t.Fatal("checkpoint not found")
	}
	if got.State.Items[0] != 1 {
		t.Errorf("saved checkpoint corrupted by later mutation: Items[0] = %d, want 1", got.State.Items[0])
	}
}

// cloneableState implements Cloner to exercise the precise-copy path.
type cloneableState struct {
	Items  []int
	cloned bool // unexported: gob would drop it; Clone preserves intent
}

func (c cloneableState) Clone() cloneableState {
	dup := make([]int, len(c.Items))
	copy(dup, c.Items)
	return cloneableState{Items: dup, cloned: true}
}

func TestMemoryCheckpointer_UsesClonerWhenAvailable(t *testing.T) {
	t.Parallel()
	cp := NewMemoryCheckpointer[cloneableState]()
	ctx := context.Background()
	st := cloneableState{Items: []int{1, 2}}
	if err := cp.Save(ctx, Checkpoint[cloneableState]{RunID: "r", State: st}); err != nil {
		t.Fatal(err)
	}
	st.Items[0] = 999
	got, _, _ := cp.Load(ctx, "r")
	if !got.State.cloned {
		t.Error("Cloner.Clone was not used")
	}
	if got.State.Items[0] != 1 {
		t.Errorf("Items[0] = %d, want 1", got.State.Items[0])
	}
}

// buildAddThree assembles a tiny 3-step pipeline used by checkpoint
// and interrupt tests. Steps add 1, 10, 100 to N respectively.
func buildAddThree(t *testing.T) *Runnable[counter] {
	t.Helper()
	r, err := New[counter]().
		AddNode("a", func(_ context.Context, c counter) (counter, error) { c.N += 1; return c, nil }).
		AddNode("b", func(_ context.Context, c counter) (counter, error) { c.N += 10; return c, nil }).
		AddNode("c", func(_ context.Context, c counter) (counter, error) { c.N += 100; return c, nil }).
		AddEdge(START, "a").
		AddEdge("a", "b").
		AddEdge("b", "c").
		AddEdge("c", END).
		Compile()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMemoryCheckpointer_SaveLoadEmpty(t *testing.T) {
	t.Parallel()
	cp := NewMemoryCheckpointer[counter]()
	_, found, err := cp.Load(context.Background(), "missing")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("found=true on empty checkpointer")
	}
}

func TestMemoryCheckpointer_RoundTrip(t *testing.T) {
	t.Parallel()
	cp := NewMemoryCheckpointer[counter]()
	want := Checkpoint[counter]{RunID: "r1", Step: 2, Node: "b", State: counter{N: 5}}
	if err := cp.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, found, err := cp.Load(context.Background(), "r1")
	if err != nil || !found {
		t.Fatalf("err=%v found=%v", err, found)
	}
	if got.Node != want.Node || got.Step != want.Step || got.State != want.State {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

func TestMemoryCheckpointer_HistoryAndReset(t *testing.T) {
	t.Parallel()
	cp := NewMemoryCheckpointer[counter]()
	for i := 1; i <= 3; i++ {
		_ = cp.Save(context.Background(), Checkpoint[counter]{RunID: "r", Step: i})
	}
	h := cp.History("r")
	if len(h) != 3 {
		t.Errorf("History len = %d", len(h))
	}
	// Mutating the returned slice must not affect the store.
	h[0] = Checkpoint[counter]{}
	if cp.History("r")[0].Step != 1 {
		t.Error("History must return a defensive copy")
	}
	cp.Reset("r")
	if got := cp.History("r"); got != nil {
		t.Errorf("Reset should clear history, got %+v", got)
	}
}

func TestInvokeWith_CheckpointerWithoutRunIDRejected(t *testing.T) {
	t.Parallel()
	r := buildAddThree(t)
	cp := NewMemoryCheckpointer[counter]()
	_, err := r.InvokeWith(context.Background(), counter{}, RunOptions[counter]{Checkpointer: cp})
	if !errors.Is(err, ErrCheckpointerMissingRunID) {
		t.Fatalf("err = %v", err)
	}
}

func TestInvokeWith_SavesPerStepCheckpoints(t *testing.T) {
	t.Parallel()
	r := buildAddThree(t)
	cp := NewMemoryCheckpointer[counter]()
	final, err := r.InvokeWith(context.Background(), counter{}, RunOptions[counter]{
		Checkpointer: cp,
		RunID:        "run-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if final.N != 111 {
		t.Errorf("final.N = %d", final.N)
	}
	h := cp.History("run-1")
	// Per-step before a, b, c + the terminal END save = 4.
	if len(h) != 4 {
		t.Fatalf("history len = %d (%+v)", len(h), h)
	}
	for i, want := range []string{"a", "b", "c", END} {
		if h[i].Node != want {
			t.Errorf("h[%d].Node = %q, want %q", i, h[i].Node, want)
		}
	}
	last := h[len(h)-1]
	if last.Reason != CheckpointReasonEnd {
		t.Errorf("final reason = %q", last.Reason)
	}
}

func TestInterrupt_PausesAndReturnsErrInterrupted(t *testing.T) {
	t.Parallel()
	r, err := New[counter]().
		AddNode("a", func(_ context.Context, c counter) (counter, error) { c.N += 1; return c, nil }).
		AddNode("b", func(_ context.Context, c counter) (counter, error) { c.N += 10; return c, nil }).
		AddNode("c", func(_ context.Context, c counter) (counter, error) { c.N += 100; return c, nil }).
		AddEdge(START, "a").
		AddEdge("a", "b").
		AddEdge("b", "c").
		AddEdge("c", END).
		InterruptBefore("b").
		Compile()
	if err != nil {
		t.Fatal(err)
	}
	cp := NewMemoryCheckpointer[counter]()
	state, err := r.InvokeWith(context.Background(), counter{}, RunOptions[counter]{
		Checkpointer: cp, RunID: "run-int",
	})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("err = %v", err)
	}
	// State is what node 'a' produced (N=1), because b is gated.
	if state.N != 1 {
		t.Errorf("state.N at pause = %d, want 1", state.N)
	}
	last, _, _ := cp.Load(context.Background(), "run-int")
	if last.Node != "b" || last.Reason != CheckpointReasonInterrupt {
		t.Errorf("interrupt checkpoint = %+v", last)
	}
}

func TestResume_ContinuesAfterInterrupt(t *testing.T) {
	t.Parallel()
	r, err := New[counter]().
		AddNode("a", func(_ context.Context, c counter) (counter, error) { c.N += 1; return c, nil }).
		AddNode("b", func(_ context.Context, c counter) (counter, error) { c.N += 10; return c, nil }).
		AddNode("c", func(_ context.Context, c counter) (counter, error) { c.N += 100; return c, nil }).
		AddEdge(START, "a").
		AddEdge("a", "b").
		AddEdge("b", "c").
		AddEdge("c", END).
		InterruptBefore("b").
		Compile()
	if err != nil {
		t.Fatal(err)
	}
	cp := NewMemoryCheckpointer[counter]()
	if _, err2 := r.InvokeWith(context.Background(), counter{}, RunOptions[counter]{
		Checkpointer: cp, RunID: "run-resume",
	}); !errors.Is(err2, ErrInterrupted) {
		t.Fatalf("setup err = %v", err2)
	}
	final, err := r.Resume(context.Background(), RunOptions[counter]{
		Checkpointer: cp, RunID: "run-resume",
	})
	if err != nil {
		t.Fatal(err)
	}
	if final.N != 111 {
		t.Errorf("final.N = %d (want 111: 1+10+100)", final.N)
	}
}

func TestResume_OverrideState(t *testing.T) {
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
	_, _ = r.InvokeWith(context.Background(), counter{}, RunOptions[counter]{
		Checkpointer: cp, RunID: "ovr",
	})
	override := counter{N: 1000}
	final, err := r.Resume(context.Background(), RunOptions[counter]{
		Checkpointer:  cp,
		RunID:         "ovr",
		OverrideState: &override,
	})
	if err != nil {
		t.Fatal(err)
	}
	if final.N != 1010 {
		t.Errorf("final.N = %d (override(1000) + b(+10) = 1010)", final.N)
	}
}

func TestResume_MissingCheckpointer(t *testing.T) {
	t.Parallel()
	r := buildAddThree(t)
	_, err := r.Resume(context.Background(), RunOptions[counter]{RunID: "x"})
	if !errors.Is(err, ErrResumeMissingCheckpointer) {
		t.Fatalf("err = %v", err)
	}
}

func TestResume_MissingRunID(t *testing.T) {
	t.Parallel()
	r := buildAddThree(t)
	cp := NewMemoryCheckpointer[counter]()
	_, err := r.Resume(context.Background(), RunOptions[counter]{Checkpointer: cp})
	if !errors.Is(err, ErrResumeMissingRunID) {
		t.Fatalf("err = %v", err)
	}
}

func TestResume_UnknownRunID(t *testing.T) {
	t.Parallel()
	r := buildAddThree(t)
	cp := NewMemoryCheckpointer[counter]()
	_, err := r.Resume(context.Background(), RunOptions[counter]{
		Checkpointer: cp,
		RunID:        "never-existed",
	})
	if !errors.Is(err, ErrCheckpointNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestInterruptBefore_UnknownNodeFailsCompile(t *testing.T) {
	t.Parallel()
	_, err := New[counter]().
		AddNode("a", func(_ context.Context, c counter) (counter, error) { return c, nil }).
		AddEdge(START, "a").
		AddEdge("a", END).
		InterruptBefore("ghost").
		Compile()
	if !errors.Is(err, ErrCompile) {
		t.Fatalf("err = %v", err)
	}
}

func TestInterruptBefore_RejectsReservedNames(t *testing.T) {
	t.Parallel()
	_, err := New[counter]().
		AddNode("a", func(_ context.Context, c counter) (counter, error) { return c, nil }).
		AddEdge(START, "a").
		AddEdge("a", END).
		InterruptBefore(START, END, "").
		Compile()
	if !errors.Is(err, ErrCompile) {
		t.Fatal("expected compile error")
	}
}

func TestInvokeWith_MaxStepsOverride(t *testing.T) {
	t.Parallel()
	r, err := New[counter]().
		AddNode("loop", func(_ context.Context, c counter) (counter, error) { c.N++; return c, nil }).
		AddEdge(START, "loop").
		AddConditionalEdge("loop", func(_ counter) string { return "loop" }).
		Compile()
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.InvokeWith(context.Background(), counter{}, RunOptions[counter]{MaxSteps: 2})
	if !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("err = %v", err)
	}
}
