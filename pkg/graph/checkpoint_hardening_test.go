package graph

import (
	"context"
	"testing"
)

type unexportedFieldState struct {
	Public  string
	private int // the point is that gob silently drops this unexported field
}

// Regression for audit H2: a gob round-trip silently drops unexported
// fields (it's not an encode error), so checkpointing such a state would
// quietly lose data. Save must reject it and direct the user to Cloner.
func TestMemoryCheckpointer_RejectsUnexportedFieldState(t *testing.T) {
	cp := NewMemoryCheckpointer[unexportedFieldState]()
	err := cp.Save(context.Background(),
		Checkpoint[unexportedFieldState]{RunID: "r", State: unexportedFieldState{Public: "x", private: 7}})
	if err == nil {
		t.Fatal("Save must reject a state whose unexported fields a gob copy would silently drop (regression of H2)")
	}
}

type funcFieldState struct {
	Step int
	Fn   func() // not gob-serializable
}

// Regression for audit H3: a state that gob can't serialize used to be
// returned unchanged (aliased), so a later node mutating shared
// references would corrupt the already-saved checkpoint. Save must error
// instead of silently aliasing.
func TestMemoryCheckpointer_RejectsNonGobState(t *testing.T) {
	cp := NewMemoryCheckpointer[funcFieldState]()
	err := cp.Save(context.Background(),
		Checkpoint[funcFieldState]{RunID: "r", State: funcFieldState{Step: 1, Fn: func() {}}})
	if err == nil {
		t.Fatal("Save must reject a non-gob-serializable state instead of silently aliasing it (regression of H3)")
	}
}
