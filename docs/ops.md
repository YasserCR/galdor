# Operations

Running galdor in production. This is a flat list of concrete
concerns; pick what applies.

---

## Building one static binary

```bash
CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o ./myagent ./cmd/myagent
```

`CGO_ENABLED=1` is required by the SQLite-backed memory and
trace stores; without either, `CGO_ENABLED=0` produces a fully
static binary.

To bundle the `galdor` CLI alongside your agent app, ship the
prebuilt CLI (`go install
github.com/YasserCR/galdor/cmd/galdor@latest`) in your container
image. The CLI honors `$GALDOR_DB` and `~/.galdor/traces.db` as
fallback DB paths — set `$GALDOR_DB` to your app's trace path
and `galdor scry tail` "just works" for operators.

---

## The `galdor ui` server

```bash
galdor ui --db ./traces.db
# galdor scry dashboard listening on http://127.0.0.1:7777
```

Flags (from `cmd/galdor/ui.go`):

- `--db PATH` — span store path. Falls back to `$GALDOR_DB`,
  then `~/.galdor/traces.db`.
- `--addr ADDR` — bind address, default `127.0.0.1:7777`
  (loopback). `--addr 0.0.0.0:7777` exposes the dashboard on
  every interface — opt in deliberately.

### When to expose

The dashboard renders prompt and completion bodies recorded
with `WithCaptureContent(true)` — customer input, internal
prompts, things you don't want a casual LAN visitor reading.
Default to loopback; reverse-proxy when you need to share.

### Reverse-proxy setup

The dashboard is regular HTTP plus one SSE endpoint
(`GET /api/stream/runs`). Nginx example:

```nginx
location /galdor/ {
    proxy_pass http://127.0.0.1:7777/;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 1h;
}
```

Put auth (basic, OAuth proxy, mTLS — your call) in front of the
proxy. galdor ships no auth; loopback is the default auth model.

### SSE endpoint

`GET /api/stream/runs?interval=1s` emits `text/event-stream`
frames with new runs. The dashboard uses `EventSource`; a CLI
consumer can use `curl --no-buffer`. Disable buffering on any
proxy in the middle.

---

## SQLite trace store

### File location

Whatever you passed to `observability.NewSQLiteExporter(path)`.
CLI fallback order: `--db` → `$GALDOR_DB` →
`~/.galdor/traces.db`.

### Rotation / retention

galdor doesn't rotate the file. Two strategies:

1. **Bound by age.** Run a periodic
   `DELETE FROM spans WHERE start_time_unix_nano < ?` from a
   sidecar, then `VACUUM`. Schema is in `internal/store/`.
2. **Cycle files.** Close the exporter, `mv traces.db
   traces.db.YYYY-MM-DD`, open a fresh exporter. The dashboard
   takes any path via `--db` so old files stay browsable.

Pick (1) for a single rolling window; (2) for audit
immutability per day / per release.

### Backup

A normal SQLite file. `sqlite3 traces.db ".backup '/path/to/backup.db'"`
snapshots cleanly while the writer is live; for cold backups,
stop the writer first.

### `$GALDOR_DB` and `~/.galdor/traces.db`

Setting `$GALDOR_DB` once in your operator's shell saves typing
`--db PATH` on every command.

---

## Resource sizing

See [docs/benchmarks.md](benchmarks.md) for numbers. galdor's
own overhead is 3-5 orders of magnitude smaller than a real LLM
call. Dominant resources:

- **Disk** for `traces.db` — KB per run × retention.
- **Memory** — small; trace exporter batches in memory, the
  dashboard's working set is a few MB.
- **CPU** — bound by the LLM and your tool code, not galdor.

---

## OTel pipeline

The SQLite exporter is one consumer of OTel spans; compose with
standard OTLP exporters:

```go
sqliteExp, _ := observability.NewSQLiteExporter("./traces.db")
otlpExp,   _ := otlptracegrpc.New(ctx,
    otlptracegrpc.WithEndpoint("otel-collector:4317"),
    otlptracegrpc.WithInsecure())

tp := sdktrace.NewTracerProvider(
    sdktrace.WithBatcher(sqliteExp),
    sdktrace.WithBatcher(otlpExp),
)
```

Common targets:

- **Datadog** — DD OTel collector or `datadog-agent` with OTLP
  ingest enabled.
- **Honeycomb** — direct OTLP/HTTPS to `api.honeycomb.io`.
- **Grafana Tempo / Jaeger** — direct OTLP to your collector.

Spans follow the GenAI semantic conventions; any consumer that
understands those attributes (model, tokens, finish reason, tool
name) renders them out of the box. Run SQLite only, OTLP only,
or both.

---

## Failure handling

### Provider retries

```go
p = provider.Retry(p, provider.RetryConfig{
    MaxAttempts: 5,
    OnRetry: func(n int, d time.Duration, err error) {
        slog.Warn("retrying", "attempt", n, "delay", d, "err", err)
    },
})
```

Honors `Retry-After` on `ErrRateLimited`, exponential backoff
with jitter for transient errors, and a deny list for
non-retryable failures (auth, invalid request, capability
mismatch). `MaxAttempts` is total tries; `1` disables retry,
default is 3.

### Per-run / per-node timeouts

```go
final, err := r.InvokeWith(ctx, state, graph.RunOptions[State]{
    Timeout:     2 * time.Minute,
    NodeTimeout: 30 * time.Second,
    Logger:      slog.New(slog.NewJSONHandler(os.Stdout, nil)),
})
```

`Timeout` caps total wall time; `NodeTimeout` caps each node.
Well-behaved nodes respect `ctx` and abort cleanly.

### Panic recovery

Every node, hook, and tool runs under a recover guard. A panic
becomes `graph.PanicError` (`errors.Is(err, graph.ErrPanic)`).
The stack is captured (4 KiB) so post-mortems are possible.

### Agent config validation

`agent.Config.validate` runs from every `agent.NewReAct` /
`agent.Run` and rejects construction-time mistakes: missing
`Provider`, missing `Model`, `Tools` set against a provider that
doesn't advertise tool calling, `ForceToolUse` without `Tools`.

---

## Secrets handling

### Provider API keys

```go
p, _ := anthropic.New(anthropic.Config{
    APIKey: os.Getenv("ANTHROPIC_API_KEY"),
})
```

Never literal a key into source. Inject from your secret store
(K8s secrets, Doppler, Vault) as environment.

### File-read confinement

`builtins.NewFileReadTool` takes a `BaseDir`. Reads outside it
fail with `ErrPathEscape`. Set it to the narrowest directory
the agent needs:

```go
ft, _ := builtins.NewFileReadTool(builtins.FileReadOptions{
    BaseDir:  "/var/lib/myagent/docs",
    MaxBytes: 1 << 20,
})
```

Symlinks are rejected by default; `FollowSymlinks: true` only
if you understand the escape implications.

### HTTP-get allowlist

`builtins.NewHTTPGetTool` takes `AllowedHosts`. Empty means no
host check — rarely what you want in production. Specify the
hosts the agent may reach; plain `http://` is rejected unless
`AllowHTTP: true`.

```go
ht, _ := builtins.NewHTTPGetTool(builtins.HTTPGetOptions{
    AllowedHosts: []string{"api.internal.example.com"},
})
```

---

## Security

See [docs/security.md](security.md) for the full posture
(automated tooling, accepted findings, OWASP LLM Top 10
self-assessment, reporting a vulnerability).

CI minimum:

```bash
go install golang.org/x/vuln/cmd/govulncheck@latest
go install github.com/securego/gosec/v2/cmd/gosec@latest

govulncheck ./...
gosec ./...
```

Both run on every PR upstream. Wire the same two into your own
CI before shipping a custom build.

---

## Logging

galdor uses `slog.Logger` for runtime warnings (recovered
panics, hook failures, deadline fires). Pass one via
`RunOptions.Logger`:

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
r.InvokeWith(ctx, state, graph.RunOptions[State]{Logger: logger})
```

A nil `Logger` silently swallows these events — the happy path
never logs. `provider.Retry.OnRetry` is a callback; wire it to
the same logger so retries and node-level events end up in one
stream.

---

## Quick CI checklist

- [ ] `CGO_ENABLED=1 go build` produces a deployable binary.
- [ ] `govulncheck ./...` clean.
- [ ] `gosec ./...` clean (or findings annotated).
- [ ] Trace exporter pointed at a stable path or OTLP collector.
- [ ] `--addr` is loopback in dev, reverse-proxied in prod.
- [ ] Provider API keys injected from secrets, not literals.
- [ ] `BaseDir` set on every file-reading tool.
- [ ] `AllowedHosts` set on every HTTP-fetching tool.
- [ ] `Timeout` + `NodeTimeout` set on long-running `InvokeWith`.
- [ ] Replay fixtures for critical paths committed
      ([patterns/replay-tests](patterns/replay-tests.md)).
