# ADR-013 — CLI surface pruning: drop `serve`, `recast`, `forge`; sequence the rest

- **Status:** Accepted
- **Date:** 2026-06-11
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

Since Phase 0 the `galdor` binary has advertised eleven verbs. Two are
implemented (`scry`, `ui`); the other nine have printed "not yet
implemented" for ten releases. The pre-alpha audit (§5) flagged the gap,
and the post-audit verification asked the underlying question: which of
these verbs should *ever* exist?

The promise behind the unimplemented verbs splits into three groups:

1. **Engine exists, CLI wrapper missing** — `mcp`, `weave`, `trial`,
   `cast`, `council`. The Go packages behind them (`pkg/mcp`,
   `pkg/graph` Inspect/RenderSVG, `pkg/eval`, `pkg/agent`,
   `pkg/council`) are implemented and tested; what's missing is a thin
   CLI layer and, for the last three, a configuration format.
2. **Engine and verb both missing** — `spellbook` (the package is a
   documented stub with a planned surface).
3. **Verbs that contradict recorded positioning** — `serve`, `forge`,
   and `recast`, examined below.

## Decisions

### D1. Remove `serve` from the CLI surface

`pkg/serve` is an **explicit non-goal** in ROADMAP.md ("Ship the
example, not the abstraction"): galdor agents are Go values; embedding
one in *your* HTTP server is a ~20-line `net/http` handler that the
planned `examples/integration-http-interpret` will demonstrate. A
`galdor serve` verb is that non-goal smuggled back in through the
binary: it would need a routing story, a config story, auth, graceful
shutdown policy — an HTTP framework by installments, exactly what the
non-goal exists to prevent.

The ecosystem evidence is one-sided. LangServe — LangChain's "serve
your chain as an API" layer, the closest analogue — was **deprecated in
November 2024** in favor of a hosted platform; serving turned out to be
a product, not a framework feature. galdor should not re-walk that
path.

### D2. Remove `forge` (project scaffolding) and record scaffolding as a non-goal

This is the decision with the strongest architectural grounding, so it
gets the full argument.

**a) Scaffolding couples a generator to a moving API.** A template
freezes the framework's API at generation time. Every breaking change
then has *two* costs: the change itself, and re-validating every
template the generator emits. galdor is pre-v1.0 with breaking minors
every release — the worst possible point on that curve. The generator
would lag the framework exactly when the framework moves fastest, and a
`forge`-generated project that doesn't compile is *worse* than no
generator: it converts "I'll try galdor" into "galdor is broken" in the
first five minutes.

**b) The industry retired this pattern where it was strongest.**
- **create-react-app** — the canonical scaffolder, 2016–2025 — was
  formally deprecated by the React team in February 2025 after years
  without active maintainers; the stated reasons are precisely template
  rot and the maintenance burden of tracking an evolving ecosystem.
- **Buffalo** — the Go web framework built *around* Rails-style
  scaffolding (`buffalo new`) — was archived in February 2024. Its
  generator-first architecture meant the whole project carried the
  generator's maintenance cost, and the ecosystem moved to composing
  libraries instead.
- **LangChain's `langchain app new` + templates** — the agent-framework
  analogue — accumulated open issues of templates that no longer work
  as instructed, and the LangServe foundation they generate onto is
  deprecated.

Three different ecosystems, one lesson: generators are a liability
owned by the framework team, paid forever.

**c) Go already ships the idiomatic answer, and galdor already has the
content for it.** The Go team's own position on scaffolding is
`gonew` (golang.org/x/tools/cmd/gonew): an intentionally minimal,
*separate*, experimental tool that instantiates **an existing module as
a template** — no template language, no generator DSL, just "copy this
module, rewrite the module path". galdor's `examples/` directory is
thirteen runnable modules that compile in CI on every commit
("examples doubling as integration tests" — ARCHITECTURE.md). They are
*living templates*, compiler-verified against the current API by
construction — the exact property a `forge` template can never have.
The complete scaffolding story is therefore:

    gonew github.com/YasserCR/galdor/examples/agent-react your.module/app

zero new code, zero new maintenance, and it inherits the Go module
proxy + checksum security model for free.

**d) The cost/benefit is asymmetric.** What `forge` saves a user:
`mkdir`, `go mod init`, copying ~80 lines from an example — two
minutes, once per project. What it costs the project: N templates × M
releases of compatibility maintenance, forever, plus the opinionated
project-layout decisions a library framework has no business imposing
(the same reasoning that rejects `pkg/serve`).

**e) The adjacent need is real but different.** What new users actually
trip on is *environment*, not layout: `$GOBIN` not on PATH, missing
provider env vars, an unreachable trace store. That is `galdor doctor`
(ROADMAP Phase 14) — diagnosis has recurring value and does not rot
with the API, whereas generation has one-shot value and rots every
release. Keeping `doctor` and dropping `forge` is the same decision
applied consistently.

### D3. Remove `recast`; fold replay into `scry`

`recast` ("replay a run from a checkpoint") double-books a surface that
already exists: `galdor scry replay <run-id>` exports recorded fixtures
today, and *executing* a replay requires the agent's graph — which is
Go code, so re-running belongs in the user's test suite via
`replay.NewProvider` (the documented, shipped path). A standalone verb
would either duplicate `scry replay` or promise execution it cannot
deliver without the config format. If a config-driven runner ever makes
"re-run from checkpoint" expressible (post-`cast`), it returns as a
`cast` flag (`cast --resume-from <checkpoint>`), not a verb.

### D4. Keep and sequence the remaining six verbs

`mcp`, `weave`, `trial`, `cast`, `council`, `spellbook` stay advertised
as planned, in that order of feasibility:

- **`mcp`, `weave`** — engines complete; thin wrappers (v0.10.0).
- **`trial`, `cast`, `council`** — blocked on one shared design
  decision, the configuration format (ADR to come), with an honest
  constraint: config-driven agents can only bind **builtin tools and
  MCP-served tools**; custom Go tools remain a library-level feature.
- **`spellbook`** — package first, verb after; lowest urgency.

The usage text now separates "Commands" (implemented) from "Planned",
so the binary never advertises capability it doesn't have — the audit's
§5 complaint, fixed structurally rather than by wording.

## Consequences

- The CLI's promise/delivery gap closes from 9 phantom verbs to 6
  labeled-planned verbs with a sequenced path, and 3 deletions that
  each align the binary with an already-recorded position.
- `galdor serve|recast|forge` now print "unknown command". Nothing
  shipped ever depended on them (they exited 64 since Phase 0), so this
  is not a breaking change in any meaningful sense.
- Scaffolding is now an explicit non-goal (ROADMAP), with `gonew` +
  `examples/` documented as the supported path.
- The README/landing never advertised the pruned verbs, so no
  user-facing docs change beyond the usage text itself.

## Out of scope

- The configuration format for `cast`/`trial`/`council` — its own ADR.
- `galdor doctor` — stays a Phase 14 item; unaffected by this pruning.

## References

- ROADMAP.md — "Explicit Non-Goals": `pkg/serve`; Phase 14 `galdor doctor`.
- Pre-alpha audit §5 ("CLI announces stubs") and its follow-up
  verification.
- React team, "Sunsetting Create React App", Feb 2025 —
  https://react.dev/blog/2025/02/14/sunsetting-create-react-app
- gobuffalo/buffalo — archived Feb 2024 —
  https://github.com/gobuffalo/buffalo
- Go blog, "Experimenting with project templates" (gonew) —
  https://go.dev/blog/gonew
- langchain-ai/langserve — deprecated Nov 2024 —
  https://github.com/langchain-ai/langserve
