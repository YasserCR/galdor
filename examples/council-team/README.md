# examples/council-team

Run a multi-agent orchestration from a YAML file with `galdor council`.
`topology.yaml` declares a **supervisor** (a routing LLM that delegates to
named worker agents) — or a **swarm** (peers that hand off to each other).
Each worker is an agent block, the same shape `cast`/`trial` use.

## Run

```bash
export ANTHROPIC_API_KEY=sk-...
galdor council examples/council-team/topology.yaml \
  "What is 17 * 23, and what year did the Berlin Wall fall?"
```

The supervisor routes the math part to the `mathematician` worker and the
history part to the `historian`, then composes the final answer. Input can
also be piped on stdin.

## Two modes

- **`mode: supervisor`** (default) — a routing LLM (`supervisor:` block)
  picks a worker each turn and stops when it has a final answer. Maps to
  `council.NewSupervisor`.
- **`mode: swarm`** — peer agents hand off to each other via per-worker
  `handoffs:`; the conversation ends when an agent answers without handing
  off. Maps to `council.NewSwarm`. See the commented block in
  `topology.yaml`.

## What it shows

- **Each worker is an agent block** — provider + model (+ tools, system),
  resolved exactly like `galdor cast`. The provider comes from
  `providerset` with the key read from the environment.
- **The boundary**: workers bind builtin + MCP tools; custom Go
  tools/workers stay a library feature. Wrap your own logic as an MCP
  server (`galdor mcp serve`) to bind it back.
