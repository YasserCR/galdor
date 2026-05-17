# ADR-001 — Foundational decisions

- **Status:** Accepted
- **Date:** 2026-05-17
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

galdor is a new Go-native framework for building, orchestrating and observing AI agents. Before writing runtime code, a set of foundational decisions must be locked in so that future ADRs can build on a stable base. This ADR captures the non-controversial choices that come out of `docs/PLAN.md` and turns them into a binding reference.

## Decisions

### D1. Project name and brand

The project is named **galdor** (Old English, c. 9th century: *incantation*). The domain `galdor.org` is reserved. The Go module path is `github.com/YasserCR/galdor`. The themed lexicon (Spellbook, Council, Scry, Recast, Trial, Cast, Weave, Forge, ...) is reserved to:

- CLI verbs
- Documentation and marketing
- A small set of high-level helper packages (`pkg/spellbook`, `pkg/council`) where the themed name *teaches* better than the technical alternative.

Low-level public APIs in `pkg/` use technical, idiomatic Go names — never themed ones. The reading test: code importing galdor must look professional without forced theming.

### D2. Minimum Go version

galdor targets **Go 1.22+** as the floor. Rationale: mature generics, `range over int`, modern `loopvar` semantics. CI builds against the floor and the latest stable Go on Linux, macOS and Windows.

### D3. License — Apache 2.0

The project is licensed under **Apache License 2.0**. Reasons:

- Permissive but with explicit patent grant (stronger than MIT for an ecosystem with potential corporate adoption).
- Accepted by enterprise legal review without friction.
- Compatible with the broader Go ecosystem.

A first-class commitment in `README.md` declares that galdor will never gate features behind a paid edition.

### D4. Contributor agreement — DCO, not CLA

Contributors sign off commits per the **Developer Certificate of Origin 1.1**. Enforced via a self-hosted GitHub Action (`.github/workflows/dco.yml`) that verifies every commit in a PR carries a matching `Signed-off-by` trailer. Rationale:

- DCO certifies origin without transferring copyright — minimal legal friction for early contributors.
- Used by Linux Kernel, Docker, GitLab and most major OSS projects.
- A future migration to a CLA (e.g. for dual licensing) is supported by `cla-assistant` if ever needed.

### D5. Copyright held personally

Copyright in `NOTICE` is attributed to the maintainer's personal name, not a company. This preserves flexibility for future decisions (transfer to a foundation, dual licensing) without binding contributors. Contributors retain copyright on their own contributions.

### D6. Observability is core, not optional

**OpenTelemetry instrumentation is a core dependency** of the framework, not an opt-in adapter. Rationale:

- OTel is the lingua franca of distributed telemetry in 2026; it is no longer a debate.
- Native, embedded observability is the project's main differentiator vs. Eino, Genkit Go and LangChainGo.

This is intentionally a one-way door: removing OTel later would require a major version bump and a strong reason.

### D7. Embedded storage — SQLite via `modernc.org/sqlite`

The default embedded backend for traces, checkpoints and the prompt registry is **SQLite** through the pure-Go driver `modernc.org/sqlite` (no CGO). Postgres or ClickHouse may be plugged in for scale. BoltDB and BadgerDB are rejected as defaults because SQL queries are needed for the trace UI.

The dependency lands in `internal/store/`, not `pkg/`, so the core public API stays clean.

### D8. Web UI — HTMX + Go templates + Alpine.js

The observability UI is built with **HTMX, Go `html/template` and Alpine.js for interactivity**, served from the same binary via `embed.FS`. No TypeScript build toolchain at v1.0. This is revisited if the UI grows beyond what HTMX can comfortably serve.

### D9. Provider adapters live in independent Go modules

Each provider (Anthropic, OpenAI, Google, Bedrock, Azure, Ollama, vLLM, ...) lives under `providers/<name>/` with its **own `go.mod`**. Same for memory backends under `memory/<backend>/`. Rationale:

- The galdor core stays dependency-light (the lesson from LangChainGo's 170+ deps).
- Users only pull the adapters they actually use.
- Each adapter versions independently from the core.

### D10. Cross-platform CI from day one

CI runs on **Linux, macOS and Windows** against the Go floor (1.22) and the latest stable. No CGO is permitted in any module, ensuring straightforward Windows support and easy cross-compilation.

### D11. No telemetry from the framework itself

galdor **does not phone home**. No anonymous usage metrics, no opt-in pings, nothing. This is treated as a feature, not a TODO. Prior art: `esbuild` proves a serious tool can grow without telemetry.

### D12. SemVer with explicit pre-v1.0 instability

Public APIs in `pkg/` are explicitly **unstable until v1.0**. The `README` and module top-level GoDoc say so. After v1.0, strict SemVer applies: no breaking changes outside major version bumps.

### D13. Governance — informal BDFL with a documented exit

The maintainer (BDFL) holds final say through Phase ~3. `GOVERNANCE.md` declares the explicit intent to transition to a multi-maintainer model once three contributors with sustained activity exist. Decision-making moves to lazy consensus / RFC at that point.

### D14. No `panic` outside `init`

Library code returns errors as values. `panic` and `Must*` helpers are forbidden in hot paths and exported APIs (they are tolerated only in `init` and in test helpers that explicitly say so).

### D15. `context.Context` is universal

Every operation that can block, hit the network, take a lock or be cancelled accepts a `context.Context` as its first argument. This is non-negotiable across the entire public API.

## Consequences

**Positive:**

- The project ships with a coherent, defensible identity (name, license, governance, branding) before any runtime code lands.
- Early contributors have unambiguous answers to license, sign-off, copyright, supported platforms and dependency policy.
- The observability differentiator is structurally protected by D6, D7 and D8.
- Dependency hygiene (D9, D10) is set as a default that's hard to drift away from.

**Negative:**

- Locking in OTel as a core dependency (D6) means a future replacement is costly.
- Per-provider Go modules (D9) increase release coordination overhead; a release script will be required.
- A Windows CI target (D10) constrains us to pure Go and adds runtime cost to PRs.

## Out of scope (deferred to future ADRs)

- **ADR-002** — Cancellation semantics for partially executed graphs (which nodes finish, which are aborted, how state is persisted on cancel).
- **ADR-003** — Retry, backoff and rate-limit handling per provider.
- **ADR-004** — Streaming event schema aligned with OTel GenAI semantic conventions.
- **ADR-005** — Checkpoint serialization format (JSON vs protobuf).
- **ADR-006** — Cross-provider prompt caching policy.
- **ADR-007** — Cost tracking model (per provider, per run, surfaced to the trace UI).
- **ADR-008** — Sandboxing and permissions for tools that touch the shell or the file system.

## References

- `docs/PLAN.md` — full design plan.
- `README.md`, `GOVERNANCE.md`, `CONTRIBUTING.md`, `NOTICE`, `DCO.txt` — the operational expression of the decisions above.
- [Developer Certificate of Origin 1.1](https://developercertificate.org).
- [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0).
- [OpenTelemetry GenAI semantic conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/).
