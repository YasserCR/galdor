# ADR-014 — Declarative config format (YAML) and the `cmd/galdor` module split

- **Status:** Accepted
- **Date:** 2026-06-11
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

ADR-013 kept `cast`, `trial` and `council` as planned CLI verbs, noting
they share one missing piece: a configuration format that lets a user
describe an agent/eval/orchestration in a file and run it without writing
Go. This ADR settles that format, and a module-structure consequence it
surfaced.

Each verb maps to a `Config` struct that already exists
(`agent.Config`, `eval.Config`, `council.SupervisorConfig`). Those structs
carry fields that are **code, not data**: a `provider.Provider` interface,
a `*tool.Registry`, an `eval.Subject`/`Scorer`, a `council.Worker.Run`
func. The format's job is to express those declaratively and have the CLI
construct them.

## Decisions

### D1. Format: YAML, parsed with `goccy/go-yaml`, decoded into typed structs under strict mode

**YAML, not JSON/TOML.**
- It is the convention of the domain (CrewAI's `agents.yaml`/`tasks.yaml`,
  Dify) and of Go CLIs/DevOps (kubectl, GitHub Actions, Compose,
  golangci-lint — including this repo's own `.golangci.yml`). Lower
  friction for the audience.
- The schema is deeply nested (`workers → agent → tools → mcp → [servers]`).
  For 4+ levels of nesting the consensus is YAML > JSON > TOML; TOML's
  arrays-of-tables get verbose and unobvious exactly at this shape.
- YAML is a JSON superset, so a `.json` file is still accepted — choosing
  YAML loses nothing.

**Library: `github.com/goccy/go-yaml`, not `gopkg.in/yaml.v3`.**
`gopkg.in/yaml.v3` was archived/unmaintained as of April 2025. `goccy/go-yaml`
is pure-Go (matches the "0 CGO, pure-Go core" identity), actively
maintained, MIT-licensed, and — decisively for a hand-edited CLI config —
produces `[line:col]` error positions with a source snippet. `go.yaml.in/yaml/v3`
(the YAML-org continuation) is the conservative fallback.

**Footguns neutralized by design.** YAML's implicit-typing traps (the
Norway problem, octal coercion) bite when decoding into `map[string]any`.
We decode into **typed structs** (the field type fixes interpretation) and
enable **strict mode** (`yaml.Strict()` → `DisallowUnknownField`), so a
typo like `modle:` fails with its position instead of being silently
dropped. A `version: 1` field is required and validated.

### D2. The shared `agent` block, and the tool boundary

Provider + model + system + sampling + tools form one **agent block**
reused across every verb (a trial subject, a council worker, a cast
agent). Resolution:
- **Provider** from `provider: <name>` via `providerset`, with the API key
  read from the environment (`<PROVIDER>_API_KEY`, then `LLM_API_KEY`, or an
  explicit `api_key_env`). The secret never lives in the file.
- **Tools** from builtins (guard-gated: `file_read` needs `base_dir`,
  `http_get` needs an allowlist) plus MCP servers (URL or subprocess,
  adopted via `Client.AsRegistry`).

**Boundary:** a config-driven agent binds **builtin tools and MCP-served
tools only**. Custom Go tools/scorers/workers stay a library feature — they
are functions, and a YAML cannot hold a function. This is the same boundary
every declarative agent framework hits; `galdor mcp serve` makes it porous
(wrap your Go logic as an MCP server and bind it back).

### D3. `cmd/galdor` becomes its own Go module

Resolving providers from a name requires `providerset`, which pulls all
four adapters and the AWS SDK (~15 modules). Importing that from the root
module would drag the whole provider stack into the dependency graph of
every library consumer of `pkg/...` — destroying the "6 direct + 13
indirect, dependency-light core" property that is a stated invariant and a
marketed differentiator.

So the binary is split into its own module, `github.com/YasserCR/galdor/cmd/galdor`:
- The root module (`pkg/`, `internal/`) stays provider-free and
  dependency-light — **unchanged at 6 direct**. Library consumers never
  pull `providerset`, the adapters, or the AWS SDK.
- The CLI module composes the library + `providerset` + `goccy/go-yaml`.
  Its dependency weight is irrelevant to library users (the linker dead-codes
  it; the module graph is separate).
- Still **one binary** — only the module boundary moved, not the artifact.
- `internal/store` / `internal/ui` remain importable: Go's `internal` rule
  is lexical on import path, and `…/cmd/galdor` is under the root tree.

**Consequence — `cmd/galdor` cannot use `replace` directives.** It is
installed via `go install github.com/YasserCR/galdor/cmd/galdor@version`,
and `go install pkg@version` errors on any `replace` in the target module
(verified). The library submodules use `require + replace => ../..`; the
CLI module instead relies on `go.work` for local resolution and plain
`require`s for release. Its `go.sum` therefore can only be sealed against
the sibling modules **after** they are tagged and pushed, making the CLI
module's tag the **last** step of a release (a structural two-phase, not a
hygiene patch — distinct from [[release-bump-pins-in-release-commit]]).

## Consequences

- `galdor trial <suite.yaml>` ships in v0.11.0; `cast` and `council` follow
  on the same agent block (v0.12.0).
- The repo goes from 10 to 11 modules; releases tag the CLI module last,
  after `go mod tidy` seals its `go.sum` against the freshly-published
  siblings.
- README dependency footprint stays "6 direct" for the core; the CLI
  module's deps are listed separately and never reach `pkg/` consumers.
- A new dependency (`goccy/go-yaml`, MIT, pure-Go) enters the CLI module
  only.

## Out of scope

- `cast` and `council` schemas — they reuse the agent block; their verbs
  land later. This ADR fixes the format and the module structure they build
  on.
- A JSON-Schema export of the config for editor autocompletion — possible
  later; the typed structs are the source of truth.

## References

- ADR-013 (CLI surface) — the verbs this format unblocks.
- `gopkg.in/yaml.v3` unmaintained — https://github.com/go-task/task/issues/2171
- `goccy/go-yaml` — https://github.com/goccy/go-yaml
- CrewAI YAML config — https://docs.crewai.com/en/concepts/agents
- Go modules: `go install pkg@version` and replace directives —
  https://go.dev/ref/mod#go-install
