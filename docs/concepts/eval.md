# eval

`pkg/eval` is galdor's inline regression framework for prompts and agents. You declare a `Dataset` (a list of `Case`s), a `Subject` (a function representing the system under test), and one or more `Scorer`s. The runner evaluates every case in parallel and produces a `Report` with pass/fail counters and per-scorer aggregates. Designed to drop into `go test`, CI, or a one-off script — `RunAndExit` exits non-zero when the pass rate falls below a threshold, which is the only API CI needs.

The framework is provider-agnostic: `Subject` is just `func(ctx, input) (string, error)`, so it can wrap a ReAct agent, a Supervisor, a Plan-and-Execute pipeline, or anything else that takes a string in and produces a string out.

## Core types

```go
type Case struct {
    ID       string
    Input    string
    Expected string
    Metadata map[string]string
}

type Dataset struct {
    Name    string
    Version string
    Cases   []Case
}

type Subject func(ctx context.Context, input string) (string, error)

type Score struct {
    Value       float64
    Pass        bool
    Explanation string
}

type Scorer interface {
    Name() string
    Score(ctx context.Context, c Case, actual string) (Score, error)
}

type Config struct {
    Dataset        Dataset
    Subject        Subject
    Scorers        []Scorer
    Parallel       int           // default 4
    MinPass        *float64      // nil = default 1.0; eval.Threshold(0.9) to set
    TimeoutPerCase time.Duration
}
```

`Score.Value` is normalized to `[0, 1]`. A case "passes" only when every scorer's `Pass` is true. Errored cases (Subject returned an error) count as failures in `Report.PassRate` but are tallied separately as `Errored`.

## Built-in scorers

```go
eval.ExactMatch{}                       // strings.TrimSpace equality (case-insensitive by default)
eval.ExactMatch{CaseSensitive: true}

eval.Contains{}                         // expected substring of actual (case-insensitive)
eval.Contains{CaseSensitive: true}

&eval.Regex{Pattern: `^\d+\.\d{2}$`}    // pointer; compiles lazily on first Score()

eval.ScorerFunc("my_check", func(ctx context.Context, c eval.Case, actual string) (eval.Score, error) {
    return eval.Score{Value: 1, Pass: true}, nil
})

eval.LLMJudge{
    Provider:      strongerModel,
    Model:         "claude-opus-4-5",
    Rubric:        "Is the answer factually correct AND concise?",
    PassThreshold: 0.7,
    MaxTokens:     32,
}
```

`LLMJudge` asks a second LLM to rate the answer on a 0..100 scale, rescaled to `[0, 1]`. Pick a stronger (or independently-trained) model for the judge than the subject. `NameOverride` lets you run multiple judges with different rubrics in one report — set it to e.g. `"judge_correctness"` and `"judge_style"` so the aggregates stay distinguishable.

## Running

```go
import (
    "context"
    "github.com/YasserCR/galdor/pkg/eval"
    "github.com/YasserCR/galdor/pkg/agent"
)

func main() {
    ctx := context.Background()

    dataset := eval.MustLoadDataset("./datasets/qa.json")

    cfg := eval.Config{
        Dataset: dataset,
        Subject: func(ctx context.Context, input string) (string, error) {
            return agent.Run(ctx, agentCfg, input)
        },
        Scorers: []eval.Scorer{
            eval.Contains{},
            eval.LLMJudge{Provider: judge, Model: "claude-opus-4-5",
                Rubric: "Answer is factually correct."},
        },
        Parallel:       4,
        MinPass:        eval.Threshold(0.9),
        TimeoutPerCase: 30 * time.Second,
    }

    eval.RunAndExit(ctx, cfg)
}
```

`RunAndExit` is the CI shape — prints a summary to stderr, exits 0 when `report.Meets(MinPass)`, exits 1 when below threshold, exits 2 on setup error. For library use, call `eval.Run(ctx, cfg)` directly and inspect the `*Report`:

```go
report, err := eval.Run(ctx, cfg)
if err != nil { /* setup error */ }
report.PrintSummary(os.Stdout)
if !report.Meets(0.9) {
    os.Exit(1)
}
```

## Dataset files

The JSON shape is the `Dataset` struct verbatim, so hand-edited files and Go-marshalled values round-trip:

```json
{
  "name": "south-american-capitals",
  "version": "2025-05-01",
  "cases": [
    {"id": "ec", "input": "What is the capital of Ecuador?", "expected": "Quito"},
    {"id": "co", "input": "What is the capital of Colombia?", "expected": "Bogotá"}
  ]
}
```

`LoadDataset` validates non-empty `Name`, non-empty `Version`, non-empty `Cases`, and unique `Case.ID`s. `SaveDataset` writes the same shape — useful when generating cases programmatically (e.g., sampled from the trace store).

## Parallel runner

The runner uses a worker pool of size `Parallel` (default 4). Each worker pulls a case index off a channel and writes its result back into a pre-sized slice at the same position, so ordering is preserved without extra synchronization. Cases run concurrently; scorers run sequentially within a case (LLM-judge calls amortize at the case level). `Report.Cases` is sorted by `Case.ID` at the end so report diffs across runs are stable regardless of which worker finished when.

## Report

```go
type Report struct {
    Dataset    string
    Version    string
    StartedAt  time.Time
    Duration   time.Duration
    Cases      []CaseResult
    Aggregates map[string]Aggregate
    Passed, Failed, Errored int
}

func (r *Report) PassRate() float64           // Passed / len(Cases)
func (r *Report) Meets(minPass float64) bool  // PassRate >= minPass
func (r *Report) PrintSummary(w io.Writer)
func (r *Report) WriteJSON(w io.Writer) error
```

Per-scorer `Aggregate` carries `Mean`, `Pass`, `Fail`. The `Version` field is stamped from `Dataset.Version` so a regression can be attributed to "model changed" vs. "dataset changed".

## Gotchas

- A scorer that returns an error degrades to `{Value: 0, Pass: false}` with the error in `Explanation`. The case still produces a result; the report stays well-formed.
- `Regex` is a pointer (`*Regex`) because it memoizes the compiled pattern. The value form won't compile against `Scorer`.
- `LLMJudge.PassThreshold` defaults to 0.7. `MaxTokens` defaults to 32 — enough for a number plus a short rationale. Bump it if your rubric prompts the judge to explain itself first.
- `RunAndExit` treats a nil `MinPass` as 1.0 (strictest). Set it with `eval.Threshold(0.9)` for realistic agent eval, or `eval.Threshold(0)` for report-only (never fails on pass rate). Values outside [0,1] are a setup error.
- `TimeoutPerCase` counts a hit as an *error*, not a *fail*, so it can be diagnosed separately from a wrong answer.
- The runner doesn't recover panics inside `Subject`. Wrap your subject if your agent can panic; `go test`'s harness recovers at the test boundary.

## See also

- [provider](provider.md) — what `Subject` typically wraps.
- [replay](replay.md) — pair with eval to run regression suites offline against recorded fixtures.
- [observability](observability.md) — instrument the subject so eval failures come with span traces.
- [`examples/eval-suite`](../../examples/eval-suite/) — full Config + scorers + RunAndExit.
