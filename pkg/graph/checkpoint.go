package graph

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"sync"
	"time"
)

// Checkpoint is the durable snapshot of an in-progress run.
//
// The runtime saves a Checkpoint *before* executing each node, so
// State is what that node will receive and Node is the node about to
// run. Resuming from a Checkpoint re-enters at exactly that node with
// exactly that state — interrupts on the resumed node are bypassed
// once so a manual resume can make progress.
type Checkpoint[S any] struct {
	// RunID identifies the run. It is stable across resumes and is
	// what callers pass to Checkpointer.Load and Runnable.Resume.
	RunID string

	// Step is the 1-based ordinal of the node about to run. Step 0
	// is reserved for the initial state (before any node executes).
	Step int

	// Node is the next node to execute. It can be END when the run
	// terminated cleanly, or an interrupt-gated node when an Invoke
	// returned ErrInterrupted.
	Node string

	// State is the snapshot the next node will receive.
	State S

	// Reason describes why the checkpoint was saved. The runtime
	// uses "step" for ordinary per-step saves, "interrupt" right
	// before a paused node, and "end" once the run reaches END.
	Reason CheckpointReason

	// CreatedAt is when the checkpoint was produced. Useful for
	// trace UIs and audit logs.
	CreatedAt time.Time
}

// CheckpointReason is a small enum describing why a checkpoint exists.
type CheckpointReason string

const (
	// CheckpointReasonStep marks an ordinary per-step save.
	CheckpointReasonStep CheckpointReason = "step"

	// CheckpointReasonInterrupt marks the save that precedes an
	// interrupt-gated node before the runtime returns ErrInterrupted.
	CheckpointReasonInterrupt CheckpointReason = "interrupt"

	// CheckpointReasonEnd marks the terminal save after a run
	// reaches END.
	CheckpointReasonEnd CheckpointReason = "end"
)

// Checkpointer is the persistence interface galdor uses to save and
// reload Checkpoints. Implementations need only be safe for the
// concurrency level the calling code uses; MemoryCheckpointer below
// is safe across goroutines.
//
// Immutability contract: Save must capture an independent snapshot of
// ck.State. The runtime passes state by value, but when S contains
// reference types (slices, maps, pointers) a later node can mutate the
// shared backing storage and silently corrupt an already-saved
// checkpoint. Serializing implementations (SQLite, Postgres) get this
// for free; in-memory ones must deep-copy. MemoryCheckpointer does.
type Checkpointer[S any] interface {
	// Save stores ck. Implementations may keep only the latest
	// Checkpoint per RunID or retain history — galdor never assumes
	// history is preserved. See the immutability contract above.
	Save(ctx context.Context, ck Checkpoint[S]) error

	// Load returns the latest Checkpoint for runID. The second
	// return value is false when no Checkpoint exists for that
	// runID (distinct from a fetch error).
	Load(ctx context.Context, runID string) (Checkpoint[S], bool, error)
}

// ErrCheckpointNotFound is returned by helpers when a Checkpointer
// reports no checkpoint for the requested run.
var ErrCheckpointNotFound = errors.New("graph: checkpoint not found")

// NewMemoryCheckpointer returns a Checkpointer that keeps every
// checkpoint in memory. History per RunID is preserved (latest is
// also accessible via Load); see History for time-travel-style
// access. Safe for concurrent use.
func NewMemoryCheckpointer[S any]() *MemoryCheckpointer[S] {
	return &MemoryCheckpointer[S]{
		history: map[string][]Checkpoint[S]{},
	}
}

// MemoryCheckpointer is the canonical in-process Checkpointer
// implementation. Useful for tests, prototypes, and small single-
// node deployments. For multi-process durability, plug in a
// persistent implementation (Postgres, SQLite, Redis) — the
// interface is intentionally narrow.
type MemoryCheckpointer[S any] struct {
	mu      sync.RWMutex
	history map[string][]Checkpoint[S]
}

// Save appends a snapshot of ck to the history for ck.RunID. The State
// is deep-copied (see cloneState) so a later node mutating shared slices
// or maps cannot corrupt this already-saved checkpoint.
func (m *MemoryCheckpointer[S]) Save(_ context.Context, ck Checkpoint[S]) error {
	ck.State = cloneState(ck.State)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.history[ck.RunID] = append(m.history[ck.RunID], ck)
	return nil
}

// Cloner lets a state type provide a precise deep copy for checkpointing.
// When S (or *S) implements Cloner, MemoryCheckpointer uses Clone() to
// snapshot state on Save. Implement it when your state carries unexported
// fields, funcs, channels, or anything a gob round-trip can't reproduce.
type Cloner[S any] interface {
	Clone() S
}

// cloneState returns an independent deep copy of s for safe checkpoint
// storage. It prefers an explicit Clone() (Cloner), falls back to a gob
// round-trip for ordinary exported-field structs, and finally returns s
// unchanged when the type is not gob-serializable (funcs, channels, no
// exported fields) — preserving prior behavior rather than failing the
// run. Types in the last bucket should implement Cloner to be safe.
func cloneState[S any](s S) S {
	if c, ok := any(s).(Cloner[S]); ok {
		return c.Clone()
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(s); err != nil {
		return s
	}
	var out S
	if err := gob.NewDecoder(&buf).Decode(&out); err != nil {
		return s
	}
	return out
}

// Load returns the most recent Checkpoint for runID.
func (m *MemoryCheckpointer[S]) Load(_ context.Context, runID string) (Checkpoint[S], bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	h := m.history[runID]
	if len(h) == 0 {
		return Checkpoint[S]{}, false, nil
	}
	return h[len(h)-1], true, nil
}

// History returns the full ordered slice of Checkpoints saved for
// runID. The returned slice is a copy; mutating it does not affect
// the underlying store. Useful for tests and Phase 9 time-travel.
func (m *MemoryCheckpointer[S]) History(runID string) []Checkpoint[S] {
	m.mu.RLock()
	defer m.mu.RUnlock()
	src := m.history[runID]
	if len(src) == 0 {
		return nil
	}
	out := make([]Checkpoint[S], len(src))
	copy(out, src)
	return out
}

// Reset removes the recorded history for runID. Convenient in tests.
func (m *MemoryCheckpointer[S]) Reset(runID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.history, runID)
}
