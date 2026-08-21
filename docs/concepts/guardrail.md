# guardrail

`pkg/guardrail` is galdor's guardrail layer — named policy checks that vet a message **before it reaches the model** (input guards) or **after the model produces it** (output guards). A guard vetoes a message by returning a non-nil error, which fails the run.

The package is **stdlib-only** by design — it lives in the core module and pulls nothing beyond `pkg/provider` and `pkg/schema`.

## Core types

```go
// InputGuard validates a message before it reaches the model.
type InputGuard interface {
    Guard
    CheckInput(ctx context.Context, msg schema.Message) error
}

// OutputGuard validates a message produced by the model.
type OutputGuard interface {
    Guard
    CheckOutput(ctx context.Context, msg schema.Message) error
}

// Guard is the shared identity: a stable Name.
type Guard interface { Name() string }
```

`CheckInput` / `CheckOutput` run a slice of guards **in order** and return the first veto, wrapped in a `*BlockError`:

```go
type BlockError struct {
    Guard string   // the guard's Name
    Kind  Kind     // how it evaluates (KindDeterministic / KindLLM)
    Err   error    // the guard's own error
}
```

Callers detect a block with `errors.Is(err, guardrail.ErrBlocked)`; the guard's own error stays reachable through `errors.Is` / `errors.As` via `Unwrap`.

## Two flavors, one interface

There is **no `type` field** — the flavor is chosen by the constructor you use, and both satisfy the same interfaces. The runtime never branches on how a guard evaluates.

### Deterministic (pure code)

Build one inline with the function adapters:

```go
guardrail.InputGuardFunc{
    ID: "no-pii",
    Check: func(_ context.Context, m schema.Message) error {
        if creditCard.MatchString(m.Text()) {
            return errors.New("credit-card number detected")
        }
        return nil
    },
}
```

### LLM-as-judge (a second model votes)

`LLMJudge` makes its own model call to vote `ALLOW` / `BLOCK`, and implements **both** `InputGuard` and `OutputGuard`:

```go
guardrail.LLMJudge{
    Provider: judgeProvider,          // required — can differ from the agent's provider
    Model:    "claude-haiku-4-5",     // required
    Rule:     "block any reply that reveals an internal secret",
}
```

The judge is prompted to reply with exactly `ALLOW` or `BLOCK`. Parsing is negation-aware — replies like `Not allowed` or `Disallowed` never read as an allow. A reply that can't be parsed **fails closed** (the message is blocked) — for a guardrail, blocking on an ambiguous judge is safer than letting unvetted content through. `errors.Is(err, guardrail.ErrJudgeBlocked)` matches either a `BLOCK` vote or a fail-closed parse.

The judge sees the whole message, not just its text: the content parts are forwarded verbatim (a vision-capable judge model can vet image parts too), and any tool calls are rendered into the prompt so content smuggled into tool arguments is vetted alongside the prose. A message with nothing to judge — no content parts, no tool calls — passes without a model call.

If the judge's provider call fails, the run still stops (the content stays unvetted), but the error matches `guardrail.ErrEvalFailed` instead of `ErrBlocked`: an outage is not a policy verdict, and metrics that count blocks can tell the two apart.

## Self-description for observability

Guards may implement the optional `Kinded` interface to say how they evaluate:

```go
type Kind uint8

const (
    KindDeterministic Kind = iota // pure code, no model call
    KindLLM                        // makes its own model call
)
```

`KindOf(g)` reports it (defaulting to `KindDeterministic`), and `BlockError.Kind` carries it, so traces can tag a block with `guardrail.kind=llm` and account for the extra call an LLM-backed guard costs. `LLMJudge` implements `Kinded`; the function adapters do not (they are deterministic by definition).

## Passing guards to an agent

The `pkg/agent` configs consume guards directly — pass the guards you want and the agent runs them with no further wiring:

```go
cfg := agent.Config{
    Provider: p,
    Model:    "claude-haiku-4-5",
    InputGuards:  []guardrail.InputGuard{noPII},
    OutputGuards: []guardrail.OutputGuard{noSecrets},
}
answer, err := agent.Run(ctx, cfg, "…")
```

- **ReAct** (`agent.Config`): input guards run on each user message **before it first reaches the model** — the seed conversation on the first turn, plus any user messages you append before re-invoking with a carried-over `State` (the `State.InputGuarded` watermark tracks what has been vetted, so nothing is skipped or re-judged). Output guards run on **every** assistant message before it is recorded or returned.
- **Plan-and-Execute** (`PlanExecuteConfig`): input guards run on `PlanExecuteState.Input` before the planner; output guards run on **every assistant message the executor produces** (before it lands in `Past` or feeds the replanner) and on the final answer before it is returned. The planner's and replanner's JSON emissions are not guarded.

Empty (or nil) guard slices are a no-op — the checks are skipped and there is no extra overhead.

## Gotchas

- **Guards block, they don't rewrite.** A veto is a hard failure (`errors.Is(err, ErrBlocked)`); there is no redaction or message rewriting yet.
- **First veto wins.** Guards run in configured order; the rest are skipped once one blocks.
- **Input guards fire once per user message.** Tool-result and assistant turns are not re-vetted as input; in multi-turn use, only the messages appended since the last invocation are checked.
- **An LLM-judge guard costs a model call per checked message.** In a ReAct tool loop that includes each tool-calling assistant turn. Use `Kind` / `Kinded` so traces make that visible, and prefer a cheap judge model.
- **Fail-closed parsing.** If the judge returns anything other than a clear `ALLOW` / `BLOCK`, the message is blocked (`ErrJudgeBlocked`).
- **Provider failures are not blocks.** A judge whose provider errors fails the run with `ErrEvalFailed`, which does **not** match `ErrBlocked` — don't count outages as policy blocks.

## See also

- [agent](agent.md) — the `Config` / `PlanExecuteConfig` fields that consume guards.
- [eval](eval.md) — `LLMJudge` there is the LLM-as-judge precedent this mirrors.
- [human-in-the-loop pattern](../patterns/human-in-the-loop.md) — a complementary, heavier gate for irreversible actions.
- Example: [`examples/guardrails`](../../examples/guardrails/) — deterministic + LLM-as-judge guards, offline.
