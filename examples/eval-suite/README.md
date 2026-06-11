# examples/eval-suite

Gating prompt and agent changes from CI with `pkg/eval`. A
deterministic Subject (a stand-in for a ReAct agent) is evaluated
against a small dataset using two scorers — `Contains` and an
`LLMJudge` backed by a scripted provider — so the example runs
offline with no API key.

## Run

```bash
go run ./examples/eval-suite
```

Expected output:

```
Dataset:  geography-and-math @ 1
Cases:    3  (pass 2, fail 1, error 0)
Pass rate: 66.7%
Duration: 294µs

Per-scorer aggregates:
  contains           mean=0.67  pass=2  fail=1
  llm_judge          mean=0.85  pass=3  fail=0

Cases needing attention:
  [FAIL] math-1                   actual="5"  failed: contains(0.00)


66.7% < 80.0% threshold — would exit 1 in CI mode
```

The `math-1` case answers "5" to "2 plus 2" on purpose, so the
suite exercises a real failure and lands below the 80% threshold.

## What it shows

- **`eval.Config` ties a `Dataset` to a `Subject` and a set of
  `Scorer`s.** The Subject is any `func(ctx, input) (string, error)`
  — wrap `agent.Run`, `council.RunSupervisor`, or whatever you ship.
- **Scorers compose.** `Contains` is a cheap lexical check;
  `LLMJudge` asks a model to grade against a rubric. Each scorer
  reports its own pass/fail aggregate.
- **`eval.Threshold(0.8)`** sets `MinPass`; `report.Meets(*MinPass)`
  is the CI gate. In CI you'd call `eval.RunAndExit(ctx, cfg)`, which
  prints the summary and exits 1 when the pass rate is below the
  threshold.
- **`Parallel: 3`** runs cases concurrently.

## Run against a real provider

Replace the scripted bits:

- **Subject** — call your agent, e.g. `agent.Run(ctx, cfg, input)`
  over Anthropic/OpenAI/Google/Bedrock.
- **`LLMJudge.Provider`** — use a stronger model than the Subject
  (e.g. Opus or GPT-4o) so the judge is more capable than the system
  under test.

The dataset, scorers, threshold and CI wiring stay identical.
