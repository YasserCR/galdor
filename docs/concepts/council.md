# council

`pkg/council` is galdor's multi-agent layer. Two patterns ship: **Supervisor**, where a routing LLM decides which specialist worker handles each turn, and **Swarm**, where peer agents collaborate over a shared conversation and transfer control via synthetic handoff tools. Both compile down to a regular `graph.Runnable[State]` — checkpointing, interrupts, observability, and streaming work the same way they do for any compiled graph. The package theme name is deliberate; the constructors (`NewSupervisor`, `NewSwarm`) are not.

## Supervisor

A routing LLM picks one of the configured workers each turn, sees the worker's answer the next turn, and finishes when the user's request is satisfied. Workers are plain Go functions — wrap a ReAct runnable, a deterministic function, a sub-supervisor, or an HTTP call behind the same signature.

### Core types

```go
type Worker struct {
    Name        string
    Description string
    Run         func(ctx context.Context, task string) (string, error)
}

type SupervisorConfig struct {
    Provider     provider.Provider
    Model        string
    Workers      []Worker
    MaxHops      int    // default 8
    SystemPrompt string // optional override; must keep the strict JSON contract
}

type SupervisorState struct {
    Input    string
    History  []WorkerInvocation
    Final    string
    Hops     int
    Next     string // internal — selected worker
    NextTask string // internal — task to delegate
}
```

The routing LLM emits one of two JSON shapes per turn — `{"worker": "...", "task": "..."}` or `{"final": "..."}`. The framework parses, tolerates fenced code blocks and surrounding prose, and rejects unknown worker names.

### Usage

```go
import "github.com/YasserCR/galdor/pkg/council"

billing  := makeReActWorker(provider, "claude-haiku-4-5", billingTools)
technical := makeReActWorker(provider, "claude-haiku-4-5", techTools)

r, _ := council.NewSupervisor(council.SupervisorConfig{
    Provider: p, Model: "claude-haiku-4-5",
    Workers: []council.Worker{
        {Name: "billing",   Description: "handles invoices, refunds",  Run: billing},
        {Name: "technical", Description: "diagnoses bugs, outages",    Run: technical},
    },
})

final, _ := r.Invoke(ctx, council.SupervisorState{Input: userMessage})
fmt.Println(final.Final)
```

One-shot convenience:

```go
answer, _ := council.RunSupervisor(ctx, cfg, "my invoice is wrong")
```

### Graph shape

```
START -> supervisor -> (worker_1 | worker_2 | ... | END)
worker_n -> supervisor
```

`MaxHops` caps supervisor consultations; `MaxSteps` on the returned `Runnable` is set generously (`maxHops*3 + 4`) so the inner edges don't trip the graph runtime's safety check.

## Swarm

Peer agents share one conversation. Each agent has its own provider/model/tools, plus a list of peers it may hand off to. Handoffs surface as synthetic `handoff_to_<name>` tools that the framework intercepts. The receiving agent sees the shared history verbatim.

### Core types

```go
type SwarmAgent struct {
    Name          string
    Description   string
    Provider      provider.Provider
    Model         string
    Tools         *tool.Registry // optional
    Handoffs      []string       // names this agent may hand off to
    SystemPrompt  string         // optional
    MaxIterations int            // inner ReAct loop cap; default 6
}

type SwarmConfig struct {
    Agents  []*SwarmAgent
    Start   string // entry agent
    MaxHops int    // handoff cap; default 8
}

type SwarmState struct {
    Messages []schema.Message
    Active   string
    Hops     int
    Final    string
}
```

### Usage

```go
r, _ := council.NewSwarm(council.SwarmConfig{
    Agents: []*council.SwarmAgent{
        {
            Name: "triage", Description: "routes incoming questions",
            Provider: p, Model: "claude-haiku-4-5",
            Handoffs: []string{"researcher", "writer"},
        },
        {
            Name: "researcher", Description: "looks up facts using web search",
            Provider: p, Model: "claude-haiku-4-5",
            Tools:    searchReg,
            Handoffs: []string{"writer"},
        },
        {
            Name: "writer", Description: "drafts the final answer",
            Provider: p, Model: "claude-haiku-4-5",
        },
    },
    Start: "triage",
})

final, _ := r.Invoke(ctx, council.SwarmState{
    Messages: []schema.Message{schema.UserMessage("explain pgvector indexes")},
    Active:   "triage",
})
```

Or the one-shot: `council.RunSwarm(ctx, cfg, "explain pgvector indexes")`.

### Inner loop

Each agent activation runs a bounded model → tools → model cycle. It terminates when:

- the model emits a non-tool-call assistant message → swarm finishes (`s.Final = text`); or
- the model calls a `handoff_to_<name>` tool → control transfers; sibling tool calls in the same turn are acknowledged but skipped (the receiving agent decides what to do next).

`MaxIterations` per agent and `MaxHops` per swarm both bound runaway loops.

## From the CLI (`galdor council`)

`galdor council <topology.yaml> "<input>"` runs a Supervisor (default) or a Swarm from a YAML file, with each worker declared as an agent block.

```yaml
version: 1
mode: supervisor                  # or: swarm
supervisor: {provider: anthropic, model: claude-sonnet-4-6}   # routing LLM
max_hops: 6
workers:
  - name: mathematician
    description: "Solves arithmetic."
    agent: {provider: anthropic, model: claude-haiku-4-5, tools: {builtins: [math]}}
  - name: historian
    description: "Answers history questions."
    agent: {provider: anthropic, model: claude-haiku-4-5}
```

For `mode: swarm`, drop the `supervisor:` block, set `start:` to the first agent, and give each worker a `handoffs:` list of peers it may transfer to. Every worker's provider/tools resolve exactly like `galdor cast`; the API key is read from the environment. See [`examples/council-team`](../../examples/council-team/) and [ADR-014](../adr/ADR-014-config-format-and-cli-module.md).

## Gotchas

- Worker / agent names must match `[a-zA-Z0-9_-]+`. `START`, `END`, and `supervisor` are reserved.
- Supervisor `SystemPrompt` overrides are accepted, but the routing LLM must still emit the strict JSON contract — otherwise parsing fails and the supervisor errors out for that turn.
- Swarm system prompts are augmented automatically with the list of available handoff targets. If you supply your own, the framework still appends the handoff description block.
- A swarm agent that's handed control loses access to other agents' system prompts (only its own appears in the messages sent to its provider). Earlier system messages are filtered out at the activation boundary.
- An agent that runs out of `MaxIterations` returns an error from its node; `SwarmState.Final` stays empty so the caller can inspect `Messages` to see what happened.
- Self-handoff (`Handoffs: ["self"]`) is rejected at compile time.

## See also

- [agent](agent.md) — the ReAct helper most workers wrap.
- [graph](graph.md) — both patterns compile to a `graph.Runnable`; `RunOptions` (checkpoints, hooks, timeouts) work as usual.
- [a2a](a2a.md) — for cross-process multi-agent (council is in-process).
- [`examples/integration-support-bot`](../../examples/integration-support-bot/) — Supervisor with three ReAct sub-agents.
