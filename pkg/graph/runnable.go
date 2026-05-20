package graph

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// defaultMaxSteps is the upper bound on the number of node
// transitions a single run can perform. Misrouted conditional edges
// that loop forever are caught by this guard.
const defaultMaxSteps = 100

// Runnable is the immutable, compiled result of Graph.Compile. It is
// safe for concurrent use across Invoke / Stream calls (each call
// gets its own state copy through the initial argument).
type Runnable[S any] struct {
	nodes            map[string]NodeFunc[S]
	staticEdges      map[string]string
	conditionalEdges map[string]Router[S]
	branchMaps       map[string]map[string]string
	interruptBefore  map[string]struct{}
	entry            string

	// MaxSteps bounds Invoke / Stream to that many node transitions
	// before failing with ErrMaxSteps. Zero means "use the package
	// default" (defaultMaxSteps). Callers can override.
	MaxSteps int
}

// RunOptions configures a single Invoke / Resume call. All fields
// are optional; a zero-value RunOptions yields the same behavior as
// Invoke without options.
type RunOptions[S any] struct {
	// Checkpointer, when non-nil, receives a checkpoint before each
	// node and on terminal events. RunID must also be set.
	Checkpointer Checkpointer[S]

	// RunID identifies the run. Required when Checkpointer is set.
	// Otherwise it is informational and used only for trace
	// attribution (Phase 4 hooks in here later).
	RunID string

	// MaxSteps overrides Runnable.MaxSteps for this call when > 0.
	MaxSteps int

	// OverrideState, when non-nil on Resume, replaces the
	// checkpoint's State before execution continues. Use for
	// human-in-the-loop workflows where a person edits the state
	// during the pause.
	OverrideState *S

	// Hooks are the lifecycle callbacks invoked around each run
	// and each node. They are the seam observability layers use
	// to emit OpenTelemetry spans, metrics or audit events.
	Hooks Hooks[S]

	// Timeout, when > 0, caps the total wall time of the run. The
	// runtime derives a deadline-bound child context at runLoop
	// entry; nodes that respect ctx (every well-behaved one) abort
	// when the deadline fires. The run returns context.DeadlineExceeded
	// wrapped with the elapsed time.
	Timeout time.Duration

	// NodeTimeout, when > 0, caps the wall time of any single node
	// call. Each node receives a derived child context with this
	// deadline. Useful for "kill a stuck node" patterns — though
	// the run terminates with the node's error regardless; survival
	// requires the caller to retry.
	NodeTimeout time.Duration

	// Logger, when non-nil, receives operational events the
	// observability layer doesn't capture — recovered panics, hook
	// failures, deadline fires. These are warnings about runtime
	// invariants, not normal trace data. When nil the runtime
	// silently swallows the same events; nothing in the loop's
	// happy path emits log lines.
	Logger *slog.Logger
}

// Compile validates the graph and returns a Runnable.
//
// Validation:
//   - All edge endpoints reference either a registered node or one
//     of the reserved sentinels (START as source, END as sink).
//   - Exactly one outgoing edge exists from START (the entry).
//   - Every registered node has an outgoing edge (static or
//     conditional). END never has one.
//
// Builder errors accumulated during AddNode / AddEdge /
// AddConditionalEdge are returned wrapped in a *CompileError that
// satisfies errors.Is(err, ErrCompile).
func (g *Graph[S]) Compile() (*Runnable[S], error) {
	problems := append([]error(nil), g.errs...)

	// Entry point: the static edge out of START.
	entry, hasEntry := g.staticEdges[START]
	if !hasEntry {
		if _, hasCond := g.conditionalEdges[START]; hasCond {
			problems = append(problems,
				errors.New("graph: START must have a static edge, not a conditional one"))
		} else {
			problems = append(problems, errors.New("graph: missing entry — add an edge from START to a node"))
		}
	}

	// Validate edge endpoints reference known nodes (or END).
	for from, to := range g.staticEdges {
		if from == START {
			continue // entry handled above
		}
		if _, ok := g.nodes[from]; !ok {
			problems = append(problems, fmt.Errorf("graph: edge from unknown node %q", from))
		}
		if to != END {
			if _, ok := g.nodes[to]; !ok {
				problems = append(problems, fmt.Errorf("graph: edge from %q to unknown node %q", from, to))
			}
		}
	}
	for from := range g.conditionalEdges {
		if from == START {
			// Already reported above.
			continue
		}
		if _, ok := g.nodes[from]; !ok {
			problems = append(problems, fmt.Errorf("graph: conditional edge from unknown node %q", from))
		}
	}

	// Branch-map targets must reference known nodes (or END). The
	// labels themselves are opaque; their resolution is enforced at
	// build time so a typo in the map fails at Compile, not on the
	// first run that happens to hit that branch.
	for from, bm := range g.branchMaps {
		for label, target := range bm {
			if target == "" {
				problems = append(problems, fmt.Errorf("graph: AddConditionalEdges(%q): label %q has empty target", from, label))
				continue
			}
			if target == END {
				continue
			}
			if _, ok := g.nodes[target]; !ok {
				problems = append(problems, fmt.Errorf("graph: AddConditionalEdges(%q): label %q -> unknown node %q", from, label, target))
			}
		}
	}

	// Every node must have exactly one outgoing transition.
	for name := range g.nodes {
		_, hasStatic := g.staticEdges[name]
		_, hasCond := g.conditionalEdges[name]
		switch {
		case hasStatic && hasCond:
			// Already reported when both were installed; defensive.
			problems = append(problems, fmt.Errorf("graph: node %q has both static and conditional edges", name))
		case !hasStatic && !hasCond:
			problems = append(problems, fmt.Errorf("graph: node %q has no outgoing edge", name))
		}
	}

	// Entry target must exist as a node (or be END — odd but legal:
	// an empty graph that just terminates).
	if hasEntry && entry != END {
		if _, ok := g.nodes[entry]; !ok {
			problems = append(problems, fmt.Errorf("graph: START -> %q is not a registered node", entry))
		}
	}

	// Interrupt-gated nodes must reference real, registered nodes.
	for name := range g.interruptBefore {
		if _, ok := g.nodes[name]; !ok {
			problems = append(problems, fmt.Errorf("graph: InterruptBefore: unknown node %q", name))
		}
	}

	if len(problems) > 0 {
		return nil, &CompileError{Problems: problems}
	}

	return &Runnable[S]{
		nodes:            cloneNodes(g.nodes),
		staticEdges:      cloneStrMap(g.staticEdges),
		conditionalEdges: cloneRouters(g.conditionalEdges),
		branchMaps:       cloneBranchMaps(g.branchMaps),
		interruptBefore:  cloneSet(g.interruptBefore),
		entry:            entry,
	}, nil
}

func cloneBranchMaps(in map[string]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(in))
	for k, v := range in {
		out[k] = cloneStrMap(v)
	}
	return out
}

func cloneSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

func cloneNodes[S any](in map[string]NodeFunc[S]) map[string]NodeFunc[S] {
	out := make(map[string]NodeFunc[S], len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStrMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneRouters[S any](in map[string]Router[S]) map[string]Router[S] {
	out := make(map[string]Router[S], len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// maxStepsOrDefault returns the effective step ceiling for a run.
func (r *Runnable[S]) maxStepsOrDefault() int {
	if r.MaxSteps > 0 {
		return r.MaxSteps
	}
	return defaultMaxSteps
}

// resolveNext returns the next node the runtime should hop to from
// `current`, given the latest state. Returns the next node name and
// a flag indicating whether the run should terminate (next == END).
func (r *Runnable[S]) resolveNext(current string, state S) (string, error) {
	if next, ok := r.staticEdges[current]; ok {
		return next, nil
	}
	if router, ok := r.conditionalEdges[current]; ok {
		out := router(state)
		if out == "" {
			return "", fmt.Errorf("%w: from %q", ErrEmptyRouterResult, current)
		}
		// Branch-map flavor: the router returned a label; resolve it
		// to the actual next node via the per-source map.
		if bm, hasMap := r.branchMaps[current]; hasMap {
			next, found := bm[out]
			if !found {
				return "", fmt.Errorf("%w: from %q label %q", ErrUnknownBranchLabel, current, out)
			}
			return next, nil
		}
		// Plain conditional edge: the router returned a node name.
		if out != END {
			if _, known := r.nodes[out]; !known {
				return "", fmt.Errorf("%w: router from %q returned %q", ErrUnknownNode, current, out)
			}
		}
		return out, nil
	}
	return "", fmt.Errorf("%w: %q", ErrNoOutgoingEdge, current)
}

// Invoke runs the graph synchronously, returning the final state once
// END is reached. Cancellation through ctx is checked between every
// step. Equivalent to InvokeWith(ctx, initial, RunOptions[S]{}).
func (r *Runnable[S]) Invoke(ctx context.Context, initial S) (S, error) {
	return r.InvokeWith(ctx, initial, RunOptions[S]{})
}

// InvokeWith runs the graph with extra options: an optional
// Checkpointer (RunID required), per-call MaxSteps override, etc.
// When the run hits an interrupt-gated node it saves a checkpoint
// and returns ErrInterrupted; callers detect that with errors.Is and
// continue with Resume.
func (r *Runnable[S]) InvokeWith(ctx context.Context, initial S, opts RunOptions[S]) (S, error) {
	if err := validateRunOptions(opts); err != nil {
		return initial, err
	}
	return r.runLoop(ctx, initial, r.entry, 0, opts, false)
}

// Resume continues a previously interrupted run. opts.Checkpointer
// and opts.RunID are required. The most recent checkpoint for that
// run is loaded, optionally overridden via opts.OverrideState, and
// execution resumes at the checkpoint's Node. The interrupt that
// caused the original pause is bypassed for the first hop, so the
// resumed node actually executes.
func (r *Runnable[S]) Resume(ctx context.Context, opts RunOptions[S]) (S, error) {
	if opts.Checkpointer == nil {
		var zero S
		return zero, ErrResumeMissingCheckpointer
	}
	if opts.RunID == "" {
		var zero S
		return zero, ErrResumeMissingRunID
	}
	ck, found, err := opts.Checkpointer.Load(ctx, opts.RunID)
	if err != nil {
		var zero S
		return zero, fmt.Errorf("graph: load checkpoint: %w", err)
	}
	if !found {
		var zero S
		return zero, fmt.Errorf("%w: %q", ErrCheckpointNotFound, opts.RunID)
	}
	state := ck.State
	if opts.OverrideState != nil {
		state = *opts.OverrideState
	}
	return r.runLoop(ctx, state, ck.Node, ck.Step-1, opts, true)
}

// validateRunOptions enforces invariants that depend on opts only.
func validateRunOptions[S any](opts RunOptions[S]) error {
	if opts.Checkpointer != nil && opts.RunID == "" {
		return ErrCheckpointerMissingRunID
	}
	return nil
}

// runLoop is the shared core of Invoke / InvokeWith / Resume. The
// bypassInterrupt flag is true only when called from Resume; it
// suppresses the interrupt check on the first hop so a resumed
// node actually runs.
func (r *Runnable[S]) runLoop(
	ctx context.Context,
	initial S,
	startNode string,
	startStep int,
	opts RunOptions[S],
	bypassInterrupt bool,
) (final S, retErr error) {
	state := initial

	// Run-level timeout: derive a deadline-bound child context.
	// Hooks see the bounded context too, so any spans they create
	// inherit the cancellation when the deadline fires.
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// BeforeRun may install spans / loggers on the context;
	// AfterRun observes the terminal state and error.
	ctx = opts.Hooks.runBefore(ctx, opts.Logger, opts.RunID, initial)
	defer func() {
		opts.Hooks.runAfter(ctx, opts.Logger, opts.RunID, state, retErr)
	}()

	next := startNode
	maxSteps := r.maxStepsOrDefault()
	if opts.MaxSteps > 0 {
		maxSteps = opts.MaxSteps
	}

	for step := startStep; ; step++ {
		if err := ctx.Err(); err != nil {
			return state, err
		}
		if next == END {
			if err := r.saveCheckpoint(ctx, opts, Checkpoint[S]{
				RunID: opts.RunID, Step: step, Node: END, State: state,
				Reason: CheckpointReasonEnd, CreatedAt: time.Now(),
			}); err != nil {
				return state, err
			}
			return state, nil
		}
		if step >= maxSteps {
			return state, fmt.Errorf("%w: limit %d", ErrMaxSteps, maxSteps)
		}

		// Interrupt gate. The first hop after a Resume bypasses it.
		if _, gated := r.interruptBefore[next]; gated && !bypassInterrupt {
			if err := r.saveCheckpoint(ctx, opts, Checkpoint[S]{
				RunID: opts.RunID, Step: step + 1, Node: next, State: state,
				Reason: CheckpointReasonInterrupt, CreatedAt: time.Now(),
			}); err != nil {
				return state, err
			}
			return state, fmt.Errorf("%w: at node %q", ErrInterrupted, next)
		}
		bypassInterrupt = false

		node, ok := r.nodes[next]
		if !ok {
			return state, fmt.Errorf("%w: %q", ErrUnknownNode, next)
		}

		// Per-step checkpoint, before the node runs.
		if err := r.saveCheckpoint(ctx, opts, Checkpoint[S]{
			RunID: opts.RunID, Step: step + 1, Node: next, State: state,
			Reason: CheckpointReasonStep, CreatedAt: time.Now(),
		}); err != nil {
			return state, err
		}

		// Node lifecycle hooks. BeforeNode may return a derived ctx
		// (typically: one carrying a freshly-started span) — that
		// ctx is used for the node call AND for AfterNode so the
		// hook implementation has a single place to put its state.
		nodeCtx := opts.Hooks.nodeBefore(ctx, opts.Logger, opts.RunID, next, step+1, state)
		// Per-node timeout, applied AFTER the BeforeNode hook so
		// the span the hook created isn't tainted by the deadline
		// (it's the node's work that's being bounded, not the
		// instrumentation).
		if opts.NodeTimeout > 0 {
			var cancel context.CancelFunc
			nodeCtx, cancel = context.WithTimeout(nodeCtx, opts.NodeTimeout)
			defer cancel() // intentionally fires at runLoop return; deadlines are short
		}
		out, nodeErr := safeCallNode(node, nodeCtx, state)
		opts.Hooks.nodeAfter(nodeCtx, opts.Logger, opts.RunID, next, step+1, out, nodeErr)
		if nodeErr != nil {
			r.logPanicIfAny(opts, next, step+1, nodeErr)
			return state, fmt.Errorf("node %q: %w", next, nodeErr)
		}
		state = out

		nxt, err := r.resolveNext(next, state)
		if err != nil {
			return state, err
		}
		next = nxt
	}
}

// logPanicIfAny logs a recovered panic at warn level on the
// configured Logger. No-op when Logger is nil or when err is not a
// PanicError.
func (r *Runnable[S]) logPanicIfAny(opts RunOptions[S], node string, step int, err error) {
	if opts.Logger == nil || err == nil {
		return
	}
	var pe *PanicError
	if !errors.As(err, &pe) {
		return
	}
	opts.Logger.Warn("graph: recovered panic in node",
		slog.String("run_id", opts.RunID),
		slog.String("node", node),
		slog.Int("step", step),
		slog.Any("panic_value", pe.Value),
		slog.String("stack", string(pe.Stack)),
	)
}

// saveCheckpoint forwards a checkpoint to the configured Checkpointer
// (no-op when none is configured).
func (r *Runnable[S]) saveCheckpoint(ctx context.Context, opts RunOptions[S], ck Checkpoint[S]) error {
	if opts.Checkpointer == nil {
		return nil
	}
	if err := opts.Checkpointer.Save(ctx, ck); err != nil {
		return fmt.Errorf("graph: save checkpoint: %w", err)
	}
	return nil
}

// Stream runs the graph and emits typed events on the returned
// channel as it progresses. The channel is buffered to soften the
// producer/consumer coupling and is closed when the run terminates
// (success or error). Cancellation through ctx is checked at each
// step; the consumer must drain the channel until it closes.
func (r *Runnable[S]) Stream(ctx context.Context, initial S) <-chan Event[S] {
	out := make(chan Event[S], 16)
	go r.runStream(ctx, initial, out)
	return out
}

func (r *Runnable[S]) runStream(ctx context.Context, initial S, out chan<- Event[S]) {
	defer close(out)

	state := initial
	step := 0
	maxSteps := r.maxStepsOrDefault()

	emit := func(ev Event[S]) bool {
		select {
		case out <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	emit(Event[S]{Type: EventRunStart, Node: r.entry, State: state, Step: 0})

	next := r.entry
	for {
		if err := ctx.Err(); err != nil {
			emit(Event[S]{Type: EventError, Err: err, Step: step})
			return
		}
		if next == END {
			emit(Event[S]{Type: EventRunEnd, Node: END, State: state, Step: step})
			return
		}
		if step >= maxSteps {
			emit(Event[S]{
				Type: EventError, Step: step,
				Err: fmt.Errorf("%w: limit %d", ErrMaxSteps, maxSteps),
			})
			return
		}
		step++

		node, ok := r.nodes[next]
		if !ok {
			emit(Event[S]{Type: EventError, Step: step,
				Err: fmt.Errorf("%w: %q", ErrUnknownNode, next)})
			return
		}
		if !emit(Event[S]{Type: EventNodeStart, Node: next, State: state, Step: step}) {
			return
		}

		newState, err := node(ctx, state)
		if err != nil {
			emit(Event[S]{Type: EventError, Node: next, Step: step,
				Err: fmt.Errorf("node %q: %w", next, err)})
			return
		}
		state = newState

		if !emit(Event[S]{Type: EventNodeEnd, Node: next, State: state, Step: step}) {
			return
		}

		nxt, err := r.resolveNext(next, state)
		if err != nil {
			emit(Event[S]{Type: EventError, Node: next, Step: step, Err: err})
			return
		}
		if !emit(Event[S]{Type: EventEdgeTraversed, Node: nxt, State: state, Step: step}) {
			return
		}
		next = nxt
	}
}
