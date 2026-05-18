# examples/scry-store

End-to-end demo of galdor's embedded observability backend: a ReAct
agent run is instrumented, the spans are persisted to a SQLite
database, and `galdor scry` is used to inspect the resulting trace
tree. No external service, no daemon — the binary itself is the
observability backend.

## Run

```bash
go run ./examples/scry-store
```

The program prints the agent's reply, the path of the SQLite file
where its trace was stored, and the commands to inspect it:

```
agent reply: the answer is 5

traces stored in: /tmp/galdor-scry-demo-XXXXXXXXX/traces.db
explore with:
  galdor scry list --db /tmp/galdor-scry-demo-XXXXXXXXX/traces.db
  galdor scry show --db /tmp/galdor-scry-demo-XXXXXXXXX/traces.db demo-run-1
```

Build the CLI and explore:

```bash
go build -o /tmp/galdor ./cmd/galdor
DB=/tmp/galdor-scry-demo-XXXXXXXXX/traces.db
/tmp/galdor scry show --db "$DB" demo-run-1
```

Output:

```
run demo-run-1 — 7 spans
└── galdor.graph.run  2.3ms  ·
    ├── galdor.graph.node  20.0µs  ·  (node=model)
    │   └── galdor.provider.generate  7.0µs  ·  (provider=scripted in=30 out=10)
    ├── galdor.graph.node  1.8ms  ·  (node=tools)
    │   └── galdor.tool.execute  124.9µs  ·  (tool=math)
    └── galdor.graph.node  14.0µs  ·  (node=model)
        └── galdor.provider.generate  4.1µs  ·  (provider=scripted in=50 out=8)
```

## What it shows

- **`observability.NewSQLiteExporter(path)`** is a regular OTel
  `sdktrace.SpanExporter`. Drop it into
  `sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))` wherever
  you'd put `stdouttrace` or an OTLP exporter.
- **Same DB, same binary.** The exporter writes; the CLI reads.
  Phase 5's Web UI will read from the same file too.
- **Span tree fidelity.** Provider and tool spans nest under the
  node span that triggered them because the graph runtime's
  hooks return a child-bearing `context.Context` from
  `BeforeNode` — see ADR-008 for the wiring.
- **Status, duration and the most useful attributes show up in
  the tree.** `galdor scry show --format json <run-id>` returns
  every attribute on every span for richer programmatic
  consumption.

## Try the JSON output

```bash
/tmp/galdor scry list --db "$DB" --format json | jq .
/tmp/galdor scry show --db "$DB" --format json demo-run-1 | jq '.[].name'
```

## What's next

Phase 4 session C will add derived metrics (latency p50/p95/p99,
token totals, per-provider cost) on top of the same store, and a
`galdor scry tail` follow mode. Phase 5 wires a Web UI to the same
file so the trace tree shows up in a browser instead of (or
alongside) the terminal.
