# examples/spellbook

A versioned prompt registry — a directory of plain-text templates that diff
and review like code. `spells/` is the store; `galdor spellbook` manages it,
and an agent block can reference a spell as its system prompt.

```
spells/
  assistant/
    v1.md            # "You are a helpful assistant…"
    v2.md            # a revised persona
  summarize/
    v1.md            # a {{.N}}/{{.Input}} template
```

## Manage from the CLI

```bash
cd examples/spellbook

galdor spellbook list                          # every name + version
galdor spellbook show  assistant               # the latest (v2) template
galdor spellbook show  assistant v1            # a specific version
galdor spellbook diff  assistant v1 v2         # what changed between revisions
galdor spellbook render summarize --data '{"N":3,"Input":"…the text…"}'
```

`--dir` selects the store (default `$GALDOR_SPELLBOOK`, then `./spells`).

## Use a spell as an agent's system prompt

Reference a versioned spell from a `cast` / `council` / `trial` agent block
instead of inlining the prompt — so many agents share one reviewed prompt:

```yaml
version: 1
agent:
  provider: anthropic
  model: claude-haiku-4-5
  system_spell: {name: assistant, version: v2}   # from ./spells/assistant/v2.md
```

```bash
export ANTHROPIC_API_KEY=sk-...
galdor cast agent.yaml "What is the capital of Ecuador?"
```

Bump the persona for the whole fleet by editing `spells/assistant/v2.md` (or
adding `v3.md` and pointing the agents at it) — reviewed in a normal diff.
