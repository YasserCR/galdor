# ADR-009 — SQLite span store and `galdor scry` CLI

- **Status:** Accepted
- **Date:** 2026-05-18
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

ADR-008 (Phase 4 session A) wired OpenTelemetry instrumentation
through the lower-level primitives, but stopped short of giving
those spans somewhere persistent to live. The plan's elevator
pitch — "the binary IS the observability backend" — requires an
embedded store and a way to query it. ADR-001 §D7 already chose
the storage technology: SQLite via the pure-Go
`modernc.org/sqlite` driver. This ADR settles the rest:

1. What shape does the store expose to producers (the OTel
   exporter) and consumers (the CLI, the future Web UI)?
2. How do spans round-trip through that shape — what gets
   normalized, what stays as JSON?
3. How is a "run" identified across the spans that compose it,
   given that only the root span carries `galdor.run.id`?
4. What does the v1 CLI look like — verbs, flags, output formats?

## Decisions

### D1. One package owns persistence: `internal/store`

`internal/store.Store` is the single point of contact with the
SQLite database. The OTel exporter (`pkg/observability/SQLiteExporter`)
writes through it; the CLI (`cmd/galdor scry`) reads through it.
The package lives under `internal/` because the schema is going to
move (a Phase 9 replay needs more), and we don't want callers
binding to a SQL surface that's not stable yet.

The exported types — `store.Span`, `store.Event`, `store.RunSummary`,
`store.Store` — are intentionally Go-shaped, not OTel-shaped, so
the CLI and the eventual Web UI don't need an OTel dependency
just to read traces.

### D2. One table, denormalized run_id, JSON-blob attributes

The schema is:

```sql
CREATE TABLE spans (
  span_id              TEXT PRIMARY KEY,
  trace_id             TEXT NOT NULL,
  parent_span_id       TEXT NOT NULL DEFAULT '',
  name                 TEXT NOT NULL,
  start_time_unix_nano INTEGER NOT NULL,
  end_time_unix_nano   INTEGER NOT NULL,
  status_code          TEXT NOT NULL DEFAULT 'unset',
  status_message       TEXT NOT NULL DEFAULT '',
  attrs_json           TEXT NOT NULL DEFAULT '{}',
  events_json          TEXT NOT NULL DEFAULT '[]',
  run_id               TEXT NOT NULL DEFAULT ''
);
```

Indexes on `trace_id`, `run_id`, `parent_span_id`, and
`start_time_unix_nano`. PRAGMAs: `journal_mode=WAL`,
`busy_timeout=5000`, `foreign_keys=on`.

Attributes are stored as a single JSON blob rather than a
normalized side table. Reasons:

- Span attributes are sparse — Anthropic, OpenAI, Google and
  Bedrock each contribute different `gen_ai.*` keys, and we keep
  adding `galdor.*` ones. A side table forces every new attribute
  to grow the schema or live as TEXT name/value pairs that need
  parsing anyway.
- The CLI and the Web UI need the whole attribute set per span,
  not random-access by key. JSON blobs are the lighter read.
- SQLite's JSON1 functions can index into the blob for future
  filtering queries; we don't need them yet, but the option is
  there.

`run_id` is denormalized into a dedicated column off the
`galdor.run.id` attribute so the `ListRuns` aggregation doesn't
have to crack open the JSON of every span at query time. The
column is only populated for the root span — see D3.

### D3. Runs are identified by `trace_id`, not by `galdor.run.id`

Spans emitted by `InstrumentProvider` and `InstrumentTool` do not
carry the `galdor.run.id` attribute — they don't know it. They do,
however, share the run's `trace_id` because they're descendants of
the run span (the OTel SDK propagates trace IDs through the
context returned by `BeforeNode`).

The store queries reflect this. `ListRuns` aggregates by
`trace_id`, picking out the `run_id` from whichever span in that
trace carries it (the root). `SpansForRun(runID)` resolves
`run_id -> trace_id` via the root span and then fetches every
span sharing that trace. The denormalized `run_id` column is
populated only on spans that have the attribute, but the queries
are written against `trace_id`.

The alternative — propagating `galdor.run.id` to every descendant
span via context or baggage — was rejected. Spans from
`pkg/observability/InstrumentProvider` would have to look up the
ID from a context key the runtime knows about, leaking graph
concerns into the provider wrapper. Querying by `trace_id` keeps
the producer side ignorant of the run concept and centralizes the
join in the storage layer where it belongs.

### D4. `SQLiteExporter` is a plain `sdktrace.SpanExporter`

`observability.NewSQLiteExporter(path)` opens a `*store.Store`,
implements `ExportSpans` and `Shutdown`, and is otherwise a
boring exporter. Callers wire it the same way they would wire
OTLP or stdouttrace:

```go
exp, _ := observability.NewSQLiteExporter("/.galdor/traces.db")
tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))
```

`Store()` exposes the underlying store so the CLI (and tests) can
reuse the same handle for reads without opening the file twice.
After `Shutdown`, further `ExportSpans` calls return
`ErrExporterShutdown` so a misconfigured shutdown order produces a
loud error rather than silent data loss.

### D5. `galdor scry` is the v1 CLI

Two verbs:

- `galdor scry list [--db PATH] [--limit N] [--format text|json]`
- `galdor scry show <run-id> [--db PATH] [--format tree|json]`

Output:

- `list text` is a fixed-width table: run ID, status, duration,
  span count, error count, start time.
- `show tree` walks the parent/child relationships and renders an
  indented tree with the duration and a compact attribute suffix
  for the most useful fields (node name, provider, tool name,
  token counts). Stdlib only — no third-party tree renderers.
- `json` output on either verb returns the underlying
  `RunSummary` / `Span` slices verbatim so other tooling (jq,
  Phase 5 UI, custom scripts) can consume them.

DB path resolution: explicit `--db`, then `$GALDOR_DB`, then
`~/.galdor/traces.db`. Each layer is overridable; the default is
chosen for portability over per-project conventions.

### D6. Std-flag dispatcher, no third-party CLI library

The verb dispatcher in `cmd/galdor/scry.go` uses only `flag` and a
small switch. ADR-001 §D5 keeps core dependencies minimal; a
`cobra` / `urfave` import for two subcommands and three flags
would buy nothing. The cost is the standard Go quirk: flags must
come before positional arguments. Documented in `scry show`'s
usage line.

### D7. Internal package; no external API surface yet

`internal/store` cannot be imported by downstream users. That's
deliberate: the schema and the queries are likely to evolve as
Phase 4 session C adds metrics rollups and Phase 9 adds replay.
When the API stabilizes a thin wrapper at `pkg/store` (or another
location) will re-export the read surface. Until then, callers
that want trace data go through `galdor scry --format json` or
hit the SQLite file directly — both are stable contracts.

## Consequences

**Positive.** The observability story is end-to-end inside one
binary: instrument with `pkg/observability`, persist with
`NewSQLiteExporter`, inspect with `galdor scry`. No external
service, no Postgres, no ClickHouse. The store is one table with
indexes that match the v1 queries; growing it for richer queries
is a matter of new indexes or JSON1 expressions, not a schema
rewrite. Coverage on the new packages: `internal/store` 78.6%,
`pkg/observability` 84.9% (including the exporter),
`cmd/galdor` 76.6% (mostly CLI dispatch — the rendering and
query paths are exercised end-to-end).

**Negative.** SQLite is a single-process database. Two galdor
processes writing to the same file will work thanks to WAL mode
but contend on writes; really concurrent fleets need an external
backend. The schema's JSON-blob attributes can't be indexed by
key without explicit JSON1 expressions; per-attribute filtering
will require those when the time comes. Both trade-offs were
already accepted in ADR-001 §D7.

## Out of scope

- **`galdor scry tail`** (live follow mode that streams new
  spans as they're written). Plumbing is straightforward — a
  cursor on `start_time_unix_nano` plus a sleep loop — but
  Phase 4 session C will land it alongside metrics so the
  surface stays cohesive.
- **Derived metrics**: latency p50/p95/p99, total token spend,
  per-provider cost. Same session as `tail`. The queries are
  cheap on the existing schema.
- **Trace deletion / retention.** Out of scope for v1; the file
  is the unit of retention. A future ADR addresses TTL policy.
- **Web UI** (Phase 5) reads the same store via the same
  internal package, but adds an HTTP server, templates and a
  graph-rendering layer; that's its own ADR when it lands.

## References

- ADR-001 §D7 — SQLite via modernc.org/sqlite chosen as the
  default embedded backend.
- ADR-005 — graph runtime (provides `Hooks[S]`).
- ADR-006 — checkpointer (parallel concern; checkpoints and
  spans coexist in the same DB only by accident — the Phase 9
  replay ADR will decide whether they share a file).
- ADR-008 — OTel instrumentation (the producer side).
- `internal/store/store.go`, `pkg/observability/sqlite_exporter.go`,
  `cmd/galdor/scry.go`.
- `examples/scry-store/` — runnable demo of the entire pipeline.
