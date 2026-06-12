# galdor governance

This document describes how galdor is governed today and how it intends to evolve.

## Current state (Phase 0 — May 2026)

galdor is in its bootstrap phase, maintained by a single person acting as BDFL (Benevolent Dictator For Life *— informally*):

- **Maintainer:** Gustavo Yasser ([@YasserCR](https://github.com/YasserCR))

The BDFL has final say on technical direction, releases and dispute resolution **until the project transitions to a multi-maintainer model**. This is an explicit, time-bound concession to keep early velocity high — not a long-term design.

## Stated intent

galdor will transition to a multi-maintainer governance model **as soon as the project has at least three sustained, regular contributors** (defined as: 10+ merged PRs each, across at least 3 calendar months). At that point:

- A **Maintainers team** is formed with commit and merge rights.
- Decisions move to a **lazy consensus / RFC** model (see below).
- The BDFL retains a casting vote only in deadlocks, not by default.

## Decision-making

### Today (Phase 0 → Phase ~3)

- Trivial and incremental changes: any maintainer can merge after review.
- Non-trivial design changes: opened as an **ADR** (Architecture Decision Record) under `docs/adr/`, with at least 72 hours of review window before merge.
- API-breaking changes prior to v1.0 are allowed but must be documented in the changelog and announced in `Discussions`.

### Future (post first three sustained contributors)

- **Lazy consensus:** silence after 72 hours on an ADR or RFC = acceptance.
- **Significant changes** (new public package, removal of a public package, license change, governance change) require explicit approval from at least 2 maintainers and no objection from any other.
- **Disputes** are resolved by simple majority of maintainers; ties broken by the BDFL.

## Roles

- **Contributor:** anyone who has had a PR merged.
- **Reviewer:** trusted contributor with review (but not merge) rights, by invitation from any maintainer.
- **Maintainer:** has commit and merge rights, listed in `MAINTAINERS.md` (to be added when the role is non-empty beyond the BDFL).

## Copyright

Copyright on contributions remains with the contributor. The DCO (see [`DCO.txt`](DCO.txt)) certifies that contributors have the right to submit their code under the project's Apache 2.0 license.

The project's `NOTICE` file attributes overall copyright to the original maintainer (personal name) — this keeps the project's future stewardship flexible without binding contributors.

## Project structure

galdor is currently an independent project, maintained by its authors rather than controlled by a vendor. The maintainers' aim is to keep the open source core useful and complete on its own terms. Any change to the project's ownership, structure, or licensing would be made openly and follow the process in **Relicensing** below.

## Relicensing

A change to the project license (Apache 2.0) would be made openly:

- An ADR proposing the change with rationale.
- Sign-off from the maintainers.
- A reasonable comment window (≥ 30 days) for the community.

Contributions are accepted under the DCO today, with each contributor retaining copyright over their work under Apache 2.0. If the project later adopts a CLA — for example to enable dual licensing — it would be introduced through the same open process.

## Amendments

This document is itself governed by the rules above: amendments require an ADR and follow the same review process as a significant change.
