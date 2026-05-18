package graph

// EventType enumerates the kinds of events emitted on a streaming
// run. Consumers switch on Type and read the appropriate fields.
type EventType string

const (
	// EventRunStart is emitted once, before any node executes. It
	// carries the initial state and the resolved entry-node name.
	EventRunStart EventType = "run_start"

	// EventNodeStart is emitted when execution enters a node. Node
	// and State are the inputs the node will receive.
	EventNodeStart EventType = "node_start"

	// EventNodeEnd is emitted when a node returns successfully.
	// State is the new state the node produced.
	EventNodeEnd EventType = "node_end"

	// EventEdgeTraversed is emitted after EventNodeEnd to announce
	// the next node the runtime is about to enter (or END).
	EventEdgeTraversed EventType = "edge_traversed"

	// EventRunEnd is emitted exactly once at successful termination.
	// State is the final state; Node is END.
	EventRunEnd EventType = "run_end"

	// EventError is emitted exactly once when a run fails. Err
	// carries the cause and the channel is closed afterwards.
	EventError EventType = "error"
)

// Event is a single item produced on the streaming channel. The
// active fields depend on Type — see the EventType constants. The
// state snapshot is the state observed at the moment of the event.
type Event[S any] struct {
	Type EventType

	// Node is the node name the event refers to. For
	// EventEdgeTraversed it is the destination of the edge.
	Node string

	// State is the state snapshot associated with the event. Always
	// populated except for EventError, where the prior state is no
	// longer meaningful.
	State S

	// Step is the 1-based ordinal of the current step at the moment
	// the event was emitted.
	Step int

	// Err is set only on EventError.
	Err error
}
