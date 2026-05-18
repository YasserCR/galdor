# ADR-010 — Web UI architecture

- **Status:** Accepted
- **Date:** 2026-05-17
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

Phase 4 (ADR-008 + ADR-009) gave galdor end-to-end observability
on the CLI: spans flow through `pkg/observability` into the
embedded SQLite store, and `galdor scry list/show/stats/tail`
makes them queryable from a terminal. Phase 5 in the roadmap
promises the "Langsmith-local" experience — a browser dashboard
served from the same single binary, reading the same store.

This ADR pins down what that surface looks like in v1: how it
ships, how it's structured, what it serves, and what is
deliberately deferred to later Phase 5 sessions.

## Decisions

### D1. Single binary, embedded assets

All HTML templates, CSS, and (eventually) JavaScript live under
`internal/ui/{templates,static}` and are compiled into the
`galdor` binary via `//go:embed`. There is no separate frontend
build step, no `dist/`, no `node_modules`. This is consistent with
the project's "un solo binario por defecto" principle (PLAN §5.1)
and removes an entire class of operational pain (toolchain version
skew, npm supply chain, asset hosting).

Trade-off: complex interactive UI (graph viz, live timelines) will
push against the limits of server-rendered HTML. We accept this as
a Session B / C problem — when we need a richer client we'll vendor
a tiny dependency-free JS file rather than introducing a build
pipeline. Today's surface (run list + span tree) doesn't need it.

### D2. Server-rendered HTML, `html/template` only

No SPA. No client-side router. The server emits complete pages
on `GET /` and `GET /runs/{id}` using `html/template`. The
templates are independently named (`runs.html`, `run.html`,
`error.html`) and recursive structures (the span tree) are
expressed via `{{ template "spanNode" . }}` self-reference.

Why: the dashboard reads from a local SQLite file with handfuls of
rows. The complexity budget should go to the trace data and the
debugging workflow, not to a frontend framework. When live updates
become non-trivial (Session C) we add SSE — still no SPA.

### D3. Twin surfaces: HTML + JSON, same handler ladder

Every page-rendering route has a JSON sibling under `/api/`:

| HTML route          | JSON route                    | Shape                |
|---------------------|-------------------------------|----------------------|
| `GET /`             | `GET /api/runs?limit=N`       | `[]store.RunSummary` |
| `GET /runs/{id}`    | `GET /api/runs/{id}/spans`    | `[]store.Span`       |

JSON is shipped from day one — not because we plan to build an
external frontend, but because it lets users and scripts treat the
running dashboard as a query API (`curl localhost:7777/api/runs |
jq`) without needing a CLI. It also makes the planned Session C
live-update path cheap: SSE will reuse the JSON shape.

### D4. Loopback by default

`galdor ui` binds to `127.0.0.1:7777` unless `--addr` says
otherwise. Trace data routinely contains prompt/response text that
the user has not consented to publish on a LAN. Defaulting to
loopback means an accidental `galdor ui` on a shared network
doesn't leak runs.

### D5. `internal/ui` is `internal/`, not `pkg/`

Same reasoning as ADR-009 for `internal/store`: the route shape,
the template names, and the page data structs are not yet a
stable contract. Hiding them under `internal/` lets us reshape the
templates in Phases 6–9 (memory views, multi-agent topology,
replay scrubber) without breaking importers. Public API surface
remains the CLI: `galdor ui`.

### D6. Server owns the mux and templates; not the store lifecycle

`ui.NewServer(*store.Store, Options) (*Server, error)` takes a
store the caller already opened. The CLI command owns
`store.Open` / `Close`, signal handling, and graceful shutdown via
`Server.ListenAndServe(ctx, addr, resolved)`. The
`resolved` callback exists so the test harness can learn the
ephemeral port chosen by `:0`; production callers ignore it.

### D7. CSS lives in `internal/ui/static/style.css`, dark by default

One file, ~150 lines, no preprocessor. Dark theme baked in
because every comparable tool (Honeycomb, Tempo, Jaeger,
LangSmith) defaults dark and the contrast on terminal-adjacent
data lands cleaner. Light theme can come later as a CSS variable
toggle if asked for; it's not v1 scope.

## Consequences

**Now**
- Users can run `galdor ui` against the same DB the CLI reads and
  get a browsable run list + span tree in seconds.
- The CLI and the dashboard are guaranteed to agree on every
  number — they share `internal/store`, including run aggregation
  rules (trace_id resolution, run status derivation).
- External callers can hit `/api/runs` for a stable JSON shape.

**Later (deferred)**
- Session B: graph visualization (DAG render), input/output diffs
  on provider spans, attribute search.
- Session C: live updates via SSE, polling cadence options,
  Spellbook integration (prompt registry browsing once that ships
  in Phase 7).
- Eval surface (Phase 8) and Replay scrubber (Phase 9) plug into
  the same `Server.registerRoutes` ladder.

**Pinned trade-offs**
- No SPA, no JS framework, accepted: see D1/D2.
- Loopback default is mildly inconvenient on remote hosts —
  documented mitigation: `--addr 0.0.0.0:7777` with the user's
  explicit opt-in.
- Recursive `html/template` blocks have a depth limit baked into
  the Go runtime (10k). A pathological span tree could trip it;
  reachable only with adversarial input, deferred.
