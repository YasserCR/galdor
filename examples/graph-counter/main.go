// Command graph-counter is the smallest non-trivial graph example:
// a single node that increments a counter, with a conditional self-
// loop that exits when the limit is reached.
//
//	go run ./examples/graph-counter
//
// It exists to show three things that any real agent graph also
// needs:
//
//  1. State is a regular Go struct; nodes take and return it by
//     value.
//  2. AddConditionalEdge installs a router that picks the next node
//     from the current state — that's how loops and branches both
//     work.
//  3. Stream() emits typed events so a UI / observability layer can
//     see node transitions in real time without polling.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/YasserCR/galdor/pkg/graph"
)

type state struct {
	N     int
	Limit int
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	r, err := graph.New[state]().
		AddNode("inc", func(_ context.Context, s state) (state, error) {
			s.N++
			return s, nil
		}).
		AddEdge(graph.START, "inc").
		AddConditionalEdge("inc", func(s state) string {
			if s.N >= s.Limit {
				return graph.END
			}
			return "inc"
		}).
		Compile()
	if err != nil {
		return err
	}

	fmt.Println("--- Invoke ---")
	final, err := r.Invoke(context.Background(), state{Limit: 5})
	if err != nil {
		return err
	}
	fmt.Printf("final N = %d (limit was %d)\n\n", final.N, 5)

	fmt.Println("--- Stream ---")
	ch := r.Stream(context.Background(), state{Limit: 3})
	for ev := range ch {
		switch ev.Type {
		case graph.EventRunStart:
			fmt.Printf("step %d  start    -> %s\n", ev.Step, ev.Node)
		case graph.EventNodeStart:
			fmt.Printf("step %d  enter    %s   (N=%d)\n", ev.Step, ev.Node, ev.State.N)
		case graph.EventNodeEnd:
			fmt.Printf("step %d  exit     %s   (N=%d)\n", ev.Step, ev.Node, ev.State.N)
		case graph.EventEdgeTraversed:
			fmt.Printf("step %d  -> %s\n", ev.Step, ev.Node)
		case graph.EventRunEnd:
			fmt.Printf("step %d  end      (N=%d)\n", ev.Step, ev.State.N)
		case graph.EventError:
			return ev.Err
		}
	}
	return nil
}
