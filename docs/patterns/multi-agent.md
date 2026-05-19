# Multi-agent: Supervisor vs Swarm

## When to use this pattern

You have a task that naturally splits along expertise lines and a
single ReAct agent with one mega-registry is starting to feel like
a junk drawer. Two structured patterns ship in `pkg/council`:

- **Supervisor** — a routing LLM dispatches each turn to one of N
  specialist workers. Hierarchical, scripted, easy to audit.
- **Swarm** — peer agents collaborate over a shared message log;
  any agent can hand off to another via a synthetic
  `handoff_to_<name>` tool. Decentralized, conversational.

Pick **Supervisor** when the workflow is fan-out-then-return:
one decision per turn, the supervisor stays in charge, every
delegation is logged in `History`. Pick **Swarm** when agents
need to talk to each other directly and the conversation is the
state.

## Supervisor sketch

```go
billing := mustReAct(prov, buildBillingTools())
technical := mustReAct(prov, buildTechnicalTools())
general := mustReAct(prov, buildGeneralTools())

supervisor, _ := council.NewSupervisor(council.SupervisorConfig{
    Provider: prov,
    Model:    "claude-haiku-4-5",
    Workers: []council.Worker{
        {Name: "billing", Description: "invoices, refunds, charges",
            Run: workerRunner(billing)},
        {Name: "technical", Description: "bugs, outages, login issues",
            Run: workerRunner(technical)},
        {Name: "general", Description: "FAQs, hours, policies",
            Run: workerRunner(general)},
    },
    MaxHops: 4,
})

final, _ := supervisor.Invoke(ctx, council.SupervisorState{Input: userMessage})
```

Each worker is a plain `func(ctx, task string) (string, error)`.
That signature is the integration point: a worker can be a ReAct
sub-agent, a deterministic function, another `Supervisor`, or an
HTTP call to a colleague's service.

## Swarm sketch

```go
swarm, _ := council.NewSwarm(council.SwarmConfig{
    Start: "triage",
    Agents: []*council.SwarmAgent{
        {Name: "triage", Provider: p, Model: m, Tools: triageTools,
            Description: "first responder",
            Handoffs: []string{"billing", "technical"}},
        {Name: "billing", Provider: p, Model: m, Tools: billingTools,
            Description: "handles invoices and refunds",
            Handoffs: []string{"triage"}},
        {Name: "technical", Provider: p, Model: m, Tools: techTools,
            Description: "diagnoses bugs",
            Handoffs: []string{"triage"}},
    },
    MaxHops: 8,
})

final, _ := swarm.Invoke(ctx, council.SwarmState{
    Messages: []schema.Message{schema.UserMessage(userMessage)},
    Active:   "triage",
})
```

## Walkthrough — Supervisor

1. **Route.** Each turn the routing LLM receives the original
   request, the list of workers (name + description), and the
   history so far. It returns JSON: either
   `{"worker": "billing", "task": "..."}` or
   `{"final": "..."}`.
2. **Delegate.** The named worker's `Run` is called with the task
   string. The worker only sees the task, not the full history.
3. **Loop.** The worker's answer is appended to `History` and
   control returns to the supervisor. Repeat until a `final` is
   produced or `MaxHops` is hit.

## Walkthrough — Swarm

1. **Activate.** `cfg.Start` runs first with the user message in
   `SwarmState.Messages`.
2. **Inner loop.** Each agent runs its own bounded ReAct cycle
   over the shared message log: model → tools → model → ...
3. **Hand off.** The framework injects a `handoff_to_<name>` tool
   for every entry in `Agent.Handoffs`. If the LLM calls it, the
   runtime acknowledges the call, sets `Active` to the target, and
   the graph dispatches to that agent on the next hop.
4. **Terminate.** When an agent emits an assistant message with no
   tool calls, `Final` is set and the swarm returns.

## Splitting tool registries

The key design decision is which tools go where. Two rules:

- **Separate registries per agent** when the tools are
  capability-defining ("this agent has refund authority"). Putting
  `issue_refund` only in the billing registry means no other agent
  can mis-call it — that's an enforced boundary, not a hint.
- **Share a registry** when the tools are utilities that any agent
  legitimately needs (`now`, `math`, `web_search`). Build one
  `tool.Registry` and pass the same pointer to each agent.

You can mix the two: build a base registry of utilities, then
build per-agent registries that include the base set plus
domain-specific tools. `tool.NewRegistry(base.Tools()...,
specialist...)` flattens cleanly.

## Capability boundaries

- A Supervisor worker is **opaque** to the routing LLM: it only
  sees the worker's `Description` and the worker's reply. Use
  this to enforce "the routing brain never sees customer PII"
  by keeping that data inside the specialist.
- A Swarm agent **shares the whole message log** with every peer.
  Anything a previous agent said is visible to the next one.
  Pick Supervisor when you need stricter information hiding.
- A worker that errors halts the run. If you want soft failure
  ("this worker timed out; the supervisor should pick someone
  else"), catch the error inside `Worker.Run` and return a
  natural-language failure string.

## Common variations

- **Sub-supervisors.** A `Worker.Run` can be another Supervisor.
  Hierarchical orchestration drops out for free.
- **Custom routing prompt.** `SupervisorConfig.SystemPrompt`
  overrides the default. The replacement must still instruct the
  LLM to emit the same JSON shape — parsing is strict.
- **Streaming.** Both runnables implement `Stream`, so you can
  surface per-hop events to a UI while the supervisor / swarm is
  still running.
- **Checkpoints + interrupts** work the same way as any compiled
  graph. Combine with [human-in-the-loop](human-in-the-loop.md)
  to gate specific workers behind approval.

## Gotchas

- **The routing LLM's JSON output is strict.** Anthropic / OpenAI
  occasionally wrap JSON in code fences; the parser strips them,
  but a model that adds prose around the JSON breaks the run.
  Use a small, instruction-following model for the supervisor
  (Haiku, gpt-4o-mini) and keep `SystemPrompt` short.
- **`MaxHops` is a hard cap.** It prevents infinite ping-pong but
  also lops off legitimate long workflows. Default 8 — raise it
  before debugging "why did my workflow stop?"
- **Swarm handoffs are one-way per turn.** If an agent calls
  `handoff_to_billing` *and* a domain tool in the same turn, the
  domain call is dropped. Train your prompt against this.
- **Reserved worker names.** `START`, `END`, `supervisor` are
  off-limits. Names must match `[a-zA-Z0-9_-]+`.

## Links

- Runnable example: [examples/integration-support-bot](../../examples/integration-support-bot/)
- Concept: [council](../concepts/council.md)
- Concept: [agent](../concepts/agent.md)
- Concept: [tool](../concepts/tool.md)
