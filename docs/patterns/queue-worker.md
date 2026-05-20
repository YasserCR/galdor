# Queue-driven agent worker

galdor agents run perfectly well behind a queue: the queue gives you durability and back-pressure, your worker process picks up jobs, runs an agent against them, and acks. This pattern shows the wiring against any queue with a "fetch + ack" loop. The concrete examples here use BullMQ (the Pragma stack) and NATS, but the same shape works with Kafka, SQS, RabbitMQ, or Redis Streams.

## When to use

- Long-running jobs (planning, summarization, document ingest) that you don't want to keep an HTTP request open for.
- Bursty traffic that needs to land in durable storage before any agent runs.
- Multi-tenant or multi-language stacks where the queue is the lingua franca and the worker happens to be Go.

If you just want a single HTTP endpoint that calls one agent, see [agent](../concepts/agent.md) — you do not need a queue for that.

## The shape

```
producer (any language) → broker (BullMQ/NATS/Kafka) → worker (this binary)
                                                          │
                                                          ├── observability.SQLiteExporter (writes spans)
                                                          ├── observability.InstrumentProvider (wraps provider)
                                                          └── agent.NewReAct or graph.Runnable[S]
```

The worker is one Go binary. It connects to the broker, fetches jobs, runs each job under a per-job `context.Context` carrying a per-job run id, and acks (or nacks) on completion.

## Minimal worker

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/YasserCR/galdor/pkg/agent"
	"github.com/YasserCR/galdor/pkg/observability"
	"github.com/YasserCR/galdor/providerset"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	exporter, err := observability.NewSQLiteExporter("./traces.db")
	if err != nil {
		logger.Error("exporter", "err", err)
		os.Exit(1)
	}
	defer exporter.Shutdown(ctx)
	tracer := newTracer(exporter)

	prov, err := providerset.FromEnv()
	if err != nil {
		logger.Error("provider", "err", err)
		os.Exit(1)
	}
	prov = observability.InstrumentProvider(prov, tracer, observability.WithCaptureContent(true))

	queue := newQueueConsumer()
	for {
		job, err := queue.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Warn("fetch", "err", err)
			continue
		}
		go handle(ctx, prov, tracer, job)
	}
}

func handle(ctx context.Context, prov provider.Provider, tracer trace.Tracer, job Job) {
	ctx = observability.WithRunID(ctx, job.ID)
	ctx, span := tracer.Start(ctx, "worker.job", trace.WithAttributes(
		attribute.String("job.id", job.ID),
		attribute.String("job.kind", job.Kind),
	))
	defer span.End()

	answer, err := agent.Run(ctx, agent.Config{
		Provider: prov,
		Model:    job.Model,
	}, job.Prompt)
	if err != nil {
		span.RecordError(err)
		job.Nack(err)
		return
	}
	job.Ack(answer)
}
```

Three things make this work end-to-end:

1. **`observability.WithRunID(ctx, job.ID)`** ties every span produced inside `handle` to the queue job id. The dashboard groups them under that id.
2. **A root span (`worker.job`)** wraps the call so the provider span has a parent — without this, instrumentation would fall back to the trace id, which is fine but less informative than the explicit job id.
3. **The provider is constructed once at startup**, not per job. Adapters are concurrency-safe.

## BullMQ specifically

When your producer is a TypeScript service using BullMQ, the cleanest path is the `go.codycody31.dev/gobullmq` consumer:

```go
import "go.codycody31.dev/gobullmq"

queue, err := gobullmq.NewQueue(gobullmq.QueueOptions{
	Name:        "agent-jobs",
	RedisAddr:   os.Getenv("REDIS_ADDR"),
	Concurrency: 4,
})
if err != nil { /* ... */ }

queue.ProcessFunc(func(ctx context.Context, jb gobullmq.Job) error {
	return handle(ctx, prov, tracer, Job{
		ID:     jb.ID,
		Kind:   jb.Name,
		Prompt: jb.Data["prompt"].(string),
		Model:  jb.Data["model"].(string),
	})
})
```

If `gobullmq` ever drifts, you can swap it for any Redis-streams client and re-implement the BullMQ wire format yourself — the protocol is fixed and small.

## NATS specifically

NATS JetStream gives you durable subjects without Redis. The galdor side is identical; only the fetch loop changes:

```go
import "github.com/nats-io/nats.go"

nc, _ := nats.Connect(os.Getenv("NATS_URL"))
js, _ := nc.JetStream()
sub, _ := js.PullSubscribe("agent.jobs", "workers")

for {
	msgs, err := sub.Fetch(1, nats.Context(ctx))
	if err != nil { /* ... */ }
	for _, m := range msgs {
		handle(ctx, prov, tracer, decodeJob(m))
		m.Ack()
	}
}
```

## Operational notes

- **One TracerProvider per process**, lots of jobs share it. The SQLite exporter is concurrency-safe.
- **Cancel the per-job ctx with a deadline** (`context.WithTimeout`). Stuck jobs are the most common cause of worker outages.
- **Nack with a backoff strategy** the queue understands. BullMQ has `attemptsMade`; NATS has `nats.MaxDeliver`.
- **Cap concurrency below your provider's rate limit.** Otherwise you trade durable jobs for `provider.ErrRateLimited` storms. `provider.Retry` mitigates spikes; concurrency caps prevent them.

## See also

- [Cost tracking](cost-tracking.md) — the budget middleware composes with `InstrumentProvider`, useful when each queue job has a hard $-cap.
- [Human-in-the-loop](human-in-the-loop.md) — when a job needs human approval mid-run.
- [Observability](../concepts/observability.md) — the `WithRunID` mechanic and dashboard grouping.
- [`retro/pragma-galdor/pragma-agents-go`](../../../retro/pragma-galdor/pragma-agents-go/) — a real worker against BullMQ + Postgres + galdor.
