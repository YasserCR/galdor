# Architecture Decision Records

This directory contains the canonical ADRs for galdor. Each ADR is a small Markdown file recording a single design decision, its context and its consequences.

## Index

| ID | Title | Status |
|----|-------|--------|
| [ADR-001](ADR-001-foundational-decisions.md) | Foundational decisions | Accepted (§D2 partially superseded by ADR-003) |
| [ADR-002](ADR-002-provider-abstraction-shape.md) | Provider abstraction shape | Accepted |
| [ADR-003](ADR-003-bump-go-floor-to-1.25.md) | Bump the Go floor from 1.22 to 1.25 | Accepted |
| [ADR-004](ADR-004-tool-system-shape.md) | Tool system shape | Accepted |
| [ADR-005](ADR-005-graph-runtime-shape.md) | Graph runtime shape | Accepted |
| [ADR-006](ADR-006-checkpointing-and-interrupts.md) | Checkpointing and interrupt/resume | Accepted |
| [ADR-007](ADR-007-agent-helpers.md) | Agent helpers (`pkg/agent`) | Accepted |
| [ADR-008](ADR-008-observability-instrumentation.md) | Observability instrumentation | Accepted |
| [ADR-009](ADR-009-sqlite-span-store-and-scry-cli.md) | SQLite span store and `galdor scry` CLI | Accepted |
| [ADR-010](ADR-010-web-ui-architecture.md) | Web UI architecture (`galdor ui`) | Accepted |
| [ADR-011](ADR-011-parse-json-helper.md) | `schema.ParseJSON[T]` tolerant JSON helper | Accepted |
| [ADR-012](ADR-012-typed-provider-errors.md) | Typed provider errors | Accepted |
| [ADR-013](ADR-013-cli-surface-pruning.md) | CLI surface pruning (`serve`/`recast`/`forge` dropped) | Accepted |
| [ADR-014](ADR-014-config-format-and-cli-module.md) | Config format (YAML) and the `cmd/galdor` module split | Accepted |
| [ADR-015](ADR-015-structured-output.md) | Schema-bound structured output | Accepted |

## Conventions

- File name: `ADR-NNN-kebab-case-title.md`.
- IDs are monotonic and never reused.
- Required sections: **Context**, **Decisions**, **Consequences**.
- An ADR is either *Proposed*, *Accepted*, *Superseded by ADR-XXX* or *Deprecated*.
- ADRs are append-only: do not edit an Accepted ADR — write a new one that supersedes it.
- Non-trivial design changes must land via an ADR with a minimum review window (see `GOVERNANCE.md`).
