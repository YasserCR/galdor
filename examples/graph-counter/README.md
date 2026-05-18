# examples/graph-counter

The smallest non-trivial graph: one node that increments a counter,
with a conditional self-loop that exits when the limit is reached.

## Run

```bash
go run ./examples/graph-counter
```

Expected output:

```
--- Invoke ---
final N = 5 (limit was 5)

--- Stream ---
step 0  start    -> inc
step 1  enter    inc   (N=0)
step 1  exit     inc   (N=1)
step 1  -> inc
...
step 3  -> __end__
step 3  end      (N=3)
```

## What it shows

- **State is a value.** `state` is a regular Go struct; nodes take
  and return it by value. The runtime never mutates your state
  behind your back.
- **Conditional loops are just routers.** `AddConditionalEdge`
  installs a function that picks the next node from the current
  state — a self-edge plus a "exit when done" branch is all you
  need for a loop.
- **Streaming events are typed.** `Runnable.Stream` returns
  `<-chan Event[S]`; consumers `switch` on `EventType` and read the
  fields relevant to that variant. Cancellation through `context`
  closes the channel cleanly.
- **Termination is bounded.** `Runnable.MaxSteps` (default 100)
  catches misrouted conditionals that would otherwise loop forever.
