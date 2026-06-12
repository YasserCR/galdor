# Agent

`pkg/agent` ships high-level agent loops built on top of `pkg/graph`, `pkg/provider` and `pkg/tool`. There are two patterns today: **ReAct** (the canonical `model → tools → model` loop) and **Plan-and-Execute** (a planner LLM emits a multi-step plan, an executor sub-agent runs each step, a replanner decides whether to continue, revise, or finish). Both compile to ordinary `graph.Runnable[S]` values — everything from [Graph](graph.md) applies: streaming, checkpointing, interrupts, hooks.

## The shape

```go
type Config struct {
    Provider provider.Provider   // required
    Tools    *tool.Registry      // optional
    Model    string              // required

    MaxIterations int             // default 10
    Temperature   *float64
    TopP          *float64
    MaxTokens     *int
    StopSequences []string

    ForceToolUse  bool            // sets ToolChoiceRequired on first turn
}

type State struct {
    Messages   []schema.Message
    FinalText  string
    Iterations int
}

func NewReAct(cfg Config) (*graph.Runnable[State], error)
func Run(ctx context.Context, cfg Config, input string, system ...string) (string, error)
```

For Plan-and-Execute:

```go
type PlanExecuteConfig struct {
    Provider provider.Provider
    Model    string
    Tools    *tool.Registry

    // Per-role overrides (planner / executor / replanner).
    PlannerProvider, ExecutorProvider, ReplannerProvider provider.Provider
    PlannerModel, ExecutorModel, ReplannerModel          string

    MaxIterations     int   // default 8 plan/exec/replan cycles
    MaxStepIterations int   // default 6 inner ReAct iterations per step

    PlannerPrompt   string  // override the default JSON-emitting prompt
    ReplannerPrompt string
}

type PlanExecuteState struct {
    Input string
    Plan  []string
    Past  []StepRecord
    Final string
    Iter  int
}

func NewPlanAndExecute(cfg PlanExecuteConfig) (*graph.Runnable[PlanExecuteState], error)
func RunPlanAndExecute(ctx context.Context, cfg PlanExecuteConfig, input string) (string, error)
```

## Things you do with it

### 1. One-shot ReAct

`Run` is the convenience wrapper. It builds an initial `State`, invokes a fresh `Runnable`, and returns the final text.

```go
import (
    "github.com/YasserCR/galdor/pkg/agent"
    anthropic "github.com/YasserCR/galdor/providers/anthropic"
)

p, _ := anthropic.New(anthropic.Config{APIKey: os.Getenv("ANTHROPIC_API_KEY")})

answer, err := agent.Run(ctx, agent.Config{
    Provider: p,
    Model:    "claude-haiku-4-5",
}, "What is the capital of Ecuador?", "Be brief.")
```

The last argument is a variadic list of optional system prompts; empty strings are skipped.

### 2. ReAct with tools

Attach a `*tool.Registry`; the agent will pass tool definitions on every model turn and dispatch tool calls automatically.

```go
reg, _ := tool.NewRegistry(weatherTool, builtins.MustNewMathTool())

answer, _ := agent.Run(ctx, agent.Config{
    Provider:      p,
    Tools:         reg,
    Model:         "claude-haiku-4-5",
    MaxIterations: 6,
}, "How's the weather in Quito in Fahrenheit?")
```

`MaxIterations` bounds the number of model↔tools cycles; the loop terminates with the current state if the cap is hit. Default 10. The full runnable example: [`examples/agent-react`](../../examples/agent-react/).

### 3. Build the Runnable yourself

For multi-turn chat, streaming, checkpointing, or interrupts, build the `Runnable` directly:

```go
r, err := agent.NewReAct(agent.Config{
    Provider: p,
    Tools:    reg,
    Model:    "claude-haiku-4-5",
})

initial := agent.State{
    Messages: []schema.Message{
        schema.SystemMessage("You may use tools when helpful."),
        schema.UserMessage("What's the weather in Quito?"),
    },
}
final, err := r.Invoke(ctx, initial)
```

Because `r` is a regular `graph.Runnable[agent.State]`, you can also call `r.Stream`, `r.InvokeWith` with a `Checkpointer`, or `r.Resume` after an `InterruptBefore` (although the ReAct graph itself does not install one — add yours by composing your own graph on top, or use Plan-and-Execute which already has a richer pause story).

### 4. Force tool use

`ForceToolUse: true` sets `provider.ToolChoiceRequired` on the first turn, so the model must call at least one tool before being allowed to produce text. Useful for "always answer through tools" agents.

```go
agent.Config{
    Provider:     p,
    Tools:        reg,
    Model:        "claude-haiku-4-5",
    ForceToolUse: true,
}
```

`Config.validate()` rejects `ForceToolUse: true` without a `Tools` registry — the constraint is meaningless otherwise.

### 5. Plan-and-Execute for multi-step work

When a task needs upfront decomposition (research → analyze → write), `NewPlanAndExecute` compiles a `plan → execute → replan` loop. Each step runs in an inner ReAct sub-agent with the configured tools; the replanner decides after every step whether to continue with the remaining plan, revise it, or finish.

```go
final, err := agent.RunPlanAndExecute(ctx, agent.PlanExecuteConfig{
    Provider: p,
    Model:    "claude-haiku-4-5",
    Tools:    reg,
}, "Research the population of Quito and tell me what fraction of Ecuador's total it represents.")
```

The planner emits JSON, parsed with `parsePlan`; the replanner emits `{"plan": [...], "final": ""}`, parsed with `parseReplan`. Both parsers tolerate leading prose and code fences, but if you override `PlannerPrompt` / `ReplannerPrompt`, keep the JSON contract.

Different roles can use different models — typically a stronger planner and a cheaper executor:

```go
agent.PlanExecuteConfig{
    Provider:        cheap,
    Model:           "claude-haiku-4-5",
    PlannerProvider: strong,
    PlannerModel:    "claude-sonnet-4-5",
    Tools:           reg,
}
```

## From the CLI (`galdor cast`)

You can run a ReAct agent without writing Go: `galdor cast <agent.yaml> "<input>"` maps a YAML *agent block* to `agent.Config` and runs it.

```yaml
version: 1
agent:
  provider: anthropic
  model: claude-haiku-4-5
  system: "Use tools when helpful."
  tools:
    builtins: [math, time]
    mcp:
      - command: [galdor, mcp, serve]   # adopt an MCP server's tools
```

The provider resolves via `providerset` (API key from the environment, never the file); tools come from builtins + MCP servers; custom Go tools stay a library feature. Input can be a positional argument or piped on stdin.

`--trace [--db PATH] [--run-id ID]` records the run — provider, tool and node spans — to the span store, so it shows up in `galdor scry` / `ui` / `weave`:

```bash
galdor cast agent.yaml "What is 6*7?" --trace --db ./traces.db
```

See [`examples/cast-agent`](../../examples/cast-agent/) for a complete file.

## Gotchas

- **Capability-aware validation runs at construction.** If you set `Tools` against a provider whose `Capabilities().ToolCalling` is false, `NewReAct` returns an error immediately — you don't have to wait for the first wire call to fail. Same for `ForceToolUse` without `Tools`.
- **`MaxSteps` is set for you.** The compiled ReAct runnable sets `MaxSteps = MaxIterations*3 + 4` so the graph's hard step ceiling doesn't fire before the soft iteration cap. Override `Runnable.MaxSteps` if you have an unusual topology.
- **`Run` swallows the partial state.** When the underlying `Invoke` returns an error, `Run` returns the last `FinalText` (often empty) and the error. If you need the full conversation on failure, drive the `Runnable` directly and read `state.Messages`.
- **Plan parsing is permissive but not magic.** The default prompts ask for strict JSON; the parsers strip ```json fences and leading prose. If you override the prompts and the LLM emits free text, parsing fails with `agent: planner output: ...`. Keep the JSON contract or replace both prompts together.
- **Inner ReAct iteration caps apply per step.** `MaxStepIterations` (default 6) bounds the inner loop, `MaxIterations` (default 8) bounds the outer plan/execute/replan cycles. Total LLM calls can reach ~`MaxIterations * (1 + MaxStepIterations)`.

## See also

- [Graph](graph.md) — everything you can do to a `*Runnable[S]` works here too.
- [Provider](provider.md) — the `Provider` your agent talks to.
- [Tool](tool.md) — building and registering tools.
- [Multi-agent pattern](../patterns/multi-agent.md) — when one ReAct is not enough.
- [Human-in-the-loop pattern](../patterns/human-in-the-loop.md) — pausing an agent for review.
- Examples: [`agent-react`](../../examples/agent-react/), [`tools-loop`](../../examples/tools-loop/), [`integration-support-bot`](../../examples/integration-support-bot/).
