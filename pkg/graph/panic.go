package graph

import (
	"context"
	"errors"
	"fmt"
	"runtime"
)

// safeCallNode invokes fn under a recover() guard. A panic from
// inside fn is converted into a *PanicError and returned as the
// node's error, with the prior state (the state we handed in)
// preserved so the runtime has something to expose.
//
// Generic so it works for any state type without boxing.
func safeCallNode[S any](fn NodeFunc[S], ctx context.Context, state S) (out S, err error) {
	defer func() {
		if r := recover(); r != nil {
			out = state
			err = &PanicError{Value: r, Stack: captureStack()}
		}
	}()
	return fn(ctx, state)
}

// safeRouter invokes a conditional-edge Router under a recover() guard,
// converting a panic into a *PanicError. Routers are user code; without
// this a panicking router escapes resolveNext and crashes the process
// on the synchronous Invoke path (runStream has its own backstop, but
// Invoke does not). Mirrors safeCallNode.
func safeRouter[S any](router Router[S], state S) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Value: r, Stack: captureStack()}
		}
	}()
	return router(state), nil
}

// PanicError wraps a recovered panic so the runtime can surface it
// as an ordinary error instead of crashing the process. The Value
// is whatever the user code passed to panic(); Stack is a snapshot
// of the goroutine stack at the panic point, truncated to 4 KiB.
//
// Detect with errors.As:
//
//	var pe *graph.PanicError
//	if errors.As(err, &pe) {
//	    log.Printf("node panicked: %v\n%s", pe.Value, pe.Stack)
//	}
type PanicError struct {
	// Value is the value passed to panic(). May be a string, an
	// error, or any other type per Go semantics.
	Value any

	// Stack is the goroutine stack at the panic point.
	Stack []byte
}

// Error implements the error interface.
func (e *PanicError) Error() string {
	return fmt.Sprintf("panic recovered: %v", e.Value)
}

// Unwrap returns the wrapped error when Value itself is an error
// (a common Go convention: panic(fmt.Errorf("...")) ). Otherwise
// returns nil and errors.Is falls through.
func (e *PanicError) Unwrap() error {
	if err, ok := e.Value.(error); ok {
		return err
	}
	return nil
}

// ErrPanic is the sentinel that errors.Is matches every PanicError
// against. Useful for blanket "did any node panic" checks at the
// run boundary.
var ErrPanic = errors.New("graph: panic recovered")

// Is implements the errors.Is contract for PanicError so callers
// can errors.Is(err, ErrPanic) regardless of the inner value.
func (e *PanicError) Is(target error) bool {
	return target == ErrPanic
}

// captureStack returns a 4 KiB snapshot of the current goroutine's
// stack. We size it generously so panics from deeply-nested ReAct
// loops still show the relevant frames.
func captureStack() []byte {
	const bufSize = 4096
	buf := make([]byte, bufSize)
	n := runtime.Stack(buf, false)
	return buf[:n]
}
