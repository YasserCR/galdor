# integration-cost-tracked

Per-run **budget enforcement** for LLM costs. A `BudgetProvider`
wraps any real `provider.Provider`, tallies input + output tokens
across every call (including the parallel ones produced by tool
fan-out), and hard-aborts the run the moment the cumulative usage
crosses the configured cap.

This is the production answer to *"the model went into a tool loop
and burned $200 of credits before someone noticed."*

## How it works

```
agent.NewReAct ──► BudgetProvider ──► anthropic/openai/...
                       │
                       └─► atomic counters: tokens in / out
                       └─► price table: $ per 1k tokens by model
                       └─► fail-closed once the cap is crossed
```

Two enforcement points inside `Generate`:

1. **Pre-call**: if the running total already meets/exceeds the
   budget, return `ErrBudgetExceeded` immediately. No tokens
   spent on this call.
2. **Post-call**: if THIS call's usage pushed the total past the
   budget, the response is still returned (so the caller can see
   what they got for the over-budget spend) but the error is
   returned with it. The agent loop sees the error and aborts.

The two-point check matters: a single fat call can blow past
the budget on its own, and you want to abort BEFORE the loop
keeps issuing follow-ups.

## Running it

```bash
go run ./examples/integration-cost-tracked
```

You'll see three scenarios run against the same wrapper:

* **small** (budget 10k tokens): well under, completes normally.
* **medium** (budget 10k tokens): a chunkier response but still
  comfortably under budget.
* **huge** (budget 2k tokens): the response alone blows the cap,
  so the run aborts and the wrapper reports cumulative usage +
  dollar cost.

Output is roughly:

```
=== scenario: huge (budget = 2000 tokens) ===
  aborted: budget exceeded mid-run
  usage: 10 in + 2250 out = 2260 tokens, cost ~$0.0113
```

## Adapting it to a real LLM

```go
real := anthropic.MustNew(anthropic.Config{APIKey: os.Getenv("ANTHROPIC_API_KEY")})
bp := NewBudgetProvider(BudgetConfig{
    Inner:        real,
    BudgetTokens: 100_000, // ~ $0.50 worth of Haiku
    Prices: map[string]Pricing{
        "claude-haiku-4-5": {InputPer1K: 0.0008, OutputPer1K: 0.004},
    },
    PriceDefault: Pricing{InputPer1K: 0.01, OutputPer1K: 0.03},
})

r, _ := agent.NewReAct(agent.Config{
    Provider: bp,
    Model:    "claude-haiku-4-5",
    Tools:    yourRegistry,
})

final, err := r.Invoke(ctx, agent.State{Messages: []schema.Message{schema.UserMessage("...")}})
report := bp.Report("claude-haiku-4-5")
log.Printf("spent $%.4f on %d tokens", report.CostUSD, report.TokensIn+report.TokensOut)
```

## Patterns this enables

* **Per-customer budgets**: build one `BudgetProvider` per customer
  request, configured from your billing system. When the customer's
  paid quota is exhausted, the wrapper returns an error you can
  surface as a 402 to your API.
* **Per-feature caps**: different routes get different budgets.
  Marketing-copy generator gets a small cap; deep-research agent
  gets a bigger one.
* **Observability without enforcement**: pass `BudgetTokens: 0`
  and use `Report()` to log spend per run without ever blocking.

## Files

* `main.go` — `BudgetProvider`, `Pricing`, `Report` + the three demo scenarios
