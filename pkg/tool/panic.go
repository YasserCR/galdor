package tool

import (
	"errors"
	"fmt"
	"runtime"
)

// PanicError wraps a recovered panic from inside a Tool's
// ExecuteJSON. The executor surfaces it as a regular Result.Err
// (errors.As-detectable) instead of letting the panic unwind into
// the agent's goroutine.
//
// Tool authors should never have to think about this: well-written
// tools return errors. But a user-supplied tool that dereferences
// a nil pointer, indexes a slice out of bounds, or calls a paniccing
// library is a real production hazard. This wrapper contains the
// blast radius to the offending tool's Result.
type PanicError struct {
	// Tool is the name of the tool that panicked.
	Tool string

	// Value is the value passed to panic(). May be a string, an
	// error, or any other type per Go semantics.
	Value any

	// Stack is the goroutine stack at the panic point.
	Stack []byte
}

// Error implements the error interface.
func (e *PanicError) Error() string {
	return fmt.Sprintf("tool %q panicked: %v", e.Tool, e.Value)
}

// Unwrap returns the wrapped error when Value itself is an error
// (a common Go convention: panic(fmt.Errorf("..."))). Otherwise
// returns nil and errors.Is falls through.
func (e *PanicError) Unwrap() error {
	if err, ok := e.Value.(error); ok {
		return err
	}
	return nil
}

// ErrPanic is the sentinel that errors.Is matches every PanicError
// against. Useful for blanket "did any tool panic" checks.
var ErrPanic = errors.New("tool: panic recovered")

// Is implements errors.Is for PanicError.
func (e *PanicError) Is(target error) bool {
	return target == ErrPanic
}

// captureStack returns a 4 KiB snapshot of the current goroutine's
// stack. Matches the helper in pkg/graph; the two are intentionally
// duplicated so neither package depends on the other for this
// low-level utility.
func captureStack() []byte {
	const bufSize = 4096
	buf := make([]byte, bufSize)
	n := runtime.Stack(buf, false)
	return buf[:n]
}
