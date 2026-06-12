# examples/cast-agent

Run a ReAct agent from a YAML file — no Go required — with `galdor cast`.
`agent.yaml` is an *agent block* (provider + model + optional system/tools),
the same shape `galdor trial` and `galdor council` reuse.

## Run

```bash
export ANTHROPIC_API_KEY=sk-...
galdor cast examples/cast-agent/agent.yaml "What is 19 * 23, and what time is it in UTC?"

# input can also come from stdin:
echo "What is the capital of Ecuador?" | galdor cast examples/cast-agent/agent.yaml
```

## Trace it into your dashboard

`--trace` records the run (provider + tool + node spans) to the span store,
so it shows up in `galdor scry` / `ui` / `weave`:

```bash
galdor cast examples/cast-agent/agent.yaml "What is 6*7?" --trace --db ./traces.db
galdor scry show <run-id> --db ./traces.db    # the run id is printed on stderr
galdor weave    <run-id> --db ./traces.db -o graph.svg
galdor ui       --db ./traces.db
```

Flags work wherever you put them (before or after the input).

## What it shows

- **The agent block** resolves the provider via `providerset` (key from the
  environment, never the file) and builds the tool registry from builtins +
  MCP servers.
- **`--trace`** wires `observability.InstrumentProvider` + a SQLite exporter
  and runs through the graph with `TraceHooks`, so the whole run — LLM calls,
  tool calls, node hops — lands in the same store the dashboard reads.
- **One binary** ties it together: define in YAML, run, trace, inspect.
