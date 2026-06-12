# examples/trial-suite

A declarative evaluation suite run by `galdor trial` — no Go required.
`suite.yaml` maps to `pkg/eval`: a dataset of cases, a subject (an agent
block), and scorers, gated by a pass-rate threshold. This is the
config-driven counterpart of [`examples/eval-suite`](../eval-suite/) (which
wires the same thing in Go).

## Run

```bash
export ANTHROPIC_API_KEY=sk-...        # the suite's subject + judge use anthropic
galdor trial examples/trial-suite/suite.yaml
```

Exit code is a CI gate: `0` (pass rate ≥ `min_pass`), `1` (below it), `2`
(setup error — bad file, missing key, unknown field). Drop it straight into
a pipeline:

```yaml
- run: galdor trial suite.yaml   # fails the job when quality regresses
```

Add `--json` for machine-readable output.

## What it shows

- **The subject is an agent block.** `provider` + `model` (+ optional
  `system`, `tools`, sampling params) — the same shape `galdor cast` and
  `galdor council` reuse. The provider is built via `providerset`; the API
  key is read from `ANTHROPIC_API_KEY` (or `<PROVIDER>_API_KEY` /
  `LLM_API_KEY` / an explicit `api_key_env`), never from the file.
- **Scorers compose.** `contains` is lexical; `llm_judge` grades a rubric
  with a (typically stronger) model. A case passes only when every scorer
  passes.
- **Strict parsing.** A typo in a key fails with its `[line:col]` position
  instead of being silently ignored; `version: 1` is required.

## Tools (optional)

A config-driven subject can bind builtin tools and MCP-served tools — the
boundary is that custom Go tools stay a library feature:

```yaml
subject:
  provider: anthropic
  model: claude-haiku-4-5
  tools:
    builtins: [math, time]
    base_dir: ./docs            # enables file_read, confined here
    mcp:
      - url: http://127.0.0.1:4000
      - command: [galdor, mcp, serve]
```
