# Cost tracking and budget enforcement

## When to use this pattern

The model went off the rails and burned $200 on retries before
anyone noticed. You want a per-run cap that aborts the agent the
moment a budget is exceeded, plus a running tally you can surface
to operators and users.

The pattern is a `Provider` middleware: wrap any
`provider.Provider`, count input + output tokens on every
response, refuse further calls (or refuse the next call) once the
configured cap is hit. Because the wrapper is itself a
`provider.Provider`, every agent in this repo composes with it
without changes.

## Minimal sketch

```go
type BudgetProvider struct {
    inner        provider.Provider
    budgetTokens int64
    prices       map[string]Pricing
    tokensIn     atomic.Int64
    tokensOut    atomic.Int64
}

var ErrBudgetExceeded = errors.New("budget exceeded")

func (b *BudgetProvider) Generate(ctx context.Context, req provider.Request) (*provider.Response, error) {
    if b.budgetTokens > 0 {
        if used := b.tokensIn.Load() + b.tokensOut.Load(); used >= b.budgetTokens {
            return nil, fmt.Errorf("%w: %d used, budget %d", ErrBudgetExceeded, used, b.budgetTokens)
        }
    }
    resp, err := b.inner.Generate(ctx, req)
    if err != nil {
        return resp, err
    }
    b.tokensIn.Add(int64(resp.Usage.InputTokens))
    b.tokensOut.Add(int64(resp.Usage.OutputTokens))
    if b.budgetTokens > 0 {
        if used := b.tokensIn.Load() + b.tokensOut.Load(); used >= b.budgetTokens {
            return resp, fmt.Errorf("%w: this call pushed total to %d", ErrBudgetExceeded, used)
        }
    }
    return resp, nil
}
```

(Full implementation with pricing report, `Name()`,
`Capabilities()`, `Stream()` in the example link below.)

## Walkthrough

1. **Wrap.** Construct a `BudgetProvider` around your real
   provider. Pass the same wrapped instance to every agent that
   should share the budget.
2. **Pre-call refuse.** Before the inner `Generate` runs, check
   the running total. If already over, return `ErrBudgetExceeded`
   without making the network call.
3. **Tally.** On a successful response, add `resp.Usage.InputTokens`
   and `resp.Usage.OutputTokens` to atomic counters. Atomic so
   concurrent tool fan-out (parallel `Generate` calls in the same
   run) doesn't undercount.
4. **Post-call fail-on-overshoot.** If this call's usage pushed
   the total past the cap, return both the response *and*
   `ErrBudgetExceeded`. The agent loop sees the error and aborts;
   you still have the response for inspection / billing.

## Pre-call refuse vs post-call fail-on-overshoot

| | Pre-call refuse | Post-call fail-on-overshoot |
|---|---|---|
| When it fires | running total `>=` cap before the call | this call's response pushed the total past the cap |
| Cost of the gate | zero — no API call made | one over-budget API call |
| Use when | hard cap matters more than completing the current step | you want the agent to surface its best answer for the over-budget request before aborting |

Most production setups use both, as in the sketch: pre-call to
short-circuit obviously-doomed continuations, post-call to catch
the one call that crossed the line.

## Pricing tables

`provider.Response.Usage` gives you tokens; pricing is in
dollars-per-1000-tokens and belongs in your code, not the
framework (prices change; the framework shouldn't ship stale
numbers). Index by model ID:

```go
type Pricing struct {
    InputPer1K, OutputPer1K float64
}

prices := map[string]Pricing{
    "claude-haiku-4-5":  {InputPer1K: 0.0008, OutputPer1K: 0.004},
    "claude-sonnet-4-5": {InputPer1K: 0.003,  OutputPer1K: 0.015},
    "gpt-4o-mini":       {InputPer1K: 0.00015, OutputPer1K: 0.0006},
}

func (b *BudgetProvider) Report(model string) Report {
    p := b.prices[model]
    in := b.tokensIn.Load()
    out := b.tokensOut.Load()
    cost := float64(in)/1000*p.InputPer1K + float64(out)/1000*p.OutputPer1K
    return Report{TokensIn: in, TokensOut: out, CostUSD: cost}
}
```

Unknown models should fall through to a `PriceDefault` rather
than crashing — better to over-estimate cost on an unrecognized
model than to miss it entirely.

## Common variations

### Per-tenant / per-user budgets

The wrapper is just a Go struct. Build one per tenant, key them in
a map, and select the right one from your handler. Don't share an
atomic across tenants — that's a thundering herd waiting to
happen.

### Budget = 0 (accounting only)

Set `BudgetTokens = 0` and the wrapper becomes a pure tally:
no enforcement, just a counter you can call `Report()` on after
the run. Useful for "what does this prompt cost?" experiments.

### Budget per run, not global

Construct a fresh `BudgetProvider` per `agent.Run` / per request.
Tokens consumed by one request don't count against the next.

### Pair with retry

`provider.Retry` wraps a `BudgetProvider` (or the other way
around). Order matters:

- `Retry(Budget(real))` — retries don't consume extra budget on
  the failure path (the inner Generate failed, so usage wasn't
  added), but a transient flake plus retry plus success spends
  the same as one normal call.
- `Budget(Retry(real))` — the retry wrapper is hidden from the
  budget; a 5-retry call shows up as one Generate to the
  accountant. Usually the more accurate accounting.

### Stream support

The shipped example skips streaming (`Stream` returns
`provider.ErrUnsupported`). If your agent uses streaming, wrap the
returned `StreamReader` in a counting filter that accumulates
tokens as chunks arrive.

## Gotchas

- **`Usage` is provider-reported.** Mock providers and some
  third-party endpoints return zero — your budget never trips.
  In tests, use the in-tree estimator (chars/4) or assert the
  values explicitly.
- **One run, one provider.** Don't share a `BudgetProvider`
  across unrelated requests unless you actually want the budget
  to be cumulative.
- **The error is normal flow.** Agents propagate errors from
  `Generate` up to the run boundary. Check
  `errors.Is(err, ErrBudgetExceeded)` at the run-level call site
  to distinguish "budget tripped" from "provider failed".
- **Capabilities pass through.** `BudgetProvider.Capabilities()`
  should return the inner provider's capabilities verbatim. The
  shipped example does — copy that, otherwise capability-aware
  agent validation will get confused.

## Links

- Runnable example: [examples/integration-cost-tracked](../../examples/integration-cost-tracked/)
- Concept: [provider](../concepts/provider.md)
- Related: [human-in-the-loop](human-in-the-loop.md) — gate
  high-cost actions behind explicit approval.
