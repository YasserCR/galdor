package graph

import (
	"context"
	"log/slog"
)

// Hooks are the lifecycle callbacks the runtime invokes around each
// run and each node. They are the seam pkg/observability uses to
// emit OpenTelemetry spans, but any caller can install custom hooks
// — logging, metrics, audit trails, etc.
//
// Each callback is optional; nil fields are no-ops. BeforeRun /
// BeforeNode may return an updated context.Context that the runtime
// will use for the wrapped scope (typically: a context carrying a
// freshly-started span). AfterRun / AfterNode see the resulting err.
//
// Hooks are passed by value in RunOptions so the struct can be
// cheaply built and discarded per call.
type Hooks[S any] struct {
	// BeforeRun runs once at the top of the loop. It receives the
	// run's RunID (empty when not configured), the initial state
	// and the call's context. The returned ctx becomes the context
	// the loop runs under.
	BeforeRun func(ctx context.Context, runID string, initial S) context.Context

	// AfterRun runs once when the loop terminates (success or
	// error). final is the last observed state; err is the run
	// error, if any.
	AfterRun func(ctx context.Context, runID string, final S, err error)

	// BeforeNode runs immediately before a node is dispatched.
	// The returned ctx is used for the node's execution and for
	// AfterNode; observability hooks attach the node's span here.
	BeforeNode func(ctx context.Context, runID, node string, step int, state S) context.Context

	// AfterNode runs after a node returns. err is the node error,
	// if any (the runtime translates it into the run error).
	AfterNode func(ctx context.Context, runID, node string, step int, state S, err error)
}

// IsZero reports whether h is the zero Hooks — every field nil.
// The runtime uses this to short-circuit the hook plumbing.
func (h Hooks[S]) IsZero() bool {
	return h.BeforeRun == nil && h.AfterRun == nil && h.BeforeNode == nil && h.AfterNode == nil
}

// runBefore invokes BeforeRun (if any) and returns the (possibly
// updated) ctx. Panics from inside the hook are recovered: the
// runtime keeps the original ctx, logs to logger (when non-nil) and
// continues. The principle is that broken instrumentation must not
// break the agent.
func (h Hooks[S]) runBefore(ctx context.Context, logger *slog.Logger, runID string, initial S) (out context.Context) {
	out = ctx
	if h.BeforeRun == nil {
		return
	}
	defer recoverHook(logger, "BeforeRun", runID, "")
	if next := h.BeforeRun(ctx, runID, initial); next != nil {
		out = next
	}
	return
}

func (h Hooks[S]) runAfter(ctx context.Context, logger *slog.Logger, runID string, final S, err error) {
	if h.AfterRun == nil {
		return
	}
	defer recoverHook(logger, "AfterRun", runID, "")
	h.AfterRun(ctx, runID, final, err)
}

func (h Hooks[S]) nodeBefore(ctx context.Context, logger *slog.Logger, runID, node string, step int, state S) (out context.Context) {
	out = ctx
	if h.BeforeNode == nil {
		return
	}
	defer recoverHook(logger, "BeforeNode", runID, node)
	if next := h.BeforeNode(ctx, runID, node, step, state); next != nil {
		out = next
	}
	return
}

func (h Hooks[S]) nodeAfter(ctx context.Context, logger *slog.Logger, runID, node string, step int, state S, err error) {
	if h.AfterNode == nil {
		return
	}
	defer recoverHook(logger, "AfterNode", runID, node)
	h.AfterNode(ctx, runID, node, step, state, err)
}

// MergeHooks composes multiple Hooks[S] into one: each callback
// fires every component's matching callback in the order they were
// passed. BeforeRun / BeforeNode chain their ctx returns so each
// hook sees the ctx updated by the previous one. Nil-callback fields
// are skipped. Useful when wiring observability + spec recording +
// custom logging without writing a manual fan-out.
func MergeHooks[S any](hs ...Hooks[S]) Hooks[S] {
	hs = nonEmptyHooks(hs)
	if len(hs) == 0 {
		return Hooks[S]{}
	}
	if len(hs) == 1 {
		return hs[0]
	}
	return Hooks[S]{
		BeforeRun: func(ctx context.Context, runID string, initial S) context.Context {
			for _, h := range hs {
				if h.BeforeRun != nil {
					ctx = h.BeforeRun(ctx, runID, initial)
				}
			}
			return ctx
		},
		AfterRun: func(ctx context.Context, runID string, final S, err error) {
			for _, h := range hs {
				if h.AfterRun != nil {
					h.AfterRun(ctx, runID, final, err)
				}
			}
		},
		BeforeNode: func(ctx context.Context, runID, node string, step int, state S) context.Context {
			for _, h := range hs {
				if h.BeforeNode != nil {
					ctx = h.BeforeNode(ctx, runID, node, step, state)
				}
			}
			return ctx
		},
		AfterNode: func(ctx context.Context, runID, node string, step int, state S, err error) {
			for _, h := range hs {
				if h.AfterNode != nil {
					h.AfterNode(ctx, runID, node, step, state, err)
				}
			}
		},
	}
}

func nonEmptyHooks[S any](hs []Hooks[S]) []Hooks[S] {
	// Allocate a fresh slice rather than compacting in place (hs[:0]):
	// MergeHooks is variadic, so MergeHooks(mySlice...) aliases the
	// caller's backing array, and in-place compaction would overwrite
	// their elements.
	out := make([]Hooks[S], 0, len(hs))
	for _, h := range hs {
		if !h.IsZero() {
			out = append(out, h)
		}
	}
	return out
}

// recoverHook turns a panic inside a hook callback into a log
// entry. Used as `defer recoverHook(...)` inside each wrapper.
// logger == nil silently swallows the panic.
func recoverHook(logger *slog.Logger, hookName, runID, node string) {
	r := recover()
	if r == nil {
		return
	}
	if logger == nil {
		return
	}
	attrs := []any{
		slog.String("hook", hookName),
		slog.String("run_id", runID),
		slog.Any("panic_value", r),
		slog.String("stack", string(captureStack())),
	}
	if node != "" {
		attrs = append(attrs, slog.String("node", node))
	}
	logger.Warn("graph: recovered panic in hook", attrs...)
}
