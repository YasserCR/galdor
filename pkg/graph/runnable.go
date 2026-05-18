package graph

import (
	"context"
	"errors"
	"fmt"
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
	entry            string

	// MaxSteps bounds Invoke / Stream to that many node transitions
	// before failing with ErrMaxSteps. Zero means "use the package
	// default" (defaultMaxSteps). Callers can override.
	MaxSteps int
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

	if len(problems) > 0 {
		return nil, &CompileError{Problems: problems}
	}

	return &Runnable[S]{
		nodes:            cloneNodes(g.nodes),
		staticEdges:      cloneStrMap(g.staticEdges),
		conditionalEdges: cloneRouters(g.conditionalEdges),
		entry:            entry,
	}, nil
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
		next := router(state)
		if next == "" {
			return "", fmt.Errorf("%w: from %q", ErrEmptyRouterResult, current)
		}
		if next != END {
			if _, known := r.nodes[next]; !known {
				return "", fmt.Errorf("%w: router from %q returned %q", ErrUnknownNode, current, next)
			}
		}
		return next, nil
	}
	return "", fmt.Errorf("%w: %q", ErrNoOutgoingEdge, current)
}

// Invoke runs the graph synchronously, returning the final state once
// END is reached. Cancellation through ctx is checked between every
// step.
func (r *Runnable[S]) Invoke(ctx context.Context, initial S) (S, error) {
	state := initial
	next := r.entry
	maxSteps := r.maxStepsOrDefault()

	for step := 0; ; step++ {
		if err := ctx.Err(); err != nil {
			return state, err
		}
		if next == END {
			return state, nil
		}
		if step >= maxSteps {
			return state, fmt.Errorf("%w: limit %d", ErrMaxSteps, maxSteps)
		}

		node, ok := r.nodes[next]
		if !ok {
			return state, fmt.Errorf("%w: %q", ErrUnknownNode, next)
		}
		out, err := node(ctx, state)
		if err != nil {
			return state, fmt.Errorf("node %q: %w", next, err)
		}
		state = out

		nxt, err := r.resolveNext(next, state)
		if err != nil {
			return state, err
		}
		next = nxt
	}
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
