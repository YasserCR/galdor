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

The project's `NOTICE` file attributes overall copyright to the original maintainer (personal name) — this preserves flexibility for the project's future (e.g. transferring to a foundation) without binding contributors.

## Commercial neutrality

galdor is and will remain a personal / community project, **not owned by any company**. There will be:

- No "Enterprise" edition.
- No paid feature tiers.
- No SaaS gating core functionality.

If a commercial offering ever emerges around galdor it will be in the form of optional **support, consulting or managed hosting**, and will not alter the open source license or feature parity of the upstream project.

## Relicensing

The project license (Apache 2.0) will not change without:

- An ADR proposing the change with rationale.
- Explicit sign-off from all maintainers.
- A reasonable comment window (≥ 30 days) for the community.

DCO is intentionally used (instead of an aggressive CLA) so that no single entity has the unilateral right to relicense contributions.

## Amendments

This document is itself governed by the rules above: amendments require an ADR and follow the same review process as a significant change.
