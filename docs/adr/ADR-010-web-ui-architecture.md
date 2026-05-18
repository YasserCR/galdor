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

### D8. Content capture is opt-in on `InstrumentProvider` (Session B)

The span detail page surfaces prompt and completion messages
when the producer recorded them as `gen_ai.prompt` /
`gen_ai.completion` attributes. Capture is enabled per
`InstrumentProvider` call via `observability.WithCaptureContent(true)`
and **off by default**: prompts routinely carry PII, secrets, or
proprietary text that should not silently land in a trace store
the user might later commit, share, or expose via the dashboard.

The decision lives on the producer side (one boolean, scoped to
one instrumentation site) rather than as a render-time toggle
because the wrong default is the one that quietly leaks data.
A render-time switch can suppress display but cannot un-record;
producer-side opt-in keeps the store free of content unless the
user asked for it.

Capture serializes the `schema.Message` slice (or single
response message) as compact JSON into the attribute value. The
UI decodes it back into structured form for the dashboard;
`/api/runs/{id}/spans/{spanID}` returns the raw attribute so
external tooling can re-parse it identically.

### D9. Live updates via SSE; SVG timeline server-rendered (Session C)

Live updates use Server-Sent Events at `/api/stream/runs`. The
server polls the store at a fixed cadence (default 1s, matches
`galdor scry tail`) and emits a `run` event whenever a span
arrives that belongs to a previously unseen trace, or updates an
existing run's summary. A `heartbeat` event fires every tick
even with no new data so reverse proxies and idle-detection
timers don't drop the connection.

SSE was chosen over WebSockets because the channel is one-way
(server → client), the existing JSON shape is reused verbatim,
and there is no upgrade handshake to special-case. The tradeoff
— SSE caps at six concurrent connections per origin in some
browsers — does not matter for a local-only dashboard.

Client code is a single ~80-line vanilla-JS file
(`internal/ui/static/live.js`). It is the first JS galdor
ships, and the file is intentionally dependency-free: if it
fails to load or `EventSource` is missing, the page works
unchanged from a static refresh. ADR-010 D1 allowed this
vendor-free JS escape hatch when a richer client became
necessary; SSE made it necessary.

The execution timeline is **server-rendered SVG**, not a JS
charting library. `buildTimeline` computes absolute pixel
coordinates from span start/end times in Go; the template emits
`<rect>` and `<text>` elements directly. Pros: zero runtime
overhead in the browser, identical render under printing or
JS-disabled, trivially diffable in tests. Con: pan/zoom would
need either JS or per-request re-render; deferred until someone
actually traces a >100-span run and feels the need.

## Consequences

**Now**
- Users can run `galdor ui` against the same DB the CLI reads and
  get a browsable run list + span tree in seconds.
- The CLI and the dashboard are guaranteed to agree on every
  number — they share `internal/store`, including run aggregation
  rules (trace_id resolution, run status derivation).
- External callers can hit `/api/runs` for a stable JSON shape.

**Session B**
- Per-span detail page at `/runs/{id}/spans/{spanID}` with the
  full attribute table, events list, and (when capture is on)
  the prompt + completion messages rendered side-by-side.
- Span rows in the tree are now anchor links, not just text.
- Routes migrated to Go 1.22+ ServeMux patterns
  (`/runs/{runID}/spans/{spanID}`) instead of `TrimPrefix`.
- `observability.WithCaptureContent(bool)` option for opt-in
  prompt/completion recording.

**Session C (this revision)**
- SSE feed at `/api/stream/runs` for live run-list updates.
- Tiny vanilla JS (`static/live.js`) drives row insert/update
  on the runs page; page falls back to static when JS / SSE is
  unavailable.
- SVG timeline (Gantt-style) on the run detail page rendered
  fully server-side; clickable bars link to the span detail.

**Later (deferred)**
- Workflow graph (DAG render of the static `Graph[S]`, not
  just the execution trace) — needs the graph structure on the
  root span; Phase 6 candidate.
- Spellbook integration (prompt registry browsing once that
  ships in Phase 7).
- Pan/zoom on the timeline if span counts cross ~100.
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
