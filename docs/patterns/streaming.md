# Streaming responses through a graph

galdor's provider abstraction supports token streaming (`Provider.Stream`), and the graph runtime supports event streaming (`Runnable.Stream`). This guide shows how to plumb both together so the assistant's tokens flow from the LLM through the graph to your downstream consumer (WebSocket, SSE, Telegram, terminal, ...) with `context.Context` cancel working end-to-end.

## When to use

- Chat UIs that render tokens as they arrive instead of waiting for the final message.
- Long answers where TTFB matters (terminal CLIs, voice assistants).
- Live dashboards that visualize the agent's intermediate state per node hop.

If your consumer is a queue worker or a batch script, do **not** stream — `Generate` is simpler, cheaper to instrument, and easier to retry.

## Two streams, two layers

galdor exposes two separate streaming surfaces that work at different layers:

| Layer | API | What flows |
|---|---|---|
| Provider | `provider.StreamReader` from `Provider.Stream(ctx, req)` | token deltas (`EventContentDelta`), the terminal `EventMessageStop` with usage |
| Graph | `<-chan graph.Event[S]` from `Runnable.StreamWith(ctx, state, opts)` (or `Stream(ctx, state)` without options) | one event per node hop (`EventNodeStart`, `EventNodeEnd`), terminal `EventRunEnd` with the final state |

You can use either independently. To plumb LLM tokens **through** the graph to the consumer, you bridge them yourself inside the node body.

## Pattern 1: stream tokens directly to the consumer

For the common case — one LLM call, no graph — call `Provider.Stream` and forward the deltas.

```go
reader, err := prov.Stream(ctx, provider.Request{
	Model:    "claude-haiku-4-5",
	Messages: []schema.Message{schema.UserMessage(prompt)},
})
if err != nil {
	return err
}
defer reader.Close()

for {
	ev, err := reader.Recv(ctx)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	switch ev.Type {
	case provider.EventContentDelta:
		if _, err := io.WriteString(consumer, string(ev.ContentDelta)); err != nil {
			return err
		}
	case provider.EventMessageStop:
		// Final usage available on ev.Usage.
	}
}
```

This is what a thin chat HTTP endpoint looks like. `ctx` cancellation propagates: when the consumer closes the connection, cancel the ctx and the stream tears down.

## Pattern 2: stream tokens from inside a graph node

When the graph orchestrates multiple steps and one of them is an LLM call whose tokens you want exposed:

1. Add a channel to the graph state (`Tokens chan<- string`).
2. Inside the node body, call `Provider.Stream` and push each delta onto the channel.
3. The outer caller reads from the channel concurrently with consuming `Runnable.Stream` events.

```go
type State struct {
	Question string
	Answer   string
	Tokens   chan<- string // write-only inside nodes
}

llmNode := func(ctx context.Context, s State) (State, error) {
	reader, err := prov.Stream(ctx, provider.Request{
		Model:    "claude-haiku-4-5",
		Messages: []schema.Message{schema.UserMessage(s.Question)},
	})
	if err != nil {
		return s, err
	}
	defer reader.Close()

	var b strings.Builder
	for {
		ev, err := reader.Recv(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return s, err
		}
		if ev.Type == provider.EventContentDelta {
			tok := string(ev.ContentDelta)
			b.WriteString(tok)
			select {
			case s.Tokens <- tok:
			case <-ctx.Done():
				return s, ctx.Err()
			}
		}
	}
	s.Answer = b.String()
	return s, nil
}
```

The outer caller:

```go
tokens := make(chan string, 64)
state := State{Question: q, Tokens: tokens}

events := r.StreamWith(ctx, state, graph.RunOptions[State]{RunID: runID})

go func() {
	for tok := range tokens {
		writeToConsumer(tok)
	}
}()
for ev := range events {
	switch ev.Type {
	case graph.EventNodeStart:
		writeMetadata("node", ev.Node, "started")
	case graph.EventRunEnd:
		writeMetadata("done", ev.State.Answer)
	}
}
close(tokens)
```

## Gotchas

- **Stream spans end on `Close()` or EOF.** `observability.InstrumentProvider`'s wrapped stream finalizes the span the first time `Recv` returns `io.EOF` or `Close` is called. Don't leak the reader — `defer reader.Close()` is mandatory.
- **Tokens channels need a buffer or a reader.** A 0-buffer channel deadlocks if the consumer is slow. Pick a buffer size that matches your consumer's latency budget, or use `select` with `ctx.Done()` everywhere you send.
- **Don't combine streaming with `provider.Retry`.** The retry wrapper does not retry mid-stream because partial deltas already shipped — see the retry [gotcha](../concepts/provider.md). For stream retries, wrap your *outer* caller and reset the consumer too.
- **Replay does not support streamed runs.** `pkg/replay` records `Generate` calls only. If you need replay for a streaming run, fold the stream to non-streaming at record time (collect deltas, emit one `Generate`-shaped fixture call). The [replay](../concepts/replay.md) guide notes this explicitly.
- **`StripThinkingBlocks` buffers across deltas.** If you wrap the provider with the thinking-block middleware (see [provider](../concepts/provider.md)), expect a brief stall while the middleware waits for a closing `</think>` tag. Acceptable for chat UIs, audible for voice.

## See also

- [Provider](../concepts/provider.md) — `Stream`, `StreamReader`, the event taxonomy.
- [Graph](../concepts/graph.md) — `Runnable.Stream` and `graph.Event[S]`.
- [Observability](../concepts/observability.md) — how stream spans are accounted for.
- [`examples/agent-react`](../../examples/agent-react/) — non-streaming baseline you can compare against.
