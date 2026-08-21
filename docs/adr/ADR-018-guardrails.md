# ADR-018 — Guardrails (`pkg/guardrail`)

- **Status:** Accepted
- **Date:** 2026-08-15
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

galdor shipped v1.0.0 with tools, a graph runtime, agent helpers,
observability and eval — but no first-class guardrail mechanism. The only
gate for "don't do this" was the human-in-the-loop pattern (graph
`InterruptBefore` + `Resume`), which pauses a run for a person rather than
applying a policy automatically. Integrators who needed PII redaction,
content moderation or prompt-injection rejection had to hand-roll checks
around `agent.Run` — and the shape of that glue differed in every codebase.

The question: should galdor model guardrails as a config surface (pass the
guards you want) or as provider middleware, and should the "how a guard
evaluates" be a runtime type tag?

## Decisions

### D1. `pkg/guardrail` with block-by-error contracts

Two small interfaces over a shared identity:

- `Guard` — `Name() string`.
- `InputGuard` — `CheckInput(ctx, msg) error` (runs before the model).
- `OutputGuard` — `CheckOutput(ctx, msg) error` (runs after the model).

A guard vetoes by returning a non-nil error; there is no redaction or
rewrite in this pass. Guards run in configured order; the first veto wins.
`CheckInput` / `CheckOutput` wrap the veto in a `*BlockError` carrying the
guard name, a `Kind`, and the guard's own error, and `errors.Is(err,
guardrail.ErrBlocked)` identifies a block regardless of which guard fired.

### D2. Flavors via constructor + optional self-description, not a tag

The architecture invariant "type-safe over stringly-typed" rules out a
`type: "deterministic" | "llm"` field — a runtime string buys nothing for a
`switch` and can lie. Instead:

- **Deterministic** guards are built with `InputGuardFunc` /
  `OutputGuardFunc` (plain functions).
- **LLM-as-judge** guards are built with `LLMJudge`, which makes its own
  model call to vote `ALLOW` / `BLOCK` and implements both `InputGuard` and
  `OutputGuard`. The judge is shown the message's content parts verbatim
  plus a rendering of its tool calls (content in tool arguments is vetted
  too), and skips the call when there is nothing to judge. Reply parsing is
  negation-aware ("Not allowed" never reads as an allow) and unparseable
  replies fail closed (`ErrJudgeBlocked`). A failed judge provider call
  fails the run with `ErrEvalFailed`, which deliberately does not match
  `ErrBlocked` — an outage is not a policy verdict.

The distinction is made at construction (compile time). For the cases that
do need to know — observability, cost accounting — a guard may implement
the optional `Kinded` interface (`Kind() Kind`) with a typed enum
(`KindDeterministic` / `KindLLM`); `KindOf` defaults to `KindDeterministic`
for guards that don't self-describe. This mirrors `Provider.Capabilities`
and the optional-interface pattern already used in `pkg/mcp`.

### D3. Consumed by `pkg/agent`, not provider middleware

`agent.Config` and `PlanExecuteConfig` gain `InputGuards` /
`OutputGuards` slices. ReAct runs input guards on each user message before
it first reaches the model — the seed conversation on the first turn, plus
any user messages appended between invocations of a carried-over `State`
(a `State.InputGuarded` watermark tracks what has been vetted, so nothing
is skipped or re-judged) — and output guards on every assistant message;
Plan-and-Execute runs input guards on the request before the planner and
output guards on every executor assistant turn and on the final answer
(the planner's and replanner's JSON emissions are not guarded). The agent
loop is the integration point
for this pass — a provider-level decorator would also fit but was left out
to keep the surface minimal (see Out of scope).

### D4. Zero cost when unused

Empty guard slices are a no-op: the loops skip nil guards and short-circuit.
Guardrails are strictly opt-in.

## Consequences

- Safety-sensitive policies become one config line: pass the guards you want
  and the agent runs them around each turn, uniformly and in order.
- The runtime treats every guard identically (polymorphic slice), matching
  the "provider details never leak" stance — the loop never branches on how
  a guard evaluates.
- LLM-as-judge guards cost an extra model call; `BlockError.Kind` gives
  observability a hook to surface that in traces (span tagging is a
  follow-up, not wired here).
- New `pkg/guardrail` is stdlib-only: no new dependencies in the core
  module.

## Out of scope

- **Rewrite / redact guards.** Guards only block; a modified-message pass
  would be a separate interface or an extension of these.
- **Provider-level guard middleware.** A decorator over `provider.Provider`
  is a natural follow-up and would reuse the same `InputGuard` / `OutputGuard`
  types.
- **A built-in guard catalog.** galdor ships the contract and the
  `LLMJudge` ergonomics, not a library of PII regexes or moderation rules.
- **Streaming.** Output guards run on the assembled assistant message in the
  `Generate` path; per-delta guarding is not implemented.

## References

- ADR-007 — the `pkg/agent` helpers whose `Config` surface this extends.
- ADR-012 — typed provider errors; `BlockError` mirrors the typed-wrapper /
  sentinel approach.
- `pkg/eval` `LLMJudge` — the in-tree LLM-as-judge precedent `LLMJudge`
  follows.
