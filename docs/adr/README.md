# Architecture Decision Records

This directory contains the canonical ADRs for galdor. Each ADR is a small Markdown file recording a single design decision, its context and its consequences.

## Index

| ID | Title | Status |
|----|-------|--------|
| [ADR-001](ADR-001-foundational-decisions.md) | Foundational decisions | Accepted (§D2 partially superseded by ADR-003) |
| [ADR-002](ADR-002-provider-abstraction-shape.md) | Provider abstraction shape | Accepted |
| [ADR-003](ADR-003-bump-go-floor-to-1.25.md) | Bump the Go floor from 1.22 to 1.25 | Accepted |

## Conventions

- File name: `ADR-NNN-kebab-case-title.md`.
- IDs are monotonic and never reused.
- Required sections: **Context**, **Decisions**, **Consequences**.
- An ADR is either *Proposed*, *Accepted*, *Superseded by ADR-XXX* or *Deprecated*.
- ADRs are append-only: do not edit an Accepted ADR — write a new one that supersedes it.
- Non-trivial design changes must land via an ADR with a minimum review window (see `GOVERNANCE.md`).
