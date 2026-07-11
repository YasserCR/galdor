# Changelog

All notable changes to galdor are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Pre-1.0 conventions: minor versions (v0.x.0) may introduce breaking changes;
patch versions (v0.x.y) are reserved for backward-compatible fixes and release
hygiene (docs, build metadata).

## [Unreleased]

## [1.2.1] - 2026-07-10

### Security
- **Bump the minimum Go to 1.25.12** across every module to pick up the
  standard-library fix for **GO-2026-5856** (Encrypted Client Hello privacy leak
  in `crypto/tls`), which govulncheck found reachable via the web UI
  (`internal/ui`), the MCP HTTP client (`pkg/mcp`) and graph SVG rendering
  (`pkg/graph`). `GOTOOLCHAIN=auto` pulls the patched toolchain; no source
  changes. `govulncheck` is clean under go1.25.12.

## [1.2.0] - 2026-07-10

### Added
- **Hybrid retrieval via Reciprocal Rank Fusion** (`memory.HybridRetriever`):
  fuses any number of retrieval sources (e.g. a lexical BM25 store and a dense
  vector retriever) with RRF (k=60), reusing the existing `Store`, `Retriever`
  and `Embedder` unchanged. A new `memory.Searcher` interface (the read half of
  `Store`) is what sources satisfy. Additive, pure-Go, no new dependencies.
  See ADR-017.
- **OKF knowledge backend** (`memory/okf`): a `memory.Store` over Open Knowledge
  Format bundles (markdown + YAML frontmatter in a git tree). Loads a bundle,
  chunks concept-first (folding title/description/tags into the indexed text),
  and retrieves via BM25 by wrapping the SQLite/FTS5 store. Ships
  `okf.NewSearchTool` for ReAct agents. Tag-membership filtering and link-graph
  outlinks are handled inside the module (via a reserved `FilterTag` key and
  metadata) without changing the core `memory.Query` contract. The frontmatter
  parser is dependency-free. See ADR-016.
- **`examples/okf-rag`**: end-to-end RAG over an embedded OKF bundle with
  `--mode bm25` and `--mode hybrid`, composing the OKF store's BM25 with a dense
  retriever under `HybridRetriever`.

### Docs
- ADR-016 (OKF backend) and ADR-017 (hybrid RRF); `docs/concepts/memory.md` and
  `docs/patterns/rag.md` document the new retriever and backend.

## [1.1.0] - 2026-06-26

### Added
- **Amazon S3 Vectors memory backend** (`memory/s3vectors`): a serverless,
  AWS-native `memory.Store` — a drop-in alternative to `memory/pgvector` and
  `memory/qdrant` for deployments that want durable vector storage without
  operating a database. Credentials resolve via the default AWS credential
  chain; the vector index is auto-created on `Open` (the vector bucket must
  pre-exist); the chunk body is stored as non-filterable metadata so it
  round-trips with query results. Verified end-to-end against a live S3 Vectors
  bucket, including `PutVectors` batching, query pagination, multi-page delete,
  and multi-key `$and` metadata filters.

### Changed
- Bumped `aws-sdk-go-v2` to v1.42.0 and `smithy-go` to v1.27.1 across the
  AWS-touching modules (required by the S3 Vectors SDK; backward-compatible —
  the only core change is a retry preview gated behind an opt-in env flag).

## [1.0.0] - 2026-06-12

galdor 1.0. The public API under `pkg/` is now **stable under SemVer**:
breaking changes only land in a future major version. `internal/` remains
implementation detail, and the CLI's YAML config format is versioned
independently (`version: 1`).

What 1.0 means here, concretely: four provider adapters (Anthropic, OpenAI
+ compatible hosts, Google, Bedrock) behind one capability-gated interface;
type-safe tools with reflection-derived schemas; a generic graph runtime
with checkpoints, interrupts and streaming; ReAct and Plan-and-Execute
helpers; Supervisor/Swarm multi-agent; MCP client + server (stdio, SSE,
Streamable HTTP) and A2A; memory backends (in-mem, SQLite/BM25, pgvector,
qdrant) with provider-backed embedders; schema-bound structured output; an
eval framework with CI gates; deterministic replay; OTel-native
observability with an embedded SQLite store and dashboard; and a
nine-verb CLI (`scry`, `ui`, `mcp`, `weave`, `trial`, `cast`, `council`,
`spellbook`, `doctor`) whose config-driven verbs run agents, teams and
eval suites from YAML — no Go required.

### Fixed
- **`--help` exits 0 everywhere.** Every verb and subcommand that parses
  flags treated `-h`/`--help` as a usage error (exit 64); asking for help
  now prints the usage and exits 0, uniformly across all 9 verbs and their
  subcommands.

### Docs
- README install snippet notes the `$GOBIN`-on-PATH gotcha (with a pointer
  to `galdor doctor`); observability.md documents that galdor spans nest
  under any caller-provided parent span via standard context propagation.
- `SECURITY.md` added (private reporting via GitHub Security Advisories);
  README/ARCHITECTURE state the SemVer commitment; ROADMAP labels remaining
  items as post-v1.0.

### Build
- Submodule `require` pins bumped v0.15.1 → v1.0.0; `cmd/galdor` tagged
  last. No new dependencies.

## [0.15.1] - 2026-06-12

Post-audit hardening: a second full audit pass over everything the
v0.9.1–v0.15.0 series shipped. The code findings were small; each fix has a
regression test. Green under `go test -race`, `go vet`, golangci-lint
v2.12.2 and gosec across the affected modules.

### Fixed
- **A2A task snapshots no longer alias the live Metadata map.** A
  `tasks/get` snapshot shared the map with the stored task, so the
  Metadata merge a concurrent `tasks/send` performs could race with (or
  bleed into) the snapshot's reader. Snapshots now deep-copy Metadata.
- **`spellbook` rejects NUL bytes in spell names/versions**, alongside the
  existing path-separator and `..` checks.
- **`cast`/`council` stdin handling is explicit.** A stdin read error or a
  piped input at/over the 1 MiB cap is now an error, never a silent empty
  or truncated input. `trial` rejects a non-positive `timeout_per_case`.
- **JSON-encode failures name the right verb** (`mcp ls --json` no longer
  reports its error as `scry:`).

### Changed
- The MCP Streamable HTTP **client** transport buffers up to 64 in-flight
  replies (was 8) and documents the backpressure behavior.
- `provider.ResponseFormat` documents the per-provider behavior: on
  Anthropic a JSONSchema request replaces Tools/ToolChoice and is
  Generate-oriented (Stream surfaces raw tool deltas); OpenAI/Google
  support it natively in both paths. `memory.Chunk.Embedding` documents
  that retrieved slices must not be mutated.

### Docs
- README: the CLI section now lists every verb (mcp, weave, trial, cast,
  council, spellbook, doctor were missing), the examples tables include the
  six new examples, and the comparison table reflects the client-side
  Streamable HTTP transport. ARCHITECTURE.md reconciled (15 ADRs, layered
  view, full verb list). `.gitignore` covers example-directory binaries.

### Tests
- New regressions: A2A snapshot Metadata copy, spellbook NUL-byte
  rejection, and a two-worker supervisor test pinning per-worker closure
  capture (verifying a reported audit finding was a false positive).

## [0.15.0] - 2026-06-12

Ecosystem & adoption: lower the friction of the first integration. Green
under `go test -race`, `go vet`, golangci-lint v2.12.2 and gosec across the
root and the CLI module.

### Added
- **`galdor doctor`** — an environment check that prints a checklist: the
  Go toolchain (and whether `go` is on PATH for building from source),
  whether the `go install` bin directory is on PATH, which provider
  credential env vars are set (values never printed), and whether the trace
  store is reachable/writable. Exits 1 on a hard failure, 0 otherwise.
- **`examples/integration-http-interpret`** — a complete copy-paste HTTP
  service wrapping an agent: structured output as the API contract, OTel
  tracing into the SQLite store, a health endpoint, and graceful shutdown.
  The "ship the example, not the abstraction" answer to "how do I serve an
  agent" — galdor has no `pkg/serve` on purpose.
- **Feedback intake** — `docs/feedback/` and GitHub issue templates (bug
  report, feature request, integration feedback).

### Build
- Submodule `require` pins bumped v0.14.0 → v0.15.0; `cmd/galdor` tagged
  last. No new dependencies.

## [0.14.0] - 2026-06-12

Schema-bound structured output: a Go type in, a decoded Go value out.
Green under `go test -race`, `go vet`, golangci-lint v2.12.2 and gosec
across the root and the affected provider modules.

### Added
- **`provider.GenerateStructured[T]`** — constrain a model's reply to the
  shape of a Go struct and get it back decoded in one call. It derives a
  JSON Schema from `T` (the same `json` / `jsonschema` tags tools use),
  sets `Request.ResponseFormat`, calls `Generate`, and decodes the reply
  via `schema.ParseJSON` (tolerating code fences / surrounding prose).
- **`provider.JSONSchemaFor[T]`** — the derived schema bytes on their own,
  for setting `ResponseFormat.Schema` by hand or reusing across calls.
- **Anthropic structured output.** Anthropic now reports
  `StructuredOutput: true`: a `json_schema` request is expressed as a
  single forced tool whose `input_schema` is the schema, and the tool reply
  is unwrapped back into the message text. Structured output now works with
  OpenAI, Google, and Anthropic. Bedrock stays unsupported (it fronts
  several model families).
- `examples/structured-output`.

### Docs
- Removed ADR references and design-rationale notes from the user-facing
  concept docs and example READMEs — they now read as implementer
  documentation, not maintainer notes. (ADRs stay under `docs/adr/`.)

### Build
- Submodule `require` pins bumped v0.13.0 → v0.14.0; `cmd/galdor` tagged
  last. No new dependencies.

## [0.13.0] - 2026-06-12

The prompt registry ships, completing the CLI surface — every advertised
verb is now implemented. Green under `go test -race`, `go vet`,
golangci-lint v2.12.2 and gosec across the root and the CLI module.

### Added
- **`pkg/spellbook`** — a versioned prompt registry. `Spell` (name +
  version + template + metadata), `Book` (`Get` / `Latest` / `List`), an
  in-memory `New` and a file-backed `Open` (one raw `.md` per version, so
  prompts diff and review like code), and `Spell.Render` over Go
  `text/template` (a missing key errors rather than emitting `<no value>`).
  Stdlib-only — the core module stays dependency-light.
- **`galdor spellbook list | show | diff | render`** — manage a spell
  directory from the CLI (`diff` is a unified +/- line diff). `--dir`
  defaults to `$GALDOR_SPELLBOOK`, then `./spells`. `examples/spellbook`.
- **`system_spell` in the agent block** — `cast` / `council` / `trial`
  agents can pull their system prompt from a versioned spell
  (`system_spell: {name, version}`) instead of inlining it, so a fleet
  shares one reviewed prompt. Mutually exclusive with inline `system:`.

### Changed
- The CLI usage no longer has a "planned" section — `spellbook` was the
  last unimplemented verb.

### Build
- Submodule `require` pins bumped v0.12.0 → v0.13.0; `cmd/galdor` tagged
  last (ADR-014 D3). No new dependencies.

## [0.12.0] - 2026-06-12

The config-driven verbs are complete: an agent and a multi-agent team now
run from a YAML file, on the same agent block `trial` already used. Green
under `go test -race`, `go vet`, golangci-lint v2.12.2 and gosec across the
root and the CLI module.

### Added
- **`galdor cast <agent.yaml> "<input>"`** — run a ReAct agent from a YAML
  agent block (provider + model + optional system/tools), no Go required.
  Input is a positional argument or piped on stdin. `--trace [--db PATH]
  [--run-id ID]` records the run — provider, tool and node spans — to the
  span store, so it shows up in `galdor scry` / `ui` / `weave`. Flags are
  honored wherever they appear (a "--" terminator protects literal-dash
  input). `examples/cast-agent`.
- **`galdor council <topology.yaml> "<input>"`** — run a multi-agent
  orchestration from YAML. `mode: supervisor` (default) wires a routing LLM
  that delegates to named worker agents; `mode: swarm` wires peers that hand
  off to each other. Each worker is an agent block. `examples/council-team`.

### Fixed
- **`galdor version` reports the real version when installed via `go install`.**
  It previously always printed `0.0.0-dev`: the version was only ever set by
  an `-ldflags` injection that no build actually performed, and
  `go install …/cmd/galdor@vX` passes no ldflags. It now falls back to the
  module version Go embeds in the binary (`runtime/debug.ReadBuildInfo`), so
  a go-installed binary reports its tag, a local build reports the VCS
  revision, and an explicit ldflags injection still wins. The MCP
  server/client info reports the same resolved version.

### Build
- Submodule `require` pins bumped v0.11.0 → v0.12.0; the `cmd/galdor` module
  is tagged last (its `go.sum` seals against the published siblings, per
  ADR-014 D3). New dependency `go.opentelemetry.io/otel/sdk` in the CLI
  module (for `cast --trace`).

## [0.11.0] - 2026-06-11

Config-driven evaluation from the CLI, and the module-structure change that
makes config-driven verbs possible without bloating the core. Green under
`go test -race`, `go vet`, golangci-lint v2.12.2 and gosec across the root
and the new CLI module.

### Added
- **`galdor trial <suite.yaml>`** — run a declarative evaluation suite, no
  Go required. The YAML maps to `pkg/eval`: a dataset of cases, a subject
  (an *agent block* — provider + model + optional tools/system), and
  scorers (`contains`, `exact`, `regex`, `llm_judge`). Exit code is a CI
  gate (0 pass / 1 below `min_pass` / 2 setup error); `--json` for
  machine-readable output. `examples/trial-suite` ships a complete suite.
- **Declarative config format (ADR-014).** YAML parsed with
  `goccy/go-yaml` into typed structs under strict mode: an unknown key
  fails with its `[line:col]` position, and `version: 1` is required.
  Providers resolve via `providerset` with the API key read from the
  environment (never the file); tools resolve from builtins + MCP servers.
  Custom Go tools/scorers stay library-only.

### Changed
- **`cmd/galdor` is now its own Go module (ADR-014 D3).** A config-driven
  verb must construct providers via `providerset`, which pulls the adapter
  stack and the AWS SDK. Splitting the binary into its own module keeps the
  **core module (`pkg/`, `internal/`) provider-free and dependency-light —
  unchanged at 6 direct**; library consumers of `pkg/...` never pull
  `providerset`, the adapters, or the AWS SDK. Still one binary;
  `go install github.com/YasserCR/galdor/cmd/galdor@v0.11.0` is unchanged.

### Build
- New module `github.com/YasserCR/galdor/cmd/galdor` (11 modules total),
  tagged last in the release so its `go.sum` seals against the published
  siblings. New dependency `github.com/goccy/go-yaml` (MIT, pure-Go) lives
  in the CLI module only. Submodule `require` pins bumped v0.10.0 →
  v0.11.0.

## [0.10.0] - 2026-06-11

CLI surface: two verbs whose engines already shipped now have a command, and
the binary stops advertising verbs it never intends to build. No library
behavior changes for existing code; the one new library symbol is a client
transport. Green under `go test -race`, `go vet`, golangci-lint v2.12.2 and
gosec across the root module.

### Added
- **`galdor mcp`** — serve galdor's builtin tools over MCP, or inspect any
  MCP server. `galdor mcp serve` exposes `time`/`math` (and, guard-gated,
  `file_read` via `--base-dir` and `http_get` via `--allow-host`/
  `--allow-any-host`) over stdio (default) or Streamable HTTP (`--http`).
  `galdor mcp ls` / `galdor mcp call` are a debugging client against either
  an `http(s)://` URL or a subprocess after `--`.
- **`galdor weave <run-id>`** — render a recorded run's graph topology to
  SVG (`-o`), dump it as JSON (`--format json`), or validate it
  (`--check`: dangling edges, unknown entry, unreachable nodes). Reads the
  same `graph.Spec` the dashboard renders, captured per run in the trace
  store.
- **`mcp.NewStreamableHTTPClientTransport(url)`** — a client-side
  Streamable HTTP transport, the dialer counterpart of
  `NewStreamableHTTPTransport`. Request/response only (initialize / list /
  call); echoes the server-minted `Mcp-Session-Id`. This is what lets the
  CLI client (and any caller) reach a Streamable HTTP server.

### Changed
- **CLI surface pruned (ADR-013).** `serve`, `recast` and `forge` were
  removed from the binary — `serve`/`forge` contradict explicit non-goals
  (no `pkg/serve`; no scaffolding — templates rot against a moving pre-v1.0
  API, the pattern create-react-app/Buffalo/LangChain retired), and
  `recast` is subsumed by `scry replay`. The supported scaffolding path is
  Go's own `gonew` over the compiler-verified `examples/`. The usage text
  now separates implemented Commands from Planned verbs, so the binary
  never advertises capability it lacks.

### Build
- Submodule `require` pins bumped from v0.9.1 to v0.10.0 across providers/*,
  memory/*, providerset and examples.

## [0.9.1] - 2026-06-11

Post-audit follow-up: three provider fixes whose earlier cleanup only landed
in one of several places, plus the regression tests and documentation the
audit verification flagged as missing. Each code fix has a permanent
regression test (red→green where the bug was reproducible). Green under
`go test -race`, `go vet` and the build across the root module and the nine
submodules.

### Fixed
- **Google streaming surfaces in-stream errors.** A `{"error":{...}}` frame
  delivered mid-stream by Gemini is now returned as a classified
  `*provider.APIError` instead of being swallowed and ending the stream with
  a synthesized, apparently-successful `MessageStop` (the same fix OpenAI
  already had). A prompt blocked by the safety filter mid-stream now errors
  too, matching the non-streaming `Generate` path.
- **Bedrock echoes signed reasoning blocks.** A signed extended-thinking
  block is now re-emitted on the assistant turn that carries `tool_use`, as
  the Converse API requires ("include the text and its signature
  unmodified") — so a `Reasoning.Enabled` + tools loop can complete a
  round-trip on Claude-on-Bedrock. Unsigned reasoning is still skipped.
- **Embedder dimension auto-detect is race-free.** The OpenAI and Google
  `Embedder.dim` cache now uses an atomic, matching the HTTP embedder; the
  types are documented "safe for concurrent use" and a concurrent
  `Embed`/`Dimensions` no longer races (confirmed under `-race`).

### Changed
- Behavior change implied by the two streaming/Bedrock fixes above: Google
  `Stream` now returns an error (rather than a truncated success) on an
  in-stream error frame or safety block, and Bedrock requests now include the
  signed reasoning block on assistant turns carrying `tool_use`. Both are
  bug fixes; call them out if you depended on the prior (silent) behavior.

### Tests
- Added the regression tests the audit verification found missing: duplicate
  in-flight JSON-RPC id rejection over Streamable HTTP MCP (returns 409),
  qdrant rejecting reserved `__`-prefixed metadata keys, and the SQLite
  vector-store dimension-mismatch error (the in-memory store already had its
  own).

### Docs
- Example READMEs added for `examples/eval-suite` and `examples/memory-rag`
  (closing the "each example has its own README" claim). Corrected stale API
  references (`providerset.md`, `from-eino.md`, `graph-interrupt`), a dead
  link in `queue-worker.md`, the `ARCHITECTURE.md` adapter/store/e2e entries,
  delivered-but-unchecked `ROADMAP.md` items, the dependency count in
  `README.md`, and the CLI usage footer.

### Build
- Submodule `require` pins bumped from v0.9.0 to v0.9.1 across providers/*,
  memory/*, providerset and examples.

## [0.9.0] - 2026-06-10

Pre-alpha audit cleanup: the ~45 low-severity findings from the audit's §4,
swept across every subsystem. Each fix landed with a regression test
(reproduced failing first where a failure was reproducible; the rest are
documented as defensive hardening). A handful change observable behavior —
they're called out below. Green under `go test -race`, golangci-lint v2.12.2,
gosec and govulncheck across all nine modules (go1.25.11).

### Changed
- **Retry hardening.** A negative/zero `Multiplier` is clamped to the
  fixed-interval floor (1.0) and the exponential schedule saturates at
  `MaxDelay`, so a hostile config can no longer produce a negative delay that
  spins in a hot retry loop. Provider adapters now also parse an HTTP-date
  `Retry-After` (not just a seconds count).
- **Anthropic `max_tokens` default raised** from 1024 to
  `anthropic.DefaultMaxTokens` (4096) — the old default truncated long
  answers. `provider.Request.MaxTokens` documents the cross-provider
  asymmetry. Google's embedder now sends the API key in a header (not the
  `?key=` query string, which leaks into proxy logs), and model ids are
  path-escaped.
- **Latency percentiles use true nearest-rank.** `scry stats` P95/P99 of a
  small sample returned the *minimum* before; they now report the correct
  high-percentile value.
- **`eval.Config.MinPass` is now `*float64`.** nil means the 1.0 default;
  `eval.Threshold(0)` expresses report-only (accept any pass rate), which a
  bare `0` could not. `RunAndExit` rejects out-of-range thresholds.
- **MCP version negotiation.** The server echoes a client's requested
  protocol version only when it supports it (otherwise it answers with its
  own), and rejects requests whose `jsonrpc` field isn't `"2.0"`. MCP client
  calls get a default 30s timeout (`WithCallTimeout`) when the caller's
  context has no deadline.
- **`galdor scry tail` honors Ctrl-C** (SIGINT is now wired into the command
  context) and reports the final post-redirect URL from `http_get`.

### Fixed
- **Data races**: the MCP client's server-info fields, and the
  instrumented-stream span teardown (which also no longer leaks an open span
  when a stream is abandoned — the span ends on context cancellation).
- **Providers**: redacted-thinking blocks round-trip (Anthropic), a tool_use
  with empty args emits `input: {}`, `cache_control` lands on the last block
  (including tool calls), Bedrock stream events carry the model, swallowed
  decode errors in the OpenAI/Bedrock response paths now surface, the
  Bedrock embedder omits the v2-only fields for Titan v1 and splits Cohere
  batches at 96, and the SSE readers honor a per-call context mid-read.
- **Streaming strip**: a `<think>` tag with attributes split across deltas is
  no longer leaked, and the terminal `MessageStop.Message` is stripped too.
- **Memory**: `memory/sqlite` pins its `:memory:` connection (data no longer
  vanishes if the pool drops it) and aliases the FTS rowid to a stable
  `INTEGER PRIMARY KEY`; `InMemoryStore` deep-copies chunks so it can't alias
  caller slices/maps; pgvector's `parseVector` errors on malformed input
  instead of silently returning nil; the `Retriever` errors when the embedder
  returns ≠1 vectors; the `Window` re-enforces its token cap after
  summarization.
- **Graph**: a run-level timeout error is wrapped with the elapsed time per
  its doc; `MemoryCheckpointer` can cap retained history
  (`NewMemoryCheckpointerWithLimit`).
- **eval**: the LLM-judge fallback no longer reads digits embedded in words
  (a model name like `gpt4`), duplicate scorers can be disambiguated with
  `eval.Named`, and `testprovider` deep-copies scripted responses.
- **A2A**: an updated `SessionID` / `Metadata` sent with a continuing message
  is applied (merged) instead of dropped; the package doc example compiles.
- **Observability**: `TraceHooks` context keys are distinct types (two
  pointers to a zero-size struct could share an address); `scry stats`
  decodes `\uXXXX` escapes when grouping; the dashboard renders templates to
  a buffer so a mid-render error is a clean 500, not a truncated 200.

## [0.8.0] - 2026-06-10

`memory.Store` contract consistency and correctness fixes from the pre-alpha
audit: the three vector backends (sqlite, pgvector, qdrant) and the in-memory
store now agree on IDs, required embeddings, score scales and metadata
round-tripping, and the observability/replay path no longer drops or
mis-attributes spans. Each fix landed with a regression test (reproduced
failing first, then green). Some previously-silent failures are now loud, so a
few inputs that used to be accepted-but-wrong now error. Green under `go test
-race`, golangci-lint v2.12.2, govulncheck and gosec across all nine modules.

### Changed
- **Retry gives up instead of retrying early.** When a server's `Retry-After`
  exceeds the configured `MaxDelay`, the retry wrapper now stops (returns the
  exhausted error) rather than truncating the wait to `MaxDelay` — truncating
  would retry inside the server's window and earn another 429. A `Retry-After`
  within `MaxDelay` is still honored as a floor (jittered upward only, never
  below the server value).
- **Cross-backend memory semantics are unified and documented.** The README now
  carries a semantics matrix (ID handling, required embeddings, lexical-query
  support, score scale) and the "swap backends by changing one constructor"
  claim is scoped to what actually holds. Vector backends consistently require
  a query embedding and drop anti-correlated (negative-cosine) results.
- **Agents surface the iteration cap.** Hitting the max-iteration limit
  mid-cycle now sets `State.StoppedAtIterationCap` and returns the
  `ErrMaxIterations` sentinel instead of silently returning a partial run.

### Fixed
- **qdrant**: the user's free-form `Chunk.ID` is round-tripped via a reserved
  `__chunk_id` payload key (previously lost on retrieval); caller metadata using
  the reserved `__` prefix is now rejected instead of silently colliding with
  system keys; searches request `with_vector` so retrieved chunks carry their
  embedding.
- **sqlite memory**: lexical (BM25) metadata filtering is pushed into SQL
  instead of being applied after the fact, and JSON metadata paths are quoted
  so keys with special characters no longer break the query; cosine on a
  dimension mismatch returns an error rather than a meaningless score.
- **pgvector**: an HNSW cosine index is created on the embedding column, so
  nearest-neighbor search is no longer a sequential scan over the table.
- **embedders**: the cached embedding dimension is read/written atomically
  (data race under concurrent first calls), and a nil/empty vector in a
  provider response is rejected rather than cached as a zero-dim embedding.
- **observability / replay**: the span tail cursor advances on rowid, so a
  late-ingested span with a low start time is no longer skipped; span ingest
  is idempotent (`INSERT OR IGNORE`); `Shutdown` waits for in-flight async
  exports (`WaitGroup`); per-run span lookup picks the latest trace; fully
  streaming runs are now replayable (`LoadFromStore` includes provider-stream
  spans and skips errored calls); the in-memory trace store caps the pool to a
  single connection so `:memory:` rows stay visible across queries.
- **providers**: Google reports a safety-block as an error instead of an empty
  response and appends (rather than overwrites) multiple system messages;
  Bedrock honors `ToolChoice: none` by stripping tool calls from the response.
- **thinking strip**: trailing whitespace after a stripped `<thinking>` block is
  preserved rather than eaten, so adjacent text isn't joined.

## [0.7.0] - 2026-06-10

Network-surface hardening and cross-provider contract fixes from the
pre-alpha audit: 23 findings across the A2A and MCP servers, the dashboard,
checkpoints, the builtin tools, and the provider adapters. Each fix landed
with a regression test (reproduced failing first, then green). The changes
tighten input handling on the unauthenticated network surfaces and make
several silent failures loud, so some previously-accepted-but-wrong inputs
now error. Green under `go test -race`, golangci-lint v2.12.2, govulncheck
and gosec across all nine modules.

### Security
- **A2A server resource bounds.** The JSON-RPC body is capped (4 MiB), the
  in-memory task store is bounded (cap + oldest-terminal eviction) and
  client task IDs are length-limited, so an unauthenticated peer can no
  longer drive unbounded memory growth.
- **MCP HTTP transports validate Origin** (DNS-rebinding protection per the
  spec) and **require the assigned session id** on the Streamable HTTP and
  SSE transports — an omitted/foreign id no longer bypasses the session.
- **Dashboard rejects DNS-rebinding.** `galdor ui` now refuses requests
  whose `Host` is a domain name (only localhost / IP literals / an opt-in
  `AllowedHosts` are served), closing the rebinding path to captured
  prompts. The stdio MCP transport no longer recurses (and overflows the
  stack) on a stream of blank lines.
- **Builtin tool sandboxes.** `file_read` now resolves symlinks and
  re-checks containment, so an intermediate symlink inside `BaseDir` can't
  escape it; `http_get` re-validates scheme + host on every redirect hop
  (SSRF) and normalizes allowlist entries that carry a port.

### Fixed
- **MCP client reply correlation (H15).** A server-initiated request
  (ping / sampling / roots) sharing an id with an in-flight client call was
  delivered as that call's reply, yielding a silent zero-value result.
  Frames carrying a method are no longer routed to pending callers.
- **A2A polling and lifecycle.** `tasks/get` no longer blocks for the whole
  duration of a running handler (it returns the live "working" snapshot),
  and `tasks/send` against a task in a terminal state is rejected
  (`-32002`) instead of silently re-opening it.
- **MCP server robustness.** A failed listener bind is now surfaced by
  `Serve` (was a silent nil), in-flight dispatches are bounded by a
  semaphore, and the Streamable HTTP transport rejects duplicate in-flight
  request ids instead of clobbering the pending slot.
- **Checkpoints fail loudly.** `MemoryCheckpointer.Save` now returns an
  error when the state can't be faithfully deep-copied — a gob round-trip
  silently drops unexported/func/channel fields and aliases
  non-serializable types — instead of corrupting the checkpoint. Implement
  `graph.Cloner` for such states.
- **Capability validation is enforced (M7).** `Capabilities.ValidateRequest`
  is now called by every adapter's `Generate`/`Stream`, so a request asking
  for a feature the provider doesn't support (e.g. structured output on
  Anthropic) returns `ErrUnsupported` instead of being silently ignored.
- **Cross-provider consistency.** Google now sends *all* system messages
  (not just the last); OpenAI maps reasoning requests to
  `max_completion_tokens` and drops `temperature`/`top_p` for o-series
  models; OpenAI synthesizes a stable tool-call id when the backend omits
  one (so the call isn't dropped); and OpenAI surfaces an in-stream
  `error` frame instead of ending the stream as if it succeeded.

### Build
- Submodule `require` pins bumped from v0.6.2 to v0.7.0 across providers/*,
  memory/*, providerset and examples. No go.sum churn.

## [0.6.2] - 2026-06-10

A follow-up to v0.6.1 that softens the read-side database handling for the
live-watch commands. No API changes.

### Changed
- **`galdor ui` and `scry tail` no longer error on a missing database.**
  v0.6.1 made every read command fail when `--db` didn't exist (to catch a
  mistyped path), but the live-watch commands may legitimately start before
  the writing process has created the store. They now create a missing
  database (and its parent directory) and watch it fill up, printing a
  one-line notice so a typo is still visible. The one-shot inspect commands
  (`scry list` / `show` / `stats` / `replay`) keep the strict
  "database does not exist" error from v0.6.1.

### Build
- Submodule `require` pins bumped from v0.6.1 to v0.6.2 across providers/*,
  memory/*, providerset and examples. No go.sum churn.

## [0.6.1] - 2026-06-10

Audit-driven correctness, reliability and security fixes. All changes are
backward-compatible — no API removals, no behavior changes for code that
wasn't hitting the bugs. Green under `go test -race`, golangci-lint v2.12.2,
govulncheck and gosec across all nine modules.

### Fixed
- **The default HTTP client no longer kills long streams.** It set a 60s
  `http.Client.Timeout`, which bounds the entire exchange *including the
  response-body read*, so any SSE stream — or an extended-thinking
  generation — past 60s was aborted mid-flight. Anthropic, OpenAI, Google
  and `providerset` now bound only the connection and time-to-first-byte
  (`Transport.ResponseHeaderTimeout`), leaving the body's lifetime to the
  request context.
- **Bedrock streaming reasoning.** `Stream` built its `ConverseStreamInput`
  from a five-field subset that dropped `AdditionalModelRequestFields`, so a
  streamed request with `Reasoning.Enabled` silently ran with no thinking
  (while still nulling temperature/top_p and inflating max_tokens). The
  streaming path now carries the full request; confirmed end-to-end against
  Bedrock.
- **`provider.CollectStream` preserves reasoning.** The canonical
  stream→response bridge read only stop-reason/usage/model from the terminal
  event and dropped the thinking part; it now keeps it (with signature).
- **Anthropic thinking round-trip.** A signed thinking block is now echoed
  back on the assistant turn that carries `tool_use`, so a Reasoning+tools
  loop can complete (the API rejects the follow-up otherwise). Unsigned
  reasoning is still skipped.
- **Replay works for tool-using agents.** Tool definitions and tool_choice —
  folded into the v2 replay fingerprint — are now captured on the span
  (`gen_ai.request.tools` / `gen_ai.request.tool_choice`), so a recorded
  fixture for a ReAct agent matches on replay instead of failing with
  `ErrPromptMismatch`.
- **`jsonschema` no longer crashes on recursive embedded types.** An
  exported struct that (transitively) embeds itself overflowed the stack
  (an unrecoverable process abort); it now returns the documented
  recursive-type error.
- **Graph SVG rendering no longer hangs on cyclic graphs.** `RenderSVG`/
  `Inspect` looped forever on a static cycle — and ReAct and Plan-Execute
  graphs are cyclic — because the layout BFS relaxed depth without bound;
  it is now bounded by the node count.
- **Conditional-edge router panics are contained.** A panic in a router
  escaped the synchronous `Invoke` path and crashed the process; it is now
  recovered into a `*PanicError`, matching node-body and `Stream` behavior.
- **The CLI honors flags placed after the run-id.** `scry show <run-id>
  --db PATH` and `scry replay <run-id> -o FILE` (the documented shapes)
  silently ignored the trailing flags — reading the wrong database — because
  stdlib `flag` stops at the first positional; they are now re-parsed.
- **Read-side commands don't fabricate empty databases.** A mistyped `--db`
  no longer silently creates an empty store and reports "no runs" — it fails
  with a clear "database does not exist" error (`store.OpenExisting`). The
  SQLite exporter now creates the parent directory of the default
  `~/.galdor/traces.db` on first write instead of failing on a fresh machine.

### Docs
- README install/status updated to the current release.
- `docs/ops.md`: corrected the CGO claim — galdor uses `modernc.org/sqlite`
  (pure Go, ADR-009), so `CGO_ENABLED=0` is the correct, recommended static
  build, not `CGO_ENABLED=1`.
- `docs/patterns/streaming.md`: fixed the graph-streaming snippet to the
  real API (`StreamWith`, `Event.Type`, `EventNodeStart` / `EventRunEnd`).

### Build
- Submodule `require` pins bumped from v0.6.0 to v0.6.1 across providers/*,
  memory/*, providerset and examples. No go.sum churn — the local
  `replace => ../..` directives keep workspace builds resolving from source.

## [0.6.0] - 2026-06-08

Model reasoning capture across every provider, plus a rustic in-SQLite vector
search for test environments. Additive and non-breaking — with no opt-in, every
provider behaves exactly as before. Green under `go test -race`, golangci-lint
v2.12.2, govulncheck and gosec across all nine modules.

### Added
- **Reasoning capture** (opt-in): a new `schema.ContentTypeThinking` content
  part — with a `ThinkingPart` helper and a `Signature` field — carries model
  chain-of-thought as a first-class, non-text part. `Message.Text()` skips it,
  so every downstream consumer that reads the text is unaffected.
  - `provider.ExtractThinkingBlocks` preserves inline `<think>` reasoning as a
    thinking part instead of discarding it like `StripThinkingBlocks` (which is
    unchanged).
  - `provider.Request.Reasoning` (`ReasoningConfig`: `Enabled` / `Budget` /
    `Effort`) turns on a model's native reasoning per call, advertised via
    `Capabilities.Reasoning` and validated by `ValidateRequest`.
  - All four providers surface native reasoning, in both `Generate` and
    streaming: Gemini thought summaries, Anthropic extended thinking (with
    signature), OpenAI / DeepSeek `reasoning_content`, and Bedrock
    `reasoningContent`. The request path now tolerates thinking parts fed back
    on a later turn.
  - `observability.WithCaptureReasoning` records reasoning under a dedicated
    `gen_ai.reasoning` span attribute, independent of `WithCaptureContent`;
    `gen_ai.completion` stays clean. The `scry` dashboard renders reasoning in
    the span detail and steps views.
- **In-SQLite vector search** (`memory/sqlite`): a rustic, brute-force vector
  search intended for test environments — exercise the RAG stack without an
  external vector store.

### Build
- Pin submodule `require` directives to root `v0.6.0` across the workspace
  (`providers/*`, `memory/*`, `providerset`, `examples`).

## [0.5.0] - 2026-06-03

Bedrock embeddings, per-span labels for observability, and a Go toolchain
floor bump for the patched standard library. Green under `go test -race`,
golangci-lint v2.12.2, govulncheck and gosec across all nine modules.

### Added
- **Bedrock embeddings** (`providers/bedrock`): `NewEmbedder` calls Amazon
  Bedrock embedding models — Titan (`amazon.titan-embed-text-v2:0`) and
  Cohere (`cohere.embed-*`) — and satisfies `memory.Embedder`, so the full
  RAG stack (`pkg/memory` + `memory/pgvector`) runs natively on AWS with no
  external embedding server. Ships unit tests plus a build-tagged integration
  test.
- **Per-span labels** (`pkg/observability`): `WithSpanLabel(ctx, "...")`
  stamps a `galdor.span.label` attribute on the instrumented provider and
  tool spans, context-scoped exactly like `WithRunID`. The dashboard timeline
  and `scry show` render it next to the span type (e.g. `provider.generate ·
  review code`), so steps are distinguishable straight from the timeline
  without opening each span.

### Changed
- ⚠️ **Minimum Go bumped to 1.25.11** (from 1.25.10) across every module, to
  build against the patched standard library (GO-2026-5037 `crypto/x509`,
  GO-2026-5039 `net/textproto`). Patch-level and drop-in — no API or behavior
  change.

### Build
- Pin submodule `require` directives to root `v0.5.0` across the workspace
  (`providers/*`, `memory/*`, `providerset`, `examples`).

## [0.4.1] - 2026-05-30

Hygiene-only patch. No code change.

### Build
- Pin submodule `require` directives to root `v0.4.1` across the workspace
  (`providers/*`, `memory/*`, `providerset`, `examples`), bumping them from
  the stale `v0.3.1` floor left after the v0.4.0 release. Pure go.mod edit —
  no go.sum churn (the local `replace => ../..` directives keep workspace
  builds resolving from source) and no code change. Minimal version
  selection already resolves the root to the newest required version for any
  consumer that depends on it directly; this keeps the submodules' own floor
  consistent with the release.

## [0.4.0] - 2026-05-30

Pre-launch hardening + dashboard redesign. An adversarial audit of the
surface not covered by v0.3.0 (MCP, A2A, multi-agent council, eval,
replay, web UI/CLI) plus a full redesign of the embedded dashboard.
Every bug fix ships a regression test; the release is green under
`go test -race`, golangci-lint v2.12.2, govulncheck and gosec across all
nine modules.

### Added
- **Embedded dashboard redesign** (`internal/ui`): a light, professional
  "Studio" theme (neutral slate + a single indigo accent) replacing the
  dark/neon look; an **interactive execution graph** whose nodes link to
  their step and show duration/status on hover (`graph.NodeAnnotation` +
  `Spec.RenderSVGAnnotated`); the `/graph` viewer now auto-loads the
  selected run's recorded topology (run dropdown, `?run=<id>`); and a
  richer timeline with a top time axis, gridlines, per-bar durations and
  depth-indented span names.
- **`graph.NodeAnnotation` + `Spec.RenderSVGAnnotated`** (`pkg/graph`):
  additive; `RenderSVG` is unchanged.
- **`council.ErrMaxHopsExceeded`, `council.ErrUnknownHandoffTarget`**: a
  Supervisor/Swarm run that is capped or misrouted now returns a
  detectable sentinel instead of a silent empty result.
- **`replay.ErrNilResponse`**: replaying a recorded nil response returns
  a descriptive error instead of `(nil, nil)`.

### Changed
- ⚠️ **Replay fixture format bumped to v2** (`replay.CurrentFixtureVersion`):
  the request fingerprint now folds in `Tools` and `ToolChoice` (and the
  model), so changing a run's available tools correctly invalidates a
  recorded answer. v1 fixtures are rejected and must be re-recorded.
- ⚠️ **Swarm handoffs are enforced at runtime**: an agent can only hand
  off to a target it declared in `Handoffs`; an undeclared `handoff_to_*`
  is rejected (returned to the model as an error result) instead of
  silently transferring control.
- ⚠️ **A2A client defaults are stricter**: cross-host redirects are
  rejected on card discovery (SSRF) and response bodies are size-capped
  (OOM); override via `WithHTTPClient` if you need the old behavior.

### Fixed
- **MCP** (`pkg/mcp`): a panicking tool no longer crashes the server
  process (recovered into a JSON-RPC error); the StreamableHTTP transport
  correlates replies by JSON-RPC id (no more dropped/cross-talked replies
  under concurrent same-session requests); the client no longer leaks its
  dispatch goroutine on `Close`; inbound messages are size-capped; the
  initialize handshake echoes the client's protocol version.
- **A2A** (`pkg/a2a`): a client-controlled `*Task` is now guarded by a
  per-task mutex (fixes a remote-triggerable data race / crash).
- **Eval** (`pkg/eval`): the shared `*Regex` scorer is compiled under a
  `sync.Once` (fixes a `-race` failure); the LLM-judge score parser no
  longer mis-reads numbers embedded in prose (no false PASS/FAIL); the
  runner honors context cancellation and recovers panicking
  Subjects/Scorers (one bad case errors instead of aborting the batch);
  `ExactMatch` no longer passes on empty expected-vs-actual; duplicate
  scorer names are rejected at setup.
- **Replay** (`pkg/replay`): lenient mode serves same-prompt calls in
  recorded order instead of collapsing to the last; cloned responses are
  deep-copied (nested image bytes / tool-call arguments no longer alias
  the recording).
- **UI/CLI**: `/api/graph/svg` caps request body and node count; the SSE
  endpoint clamps a minimum poll interval; `galdor ui` warns when binding
  to a non-loopback address (no auth); the `scry replay` CLI no longer
  panics on a short fingerprint. Also fixes the graph SVG node-fill bug
  (`fill="%q"` rendered empty).

## [0.3.1] - 2026-05-30

Hygiene-only patch. No code change.

### Build
- Pin submodule `require` directives to root `v0.3.1` across the
  workspace (`providers/*`, `memory/*`, `providerset`, `examples`),
  bumping them from the stale `v0.1.0` floor. Pure go.mod edit — no
  go.sum churn (the local `replace => ../..` directives keep workspace
  builds resolving from source) and no code change. Minimal version
  selection already resolves the root to the newest required version for
  any consumer that depends on it directly; this keeps the submodules'
  own floor consistent with the release instead of trailing at v0.1.0.

## [0.3.0] - 2026-05-30

Correctness & consistency release. Driven by an adversarial audit of the
provider adapters, graph runtime, and memory backends: it closes a class
of "capability lies" (adapters advertising features they didn't wire),
brings the streaming graph path to parity with the synchronous one, and
makes checkpoints, retries, and memory results behave consistently.

No exported function or type was removed or had its signature changed;
the only new public API is additive. Several bug fixes do change
observable behavior — see **Changed** for the ones integrators should
note.

### Added
- **`Runnable[S].StreamWith(ctx, initial, opts)`** (`pkg/graph`): the
  option-taking streaming variant, at parity with `InvokeWith`. It drives
  `Checkpointer`, `Hooks`, run/node `Timeout`, `MaxSteps`, and
  `InterruptBefore` gating, and recovers node panics into an `EventError`
  carrying a `*PanicError` instead of crashing the process. `Stream` now
  routes through it; interrupts surface as a terminal `EventError`
  wrapping `ErrInterrupted`.
- **`graph.Cloner[S]` interface** (`pkg/graph`): lets a state type provide
  a precise deep copy for checkpointing. `MemoryCheckpointer` uses it when
  the state implements it, falling back to a gob round-trip.
- **Bedrock `user_id` metadata**: the Bedrock adapter now forwards
  `Request.Metadata["user_id"]` via Converse `RequestMetadata`, matching
  the Anthropic and OpenAI adapters.

### Changed
- **Honest capabilities** ⚠️: Bedrock now reports `StructuredOutput: false`
  and `PromptCaching: false`, and Google reports `PromptCaching: false` —
  those adapters do not yet wire `Request.ResponseFormat` /
  `schema.CacheControl`, and reporting the features as available silently
  produced free-form text or uncached prompts. `Capabilities.ValidateRequest`
  now also enforces the `CacheControl` vs `PromptCaching` check its doc
  already promised.
- **Retry caps and determinism** ⚠️ (`pkg/provider`): `MaxDelay` is now a
  hard ceiling applied *after* jitter, and it also bounds a server's
  `Retry-After` (a hostile `Retry-After: 86400` no longer sleeps for a
  day). A *negative* `Jitter` disables jitter for deterministic backoff;
  the zero value still means the 0.25 default, so existing callers are
  unchanged.
- **Bedrock honors `Retry-After`** ⚠️: throttling / quota errors now
  surface the HTTP `Retry-After` header on `APIError.RetryAfter`, so the
  retry wrapper uses the server-suggested backoff (parity with the HTTP
  adapters).
- **Consistent vector results** ⚠️: the pgvector and qdrant backends now
  drop anti-correlated (negative-cosine) chunks, matching the sqlite and
  in-memory backends — `Retrieve` returns the same result *set* across all
  four. Verified against live Postgres + pgvector and Qdrant.
- **Immutable checkpoints** ⚠️ (`pkg/graph`): `MemoryCheckpointer.Save`
  now deep-copies state so a later node mutating shared slices or maps can
  no longer corrupt an already-saved checkpoint. The `Checkpointer`
  interface documents this immutability contract.
- **Anthropic stream truncation** ⚠️: the Anthropic adapter now synthesizes
  a terminal `EventMessageStop` (carrying the accumulated usage and model)
  when the connection drops before `message_stop`, matching the other
  three adapters.

### Fixed
- **`schema.ParseJSON[T]`** now tolerates trailing prose after the JSON
  value (e.g. `{"a":1}\n\nHope that helps!`), not only leading prose, as
  the doc already claimed.
- **FTS5 lexical search** (`memory/sqlite`) no longer raises a syntax
  error on queries containing `AND` / `OR` / `NOT` or operator characters;
  each token is quoted as an FTS5 string literal.
- **JSON Schema generation** (`internal/jsonschema`): embedded
  pointer-to-struct fields are now promoted like `encoding/json` does
  (previously they leaked as a spurious property and, under
  `additionalProperties:false`, made the schema reject the model's correct
  output); `map[string]T` now emits the value schema instead of discarding
  it.
- **InMemoryStore ranking** (`pkg/memory`): a vector query no longer mixes
  cosine and lexical scores in one ranked list — chunks without an
  embedding are skipped rather than scored on an incomparable scale.
- **`graph.MergeHooks`** no longer mutates the caller's backing array when
  spread from a slice; `runLoop`'s per-node timeout no longer accumulates
  timers across a long run.
- **Bedrock `ToolChoiceNone`** keeps the tool *definitions* declared (only
  the choice is omitted) instead of dropping the entire tool config, which
  could invalidate a follow-up turn carrying prior `tool_result` blocks.

## [0.2.0] - 2026-05-23

Phase 11 of the roadmap — direct-caller ergonomics, driven by the first
integrator report (Telegram interpreter migration from LangChain).
The non-agent `Provider.Generate` path is now as ergonomic as the
agent loop: typed errors, tolerant JSON parsing, importable test
provider, surfaced retry policy, and a first-class docs page.

### Added
- **Typed error wrappers** (`pkg/provider`): `*RateLimitError`,
  `*AuthError`, `*InvalidRequestError`, `*TransientError`,
  `*ContextLengthError`, `*UnsupportedError`. All embed `*APIError`
  and support `errors.As` for idiomatic Go classification. Adapters
  emit them via `provider.Classify`. Backward compatible with
  existing `errors.Is(err, ErrRateLimited)` and `errors.As(err,
  &apiErr)` patterns via the unwrap chain. See ADR-012.
- **`schema.ParseJSON[T any]`** (`pkg/schema`): tolerant LLM JSON
  parser. Strips Markdown code fences, extracts JSON from
  surrounding prose, returns `*schema.BadOutputError` with capped
  raw input on failure. Stdlib-only; no LLM-driven repair. See
  ADR-011.
- **`schema.BadOutputError`** (`pkg/schema`): non-transport content
  failures, shared by ParseJSON today and JSONOf[T] in Phase 12.
- **`pkg/testprovider`**: scripted in-process `provider.Provider`
  for unit tests. `New`, `Responses`, `JSONResponses`, `Errors`,
  `Name`, `Capabilities` options; `Requests()`, `Reset()`,
  `Remaining()` introspection; goroutine-safe; `ErrScriptExhausted`
  on overrun.
- **`provider.WithDefaultRetry(inner)`**: one-line constructor for
  the common "sensible 429/5xx retry" case.
- **`provider.RetryPolicy`**: Go type alias for `RetryConfig` so
  both names refer to the same struct.
- **`docs/patterns/direct-provider.md`**: end-to-end guide for the
  one-prompt-one-response case with copy-paste skeleton, full typed-
  error catalog, retry composition, observability wiring, and
  testing patterns. Linked from `docs/README.md` ahead of RAG.

### Changed
- All four adapters (`providers/{anthropic,openai,google,bedrock}`)
  now return typed error wrappers via `provider.Classify` at every
  failure boundary. No caller-facing breakage.
- `pkg/provider/doc.go` rewritten with Errors and Retry sections so
  the package's godoc.org / pkg.go.dev landing answers \"how do I
  classify errors\" and \"how do I retry on 429\" without scrolling.

### Acceptance principle hits
- Rejected: a `Retry` field on every adapter's Config struct.
  Composition via decorator stays the canonical pattern.
- Rejected: re-prompt-on-failure inside `ParseJSON[T]`. Failure is
  signal; recovery via LLM call belongs in caller code.

## [0.1.1] - 2026-05-23

### Documentation
- README refreshed: roadmap shipped, explicit call for integrators.
- ROADMAP extended with Post-v1.0 phases (11–14), driven by the first
  integrator report from a real LangChain → galdor migration.

### Build
- Pin `require` directives across submodules to root `v0.1.0` so
  downstream installations resolve cleanly without local `replace`
  directives. Affects `examples/`, `providerset/`, `providers/*`,
  `memory/*`.

## [0.1.0] - 2026-05-19

First tagged release. Delivers Phases 0–10 of the roadmap, including:

- Provider abstraction with Anthropic, OpenAI, Google, and AWS Bedrock
  adapters; streaming, tool-calling normalization.
- Generics-based tool system (`pkg/tool`) and JSON Schema generation
  from Go structs.
- Graph runtime (`pkg/graph`) with conditional edges, checkpointing,
  interrupt/resume, and streaming events.
- Agent helpers: ReAct and Plan-and-Execute (`pkg/agent`).
- OpenTelemetry-native observability with embedded SQLite span store
  and `galdor scry` CLI.
- Self-hosted web UI (`galdor ui`) with run list, span tree, timeline,
  SSE live updates, and graph viewer.
- Memory & RAG: in-memory, SQLite, pgvector, and Qdrant backends;
  short-term `Window` with summarization; provider-backed embedders.
- Multi-agent: Supervisor + Swarm patterns (`pkg/council`); MCP client
  and server; A2A protocol.
- Eval framework with LLM-as-judge, Go scorers, versioned datasets,
  and CI integration.
- Replay engine and time-travel UI for reproducible debugging.
- Production hardening: retry/backoff, timeouts, panic recovery,
  structured logging, goroutine-leak audit, capability validation.

See [ROADMAP.md](ROADMAP.md) for the full surface delivered.

[Unreleased]: https://github.com/YasserCR/galdor/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/YasserCR/galdor/compare/v0.15.1...v1.0.0
[0.15.1]: https://github.com/YasserCR/galdor/compare/v0.15.0...v0.15.1
[0.15.0]: https://github.com/YasserCR/galdor/compare/v0.14.0...v0.15.0
[0.14.0]: https://github.com/YasserCR/galdor/compare/v0.13.0...v0.14.0
[0.13.0]: https://github.com/YasserCR/galdor/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/YasserCR/galdor/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/YasserCR/galdor/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/YasserCR/galdor/compare/v0.9.1...v0.10.0
[0.9.1]: https://github.com/YasserCR/galdor/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/YasserCR/galdor/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/YasserCR/galdor/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/YasserCR/galdor/compare/v0.6.2...v0.7.0
[0.6.2]: https://github.com/YasserCR/galdor/compare/v0.6.1...v0.6.2
[0.6.1]: https://github.com/YasserCR/galdor/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/YasserCR/galdor/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/YasserCR/galdor/compare/v0.4.1...v0.5.0
[0.4.1]: https://github.com/YasserCR/galdor/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/YasserCR/galdor/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/YasserCR/galdor/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/YasserCR/galdor/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/YasserCR/galdor/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/YasserCR/galdor/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/YasserCR/galdor/releases/tag/v0.1.0
