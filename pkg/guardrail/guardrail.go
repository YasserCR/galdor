package guardrail

import (
	"context"
	"errors"
	"fmt"

	"github.com/YasserCR/galdor/pkg/schema"
)

// ErrBlocked is the sentinel that identifies a message vetoed by a guard.
// errors.Is(err, ErrBlocked) reports true for any block returned by
// CheckInput or CheckOutput, regardless of which guard triggered it.
var ErrBlocked = errors.New("guardrail: blocked")

// ErrEvalFailed is the sentinel a guard wraps when it could not evaluate
// the message at all — e.g. an LLM judge whose provider call failed, or
// one that is misconfigured. CheckInput / CheckOutput still fail the run
// (the content stays unvetted, so stopping is the safe outcome), but the
// returned error is not a *BlockError and does not match ErrBlocked: an
// infrastructure failure is not a policy verdict, and callers or metrics
// that count blocks must be able to tell the two apart.
var ErrEvalFailed = errors.New("guard evaluation failed")

// Guard is the shared identity of input and output guards: a stable
// Name used in error messages and traces.
type Guard interface {
	Name() string
}

// InputGuard validates a message before it reaches the model. A non-nil
// error returned from CheckInput blocks the message and fails the run.
type InputGuard interface {
	Guard
	CheckInput(ctx context.Context, msg schema.Message) error
}

// OutputGuard validates a message produced by the model before it is
// recorded or returned to the caller. A non-nil error returned from
// CheckOutput blocks the message and fails the run.
type OutputGuard interface {
	Guard
	CheckOutput(ctx context.Context, msg schema.Message) error
}

// Kind classifies how a guard evaluates a message. It is descriptive
// metadata — the runtime never branches on it — surfaced on BlockError
// and available to observability for tagging spans (e.g. to account for
// the extra model call an LLM-backed guard makes).
type Kind uint8

// Guard kinds.
const (
	// KindDeterministic is a pure-code guard (regex, denylist, schema
	// check, ...) that makes no model call. It is also the assumed kind
	// for guards that do not self-describe.
	KindDeterministic Kind = iota

	// KindLLM is a guard that makes its own model call to judge the
	// message (LLM-as-judge).
	KindLLM
)

// String returns a stable lowercase label for tracing.
func (k Kind) String() string {
	if k == KindLLM {
		return "llm"
	}
	return "deterministic"
}

// Kinded is an optional interface a Guard may implement to self-describe
// its evaluation mechanism. The core contract only requires Name + Check;
// KindOf falls back to KindDeterministic for guards that do not
// implement it.
type Kinded interface {
	Kind() Kind
}

// KindOf reports how g evaluates messages. Guards that do not implement
// Kinded are assumed deterministic.
func KindOf(g Guard) Kind {
	if k, ok := g.(Kinded); ok {
		return k.Kind()
	}
	return KindDeterministic
}

// BlockError is returned (as a wrapper) when a guard vetoes a message.
// It carries the guard's name, the guard's Kind, and the guard's own
// error, and it matches ErrBlocked via errors.Is. The guard's error
// remains reachable through errors.Is / errors.As via Unwrap.
type BlockError struct {
	Guard string
	Kind  Kind
	Err   error
}

// Error implements the error interface.
func (e *BlockError) Error() string {
	if e.Err == nil {
		return "guardrail: " + e.Guard + " blocked"
	}
	return "guardrail: " + e.Guard + ": " + e.Err.Error()
}

// Unwrap exposes the guard's own error.
func (e *BlockError) Unwrap() error { return e.Err }

// Is makes BlockError match ErrBlocked.
func (e *BlockError) Is(target error) bool { return target == ErrBlocked }

// CheckInput runs every guard in order against msg and returns the first
// block as a *BlockError. Nil guards are skipped; when no guard vetoes,
// CheckInput returns nil. An error matching ErrEvalFailed still fails the
// run but is returned as a plain wrapped error, not a *BlockError.
func CheckInput(ctx context.Context, guards []InputGuard, msg schema.Message) error {
	for _, g := range guards {
		if g == nil {
			continue
		}
		if err := g.CheckInput(ctx, msg); err != nil {
			return wrapGuardError(g, err)
		}
	}
	return nil
}

// CheckOutput runs every guard in order against msg and returns the first
// block as a *BlockError. Nil guards are skipped; when no guard vetoes,
// CheckOutput returns nil. An error matching ErrEvalFailed still fails the
// run but is returned as a plain wrapped error, not a *BlockError.
func CheckOutput(ctx context.Context, guards []OutputGuard, msg schema.Message) error {
	for _, g := range guards {
		if g == nil {
			continue
		}
		if err := g.CheckOutput(ctx, msg); err != nil {
			return wrapGuardError(g, err)
		}
	}
	return nil
}

// wrapGuardError attaches the guard's identity to its error: policy
// vetoes become a *BlockError, evaluation failures keep their
// ErrEvalFailed identity instead of masquerading as a block.
func wrapGuardError(g Guard, err error) error {
	if errors.Is(err, ErrEvalFailed) {
		return fmt.Errorf("guardrail: %s: %w", g.Name(), err)
	}
	return &BlockError{Guard: g.Name(), Kind: KindOf(g), Err: err}
}

// InputGuardFunc adapts a plain function to the InputGuard interface.
// ID is the guard's Name; Check is the validation function invoked by
// CheckInput.
type InputGuardFunc struct {
	ID    string
	Check func(ctx context.Context, msg schema.Message) error
}

// Name returns the guard's identifier.
func (g InputGuardFunc) Name() string { return g.ID }

// CheckInput runs the adapted function.
func (g InputGuardFunc) CheckInput(ctx context.Context, msg schema.Message) error {
	return g.Check(ctx, msg)
}

// OutputGuardFunc adapts a plain function to the OutputGuard interface.
// ID is the guard's Name; Check is the validation function invoked by
// CheckOutput.
type OutputGuardFunc struct {
	ID    string
	Check func(ctx context.Context, msg schema.Message) error
}

// Name returns the guard's identifier.
func (g OutputGuardFunc) Name() string { return g.ID }

// CheckOutput runs the adapted function.
func (g OutputGuardFunc) CheckOutput(ctx context.Context, msg schema.Message) error {
	return g.Check(ctx, msg)
}
