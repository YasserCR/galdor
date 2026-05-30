package graph

import (
	"context"
	"testing"
)

// TestMergeHooks_DoesNotMutateCallerSlice is the regression for the
// in-place compaction footgun: MergeHooks is variadic, so spreading a
// caller's slice (MergeHooks(s...)) must not overwrite the caller's
// backing array while filtering out zero-value hooks.
func TestMergeHooks_DoesNotMutateCallerSlice(t *testing.T) {
	t.Parallel()
	real1 := Hooks[counter]{
		BeforeRun: func(ctx context.Context, _ string, _ counter) context.Context { return ctx },
	}
	real2 := Hooks[counter]{
		AfterRun: func(context.Context, string, counter, error) {},
	}
	// Interleave zero hooks so in-place compaction would shift real hooks
	// to earlier indices and clobber them.
	in := []Hooks[counter]{{}, real1, {}, real2}
	_ = MergeHooks(in...)

	if in[1].BeforeRun == nil {
		t.Error("MergeHooks stomped the caller's slice: in[1] lost its BeforeRun")
	}
	if in[3].AfterRun == nil {
		t.Error("MergeHooks stomped the caller's slice: in[3] lost its AfterRun")
	}
}

// TestMergeHooks_ChainsAllCallbacks verifies the merged hook still fires
// every component's callbacks in order.
func TestMergeHooks_ChainsAllCallbacks(t *testing.T) {
	t.Parallel()
	var order []string
	h1 := Hooks[counter]{
		BeforeNode: func(ctx context.Context, _, _ string, _ int, _ counter) context.Context {
			order = append(order, "h1")
			return ctx
		},
	}
	h2 := Hooks[counter]{
		BeforeNode: func(ctx context.Context, _, _ string, _ int, _ counter) context.Context {
			order = append(order, "h2")
			return ctx
		},
	}
	merged := MergeHooks(h1, Hooks[counter]{}, h2)
	merged.BeforeNode(context.Background(), "run", "node", 1, counter{})
	if len(order) != 2 || order[0] != "h1" || order[1] != "h2" {
		t.Errorf("callback order = %v, want [h1 h2]", order)
	}
}
