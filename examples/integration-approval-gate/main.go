// Command integration-approval-gate demonstrates the human-in-the-loop
// pattern: a banking-style agent pauses before any irreversible
// action (here: transfer money) so a human can approve, reject, or
// edit the transfer before it goes through.
//
// What this exercises end-to-end:
//
//   - InterruptBefore to gate a critical node
//   - MemoryCheckpointer to persist state across the pause
//   - Resume with OverrideState to inject the human's decision
//   - Observability so the pause + resume show up as one linked run
//
// The "human" in this example is a function that picks an answer
// based on the transfer details. In a real deployment that's the UI
// that someone clicks on, the Slack notification someone responds
// to, or the approval workflow in your bank's compliance system.
//
// Run with:
//
//	go run ./examples/integration-approval-gate
//
// Then inspect the trace:
//
//	galdor ui --db ./traces.db
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/YasserCR/galdor/pkg/graph"
	"github.com/YasserCR/galdor/pkg/observability"
)

const dbPath = "./traces.db"

// TransferState is the value that flows through the graph. The
// "validate -> review -> execute" pipeline gates the execute step
// behind an InterruptBefore so a human can mutate Approved /
// CounterApproved / Note before the transfer actually happens.
type TransferState struct {
	// Inputs
	FromAccount string
	ToAccount   string
	Amount      float64

	// Set by validate
	Risk string // "low", "medium", "high"

	// Set by the human (via Resume with OverrideState)
	Approved        bool
	CounterApproved bool   // a second human's sign-off for HIGH risk
	Note            string // human's free-form note

	// Set by execute
	TxID string
}

func main() {
	ctx := context.Background()

	exporter, err := observability.NewSQLiteExporter(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tp.Shutdown(shutCtx)
	}()
	tracer := tp.Tracer("integration-approval-gate")

	r := buildTransferGraph()
	hooks := observability.TraceHooks[TransferState](tracer)
	ckpt := graph.NewMemoryCheckpointer[TransferState]()

	scenarios := []TransferState{
		{FromAccount: "acc-101", ToAccount: "acc-202", Amount: 75.00},
		{FromAccount: "acc-101", ToAccount: "acc-203", Amount: 6500.00},  // HIGH risk
		{FromAccount: "acc-101", ToAccount: "acc-204", Amount: 12000.00}, // HIGH risk → reject
	}

	for i, init := range scenarios {
		runID := fmt.Sprintf("transfer-%d-%d", time.Now().Unix(), i+1)
		fmt.Printf("\n=== scenario %d ===\n", i+1)
		fmt.Printf("request: %s → %s ($%.2f)\n", init.FromAccount, init.ToAccount, init.Amount)

		// Phase 1: run the agent. It will pause at `execute`.
		_, err := r.InvokeWith(ctx, init, graph.RunOptions[TransferState]{
			RunID:        runID,
			Checkpointer: ckpt,
			Hooks:        hooks,
		})
		if !errors.Is(err, graph.ErrInterrupted) {
			fmt.Printf("  unexpected: agent did NOT pause for approval (err=%v)\n", err)
			continue
		}

		// Phase 2: read the checkpoint, show what would happen,
		// run the human's decision function.
		ck, _, _ := ckpt.Load(ctx, runID)
		fmt.Printf("  paused at: %s  (risk=%s)\n", ck.Node, ck.State.Risk)
		decided := promptHuman(ck.State)
		fmt.Printf("  human says: approved=%v counter-approved=%v note=%q\n",
			decided.Approved, decided.CounterApproved, decided.Note)

		// Phase 3: if the human rejected, we don't resume — the
		// transfer is abandoned. This is the operational pattern:
		// the run stays in checkpoint-state forever (auditable),
		// nothing irreversible happened.
		if !decided.Approved {
			fmt.Printf("  result: transfer rejected by human, NOT executed\n")
			continue
		}

		// Otherwise resume with the human's decision injected.
		final, err := r.Resume(ctx, graph.RunOptions[TransferState]{
			RunID:         runID,
			Checkpointer:  ckpt,
			Hooks:         hooks,
			OverrideState: &decided,
		})
		if err != nil {
			fmt.Printf("  resume err: %v\n", err)
			continue
		}
		fmt.Printf("  result: transfer executed, tx_id=%s\n", final.TxID)
	}

	if err := tp.ForceFlush(ctx); err != nil {
		log.Printf("flush: %v", err)
	}
	fmt.Println()
	fmt.Println("Traces stored in:", dbPath)
	fmt.Println("  galdor ui --db", dbPath)
}

// buildTransferGraph compiles the validate → review → execute
// pipeline. InterruptBefore("execute") forces the runtime to pause
// and persist the state immediately before the irreversible step.
func buildTransferGraph() *graph.Runnable[TransferState] {
	validate := func(_ context.Context, s TransferState) (TransferState, error) {
		switch {
		case s.Amount >= 5000:
			s.Risk = "high"
		case s.Amount >= 500:
			s.Risk = "medium"
		default:
			s.Risk = "low"
		}
		return s, nil
	}

	// review is a no-op step that exists to give the trace a
	// visible "we evaluated this and decided to pause" frame.
	review := func(_ context.Context, s TransferState) (TransferState, error) {
		return s, nil
	}

	execute := func(_ context.Context, s TransferState) (TransferState, error) {
		// Defensive checks: enforce the policies our human-in-the-loop
		// is supposed to honor, in case Resume was called with a
		// state that didn't actually grant approval.
		if !s.Approved {
			return s, errors.New("approval-gate: execute called without Approved=true")
		}
		if s.Risk == "high" && !s.CounterApproved {
			return s, errors.New("approval-gate: HIGH risk transfer requires CounterApproved=true")
		}
		s.TxID = fmt.Sprintf("TX-%d", time.Now().UnixNano())
		return s, nil
	}

	g := graph.New[TransferState]().
		AddNode("validate", validate).
		AddNode("review", review).
		AddNode("execute", execute).
		AddEdge(graph.START, "validate").
		AddEdge("validate", "review").
		AddEdge("review", "execute").
		AddEdge("execute", graph.END).
		InterruptBefore("execute")

	r, err := g.Compile()
	if err != nil {
		log.Fatal(err)
	}
	return r
}

// promptHuman simulates the human approver. In a real deployment
// this is where the UI / Slack bot / compliance workflow lives.
// Returns a NEW TransferState (don't mutate the checkpoint's copy).
func promptHuman(paused TransferState) TransferState {
	out := paused
	out.Approved = true

	// Demo policy: HIGH risk requires a second sign-off; reject
	// anything above $10k outright.
	switch {
	case paused.Amount > 10000:
		out.Approved = false
		out.Note = "Rejected: above $10k per-transfer cap."
	case paused.Risk == "high":
		out.CounterApproved = true
		out.Note = strings.TrimSpace(fmt.Sprintf(
			"HIGH risk transfer ($%.2f). Second signer authorized.", paused.Amount,
		))
	default:
		out.Note = "Standard approval."
	}
	return out
}
