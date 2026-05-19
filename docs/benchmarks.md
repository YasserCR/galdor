# Benchmarks

Microbenchmarks for galdor's hot paths. These exist to answer two
questions:

1. **Is galdor's own overhead small enough to be irrelevant next to
   a real LLM call?** (Spoiler: yes by 3-5 orders of magnitude.)
2. **How do I size a deployment?** Each benchmark documents what
   the number means in practice, so you can pick batch sizes,
   memory limits, and concurrency caps with real numbers instead
   of guesses.

## Running them

```bash
# Everything, with allocation accounting:
go test -run='^$' -bench=. -benchmem ./...

# A single package:
go test -run='^$' -bench=. -benchmem ./pkg/graph/

# Longer runs for more stable numbers (recommended for any
# decision-grade comparison):
go test -run='^$' -bench=. -benchmem -benchtime=10s ./pkg/graph/
```

The `^$` empty regex on `-run` keeps the regular test suite from
firing during the benchmark pass.

## Caveats before you read numbers

* **Hardware matters.** The numbers below were measured on Apple
  Silicon (M2-class) running darwin/amd64 under Rosetta. Linux on
  bare-metal x86 will be 1.5-3× faster for syscall-heavy paths
  (SQLite inserts, especially); pure-Go work (graph dispatch,
  cosine math) is roughly the same.
* **Benchmark numbers are floors, not ceilings.** They run with
  no LLM, no real tools, no concurrent load. Add ~10% for
  realistic scheduling pressure.
* **The single source of truth is the `_bench_test.go` files.**
  When you see a surprising number here, go read the benchmark
  body — it tells you exactly what was measured.

---

## Graph runtime — `pkg/graph`

How much does galdor's runtime add per node, on top of whatever
the node body does itself?

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkInvoke_SingleNode` | 391 | 0 | 0 |
| `BenchmarkInvoke_TenNodes` | 2,415 | 0 | 0 |
| `BenchmarkInvoke_ConditionalEdge` | 418 | 0 | 0 |

**Reading this**: ~240 ns per node-to-node transition, **zero
allocations** in the happy path. The conditional-edge variant adds
~27 ns vs. the static edge — that's the cost of one extra function
call. The runtime is allocation-free, so a long-running agent
doesn't add GC pressure.

**For sizing**: 240 ns/transition × 100 nodes per run × 10,000
runs per minute = 240 ms of CPU per minute of wall clock. Galdor's
own dispatch is negligible at any realistic agent throughput.

---

## Observability instrumentation — `pkg/observability`

What does wrapping a Provider with `InstrumentProvider` cost?

| Benchmark | ns/op | B/op | allocs/op | What it measures |
|---|---:|---:|---:|---|
| `BenchmarkRaw_Generate` | 7.4 | 0 | 0 | Baseline: a noop Provider with no instrumentation. |
| `BenchmarkInstrumented_Generate` | 8,764 | 6,214 | 14 | Wrapped Provider, capture content **off** (default). |
| `BenchmarkInstrumented_GenerateWithCapture` | 8,359 | 8,328 | 26 | Wrapped Provider, capture content **on** — prompts + completions recorded. |

**Reading this**: ~8.7 µs per Generate call goes to OTel span
creation + attribute setting. Enabling content capture costs ~2 KB
extra per call (the JSON-encoded prompt + completion) and ~12
extra allocs.

**Compared to a real LLM call** (500 ms - 30 s typical for chat
completions), 8.7 µs is **0.0017 % - 0.001 % overhead**. You will
not measure this in production. The thing you'll notice is the
disk space the captured content takes — that's why the flag is
opt-in.

---

## SQLite trace store — `internal/store`

How fast does the embedded span store ingest and serve traces?

| Benchmark | ns/op | per-span | B/op | allocs/op | What it measures |
|---|---:|---:|---:|---:|---|
| `BenchmarkInsertSpans_1` | 100,067 | 100 µs | 2,651 | 49 | Single-span insert (one transaction per span). |
| `BenchmarkInsertSpans_100` | 2,403,196 | 24 µs | 200,575 | 3,614 | Batched insert (100 spans per transaction). |
| `BenchmarkSpansForRun_1k` | 5,674,166 | 5.7 µs | 2,192,643 | 50,033 | Read 1,000 spans for a single run. |

**Reading this**: batched inserts are ~4× faster per span than
single-row ones (24 µs vs 100 µs). The OTel batch exporter does
this automatically; you don't need to tune anything to get the
batched path. Read throughput is ~175k spans/sec — the dashboard's
"steps view" of a 1,000-span run renders in ~6 ms.

**For sizing**: a single galdor process can ingest ~42,000
spans/sec batched. A typical agent run is 5-50 spans. That's
~840-8,400 concurrent agent runs per process **just from the
store's perspective** — the LLM is always the actual bottleneck.

---

## Memory retrieval — `pkg/memory`

How fast does the in-process `InMemoryStore` rank chunks by
cosine similarity, and at what corpus size should you reach for
`memory/sqlite`, `memory/pgvector` or `memory/qdrant` instead?

| Benchmark | ns/op | total time | B/op | allocs/op |
|---|---:|---:|---:|---:|
| `BenchmarkRetrieve_Vector_100` | 36,945 | 37 µs | 9,896 | 4 |
| `BenchmarkRetrieve_Vector_1k` | 513,410 | 513 µs | 98,473 | 4 |
| `BenchmarkRetrieve_Vector_10k` | 6,970,087 | 7.0 ms | 966,824 | 4 |
| `BenchmarkHashingEmbedder` | 7,515 | 7.5 µs | 1,704 | 20 |

**Reading this**: the in-memory store scales linearly with corpus
size (as expected — it's a brute-force scan). At 1k chunks the
retrieval is sub-millisecond; at 10k it's 7 ms; **above 10k
chunks**, switch to `memory/pgvector` (Postgres + pgvector
extension does HNSW indexing) or `memory/qdrant` (dedicated
vector DB) for sub-linear lookup.

The `HashingEmbedder` is the offline embedder shipped for tests
and examples — 7.5 µs per text. Real provider-backed embedders
(OpenAI `text-embedding-3-small`, Google `text-embedding-004`)
are network-bound at 50-200 ms per call; consider batching them
if you ingest documents at scale.

---

## Concurrent tool dispatch — `pkg/tool`

What does `ExecuteCalls` cost when an LLM returns multiple tool
calls in a single turn (the parallel fan-out case)?

| Benchmark | ns/op | per-call | B/op | allocs/op |
|---|---:|---:|---:|---:|
| `BenchmarkExecuteCalls_1` | 5,653 | 5.7 µs | 472 | 10 |
| `BenchmarkExecuteCalls_10` | 35,781 | 3.6 µs | 4,546 | 82 |

**Reading this**: per-call cost actually drops with more parallel
calls — the goroutine spawn amortizes well. 10 tools in parallel
costs ~36 µs total, vs ~57 µs if you ran them sequentially. The
overhead per tool (3.6 µs) is dominated by JSON unmarshalling of
the arguments and JSON marshalling of the result; the actual
dispatch + goroutine + waitgroup adds <1 µs.

**For sizing**: 36 µs for 10 parallel tool calls is irrelevant
unless your tools themselves are sub-millisecond. If your tools
make HTTP calls (typical for production), tool latency is 100%
network-bound and galdor's fan-out is free.

---

## How these numbers compare to the rest of the stack

A typical agent turn looks like:

```
1. LLM call (Anthropic / OpenAI):      500 ms  to  30 s
2. Tool call (HTTP, DB query, etc.):    10 ms  to   2 s
3. galdor's graph + observability:       0.4 ms total (combined)
```

Galdor's own overhead is 3-5 orders of magnitude smaller than the
work it orchestrates. You will spend your performance budget on
LLM tokens and tool I/O — not on galdor.

---

## When these numbers will lie to you

* **You measure latency under contention.** Cosine-similarity in
  `pkg/memory` is CPU-bound; on a busy host it'll be slower.
  Always benchmark on a quiet machine for repeatable numbers.
* **You ignore allocation counts.** A 0-alloc number is what
  makes galdor cheap to run as a long-lived process. If a future
  change pushes allocation counts up, the GC pressure adds up
  even if the per-call number looks flat.
* **You generalize from 10k chunks to 10M.** The in-memory store
  is brute-force linear. At 100k chunks you're at 70 ms per query
  — that's user-noticeable. Move to a vector DB.

---

## Last verified

These numbers were captured on Apple M2-class hardware running
darwin/amd64 (Rosetta) in May 2026. They'll drift as galdor
evolves and as Go releases get faster. Re-run them locally before
making a sizing decision.
