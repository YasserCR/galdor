# ADR-003 — Bump the Go floor from 1.22 to 1.25

- **Status:** Accepted
- **Date:** 2026-05-17
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** ADR-001 §D2 (partial — only the version pin; the rest of D2's rationale carries over to the new pin)
- **Superseded by:** —

## Context

ADR-001 §D2 set Go 1.22 as the minimum supported version. At the time
(early Phase 0) the choice was deliberately conservative: 1.22 had
matured generics, `range over int` and `loopvar` semantics — the
features the framework most needed — while still being old enough that
"not excluding anyone" was a defensible position.

The picture in May 2026 is different:

- Go's official support window covers the latest two releases.
  Today that's **1.25** (Aug 2025) and **1.26** (Feb 2026). 1.22 has
  been out of security support for two release cycles.
- Several features that the runtime and observability phases would
  use directly are 1.23+:
  - `iter.Seq` / `iter.Seq2` (1.23) — would simplify `StreamReader`
    and the graph runtime's event channel into idiomatic
    `range`-over-func consumers.
  - `slices.Concat`, `slices.Repeat`, `slices.Sorted` (1.23).
  - Improved generics inference (1.24) — already noticeable in the
    conversion layers of the provider adapters.
- Dependency reality: AWS SDK Go v2 already requires a recent Go;
  when `providers/bedrock` ran `go get` it bumped its own module to
  Go 1.24 automatically. Holding the rest of the workspace at 1.22
  was an artificial constraint maintained only by our own go.mod
  files.
- CI matrix: with a 1.22 floor and 1.24 ceiling we ran two parallel
  test rows for every push. Tightening the floor reduces CI cost
  without losing coverage of anything anyone is realistically
  running.

ADR-001 §D12 already declares pre-v1.0 APIs unstable and explicitly
allows breaking changes via the SemVer/CHANGELOG path. The floor is
treated the same way: bumping it is permitted pre-v1.0 if documented.

## Decisions

### D1. The Go floor moves to 1.25

All modules in the workspace — the core (`github.com/YasserCR/galdor`)
and every provider module under `providers/*` — set `go 1.25` in their
`go.mod`. The workspace file (`go.work`) also moves to `go 1.25`. The
choice of 1.25 rather than the strictly more recent 1.26 leaves a small
buffer of "previous stable" for downstream users who haven't yet bumped
to the latest release; it is still inside Go's official support window.

### D2. No `toolchain` directive is pinned in committed files

Each `go.mod` declares a `go` directive but no `toolchain` directive,
so that callers with `GOTOOLCHAIN=auto` (the Go default) seamlessly
auto-download a 1.25+ toolchain when needed and callers with a
specific toolchain pinned in their environment keep using it. CI
selects `go-version: '1.25.x'` explicitly, which gives reproducible
builds without baking a patch version into the repo.

### D3. CI tests against the floor and the latest

The test matrix becomes `[1.25.x]`. A `1.26.x` row can be added once
1.26 has been GA long enough to be considered stable for this project;
that addition is a one-line change and does not need a new ADR.

### D4. ADR-001 §D2's other rationale carries over

D2 in ADR-001 also justified the `go-version` choice in CI and noted
that Windows is supported from day one alongside Linux and macOS.
Those points remain unchanged — the only thing this ADR alters is the
specific version number on the floor.

## Consequences

**Positive:**

- The framework can use `iter.Seq` directly when designing the graph
  runtime and observability stream consumers, instead of routing
  around it.
- CI runs roughly half as many test jobs per push.
- The workspace's `go` directive no longer trails individual modules
  that already required 1.24 transitively via their dependencies.
- The project's stated support window aligns with Google's: only Go
  versions that still receive security fixes are supported.

**Negative:**

- A consumer who has not updated their Go installation since early
  2024 will be unable to import galdor without first installing 1.25.
  Given Go's auto-toolchain mechanism (1.21+) this resolves itself for
  almost everyone; the manual case is rare enough that the trade-off
  is acceptable pre-v1.0.
- Pre-v1.0 floor bumps are technically breaking for module consumers
  who pinned an older Go. ADR-001 §D12 already establishes that
  pre-v1.0 breakage is permitted; this ADR is the explicit record.

## Out of scope

- Adding a `1.26.x` row to the CI matrix once 1.26 has been GA for a
  full release cycle. That is a future trivial change, not a separate
  ADR.
- Migrating existing `Recv(ctx) (Event, error)` iterators to
  `iter.Seq2` shapes. Worth doing once `pkg/graph` lands — it is
  cleaner there because the graph runtime is the heaviest stream
  consumer. Tracked as a Phase 3 follow-up.

## References

- ADR-001 §D2 — original floor choice.
- ADR-001 §D12 — pre-v1.0 API stability policy that authorizes this
  kind of bump.
- [Go release policy](https://go.dev/doc/devel/release) — two
  most-recent releases under support.
- [Go toolchain mechanism](https://go.dev/doc/toolchain) — explains
  how `GOTOOLCHAIN=auto` (the default) auto-downloads a newer Go when
  required by a module.
