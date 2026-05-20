package graph

import "errors"

// Sentinel errors returned by Compile and the Runnable. Callers match
// them with errors.Is.
var (
	// ErrCompile wraps every problem captured by the builder. Use
	// errors.As with a *CompileError to inspect the full list.
	ErrCompile = errors.New("graph: compile error")

	// ErrMaxSteps fires when a run exceeds Runnable.MaxSteps. It is
	// usually a sign of a misrouted conditional edge that produces an
	// infinite cycle without reaching END.
	ErrMaxSteps = errors.New("graph: max steps exceeded")

	// ErrUnknownNode is returned at Invoke time when a static edge or
	// a router resolves to a name that wasn't registered.
	ErrUnknownNode = errors.New("graph: unknown node")

	// ErrNoOutgoingEdge is returned when execution reaches a node that
	// has neither a static edge nor a conditional edge installed.
	// (END is the canonical sink; nodes need an outgoing transition.)
	ErrNoOutgoingEdge = errors.New("graph: node has no outgoing edge")

	// ErrEmptyRouterResult is returned when a conditional edge's
	// router returns "" (intentional dead-ends should resolve to END).
	ErrEmptyRouterResult = errors.New("graph: router returned empty next-node name")

	// ErrUnknownBranchLabel is returned when an AddConditionalEdges
	// router returns a label that isn't present in the branchMap.
	ErrUnknownBranchLabel = errors.New("graph: router returned unknown branch label")

	// ErrInterrupted is returned by Invoke / Resume when execution
	// paused at an interrupt-gated node. The associated Checkpointer
	// holds the checkpoint that should be passed to Resume to
	// continue. Callers detect this with errors.Is(err, ErrInterrupted).
	ErrInterrupted = errors.New("graph: run interrupted")

	// ErrResumeMissingCheckpointer is returned when Resume is called
	// without a Checkpointer in the options.
	ErrResumeMissingCheckpointer = errors.New("graph: Resume requires a Checkpointer")

	// ErrResumeMissingRunID is returned when Resume is called without
	// a RunID.
	ErrResumeMissingRunID = errors.New("graph: Resume requires a RunID")

	// ErrCheckpointerMissingRunID is returned when InvokeWith
	// supplies a Checkpointer but no RunID — without one the caller
	// cannot find the saved state again.
	ErrCheckpointerMissingRunID = errors.New("graph: Checkpointer requires a RunID")
)

// CompileError aggregates every problem the builder found.
// errors.Is(err, ErrCompile) is true; errors.As(err, &ce) lets the
// caller iterate the underlying problems.
type CompileError struct {
	Problems []error
}

// Error implements the error interface.
func (c *CompileError) Error() string {
	if c == nil || len(c.Problems) == 0 {
		return "graph: compile error"
	}
	if len(c.Problems) == 1 {
		return "graph: compile error: " + c.Problems[0].Error()
	}
	out := "graph: compile error:"
	for _, p := range c.Problems {
		out += "\n  - " + p.Error()
	}
	return out
}

// Unwrap exposes ErrCompile so errors.Is matches.
func (c *CompileError) Unwrap() error { return ErrCompile }
