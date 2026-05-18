// Command graph-interrupt demonstrates the human-in-the-loop cycle
// built on pkg/graph: Invoke runs until it hits an interrupt-gated
// node, persists a checkpoint, and returns ErrInterrupted. The
// caller inspects the saved state (here we simulate a human edit)
// and calls Resume to continue from exactly where the run paused.
//
//	go run ./examples/graph-interrupt
//
// The simulated workflow is a tiny editorial pipeline: a draft is
// written, a reviewer must approve (the interrupt), and the
// approved draft is published.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/YasserCR/galdor/pkg/graph"
)

type post struct {
	Draft     string
	Approved  bool
	Published bool
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	g := graph.New[post]().
		AddNode("write", func(_ context.Context, p post) (post, error) {
			p.Draft = "Galdor: speak your AI agents into being."
			return p, nil
		}).
		AddNode("review", func(_ context.Context, p post) (post, error) {
			// In a real workflow this is where the human's verdict is
			// recorded. Because we resume past the interrupt with the
			// state already edited, the body here is just a sanity
			// check.
			if !p.Approved {
				return p, fmt.Errorf("review: refusing to proceed without Approved=true")
			}
			return p, nil
		}).
		AddNode("publish", func(_ context.Context, p post) (post, error) {
			p.Published = true
			return p, nil
		}).
		AddEdge(graph.START, "write").
		AddEdge("write", "review").
		AddEdge("review", "publish").
		AddEdge("publish", graph.END).
		InterruptBefore("review")

	r, err := g.Compile()
	if err != nil {
		return err
	}
	cp := graph.NewMemoryCheckpointer[post]()
	const runID = "post-1"

	// First leg: write the draft, then pause for review.
	paused, err := r.InvokeWith(context.Background(), post{}, graph.RunOptions[post]{
		Checkpointer: cp,
		RunID:        runID,
	})
	switch {
	case errors.Is(err, graph.ErrInterrupted):
		fmt.Printf("paused at review: %q\n", strings.TrimSpace(paused.Draft))
	case err != nil:
		return err
	default:
		fmt.Println("ran straight through — interrupt did not fire (unexpected)")
		return nil
	}

	// Simulated human-in-the-loop: a reviewer reads the draft, then
	// sets Approved=true. We pass the edited state through
	// OverrideState so the next leg uses our verdict.
	approved := paused
	approved.Approved = true
	fmt.Println("human reviewer: approving the draft")

	final, err := r.Resume(context.Background(), graph.RunOptions[post]{
		Checkpointer:  cp,
		RunID:         runID,
		OverrideState: &approved,
	})
	if err != nil {
		return err
	}
	fmt.Printf("published=%v draft=%q\n", final.Published, strings.TrimSpace(final.Draft))

	// Trace summary — useful for debugging or building a UI.
	fmt.Println("\ncheckpoint history:")
	for _, ck := range cp.History(runID) {
		fmt.Printf("  step=%d node=%-7s reason=%s\n", ck.Step, ck.Node, ck.Reason)
	}
	return nil
}
