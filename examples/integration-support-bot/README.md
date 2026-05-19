# integration-support-bot

End-to-end multi-agent customer-support bot built on galdor. A
routing supervisor delegates each incoming message to one of three
specialists, each with its own tool registry.

```
user message
    │
    ▼
┌───────────────┐
│  Supervisor   │  ← decides which specialist handles this turn
└───────────────┘
    │
    ├──→ billing      (lookup_invoice, issue_refund)
    ├──→ technical    (ticket_status, create_ticket)
    └──→ general      (faq_search)
```

## Why this is useful

* The shape — supervisor on top, specialized ReAct agents below — is
  the most common production pattern for customer-facing agents.
* Each specialist has its own tool registry so a billing prompt never
  sees the `create_ticket` tool and vice versa. Reduces the
  hallucination surface.
* The whole chain is one linked trace in the dashboard. Every LLM
  call, every routing decision, every tool invocation shows up under
  one parent run.

## Running it

```bash
go run ./examples/integration-support-bot
```

The `Provider` is scripted, so no API key is required. You'll see
three scenarios run end-to-end (billing question, technical issue,
FAQ question), each routed to the correct specialist.

## Inspecting the trace

```bash
galdor ui --db ./traces.db
# open http://127.0.0.1:7777
```

Click any run, then the **steps view** to walk through it
conversation-style: supervisor decision → specialist tool call →
specialist tool result → specialist final answer → supervisor
finishing call.

## Adapting it to a real LLM

The only thing that's scripted is the `Provider`. To go live, replace
the `observability.InstrumentProvider(buildScriptedProvider(), ...)` line with:

```go
prov := observability.InstrumentProvider(
    anthropic.MustNew(anthropic.Config{APIKey: os.Getenv("ANTHROPIC_API_KEY")}),
    tracer, observability.WithCaptureContent(true))
```

and change the model string to a real model ID (e.g.,
`claude-haiku-4-5`). The supervisor and specialist wiring stays
identical.

## Files

* `main.go` — wiring + scripted provider + tool definitions
