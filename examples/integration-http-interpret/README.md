# examples/integration-http-interpret

A complete HTTP service that wraps a galdor agent — structured output, OTel
tracing into the embedded SQLite store, a health endpoint, and graceful
shutdown. It's a copy-paste **starter, not a framework**: galdor ships no
`pkg/serve`, because an agent is a plain Go value and exposing one over HTTP
is your `net/http` handler plus a few lines. Copy `main.go`, swap the
provider, ship.

## Run

```bash
go run ./examples/integration-http-interpret

curl -s localhost:8088/healthz
# {"status":"ok"}

curl -s localhost:8088/interpret -d 'book a flight to Quito next Friday'
# {
#   "intent": "book travel",
#   "entities": ["flight", "Quito", "Friday"],
#   "confidence": 0.92
# }
```

Then inspect the recorded calls:

```bash
galdor scry list --db ./interpret-traces.db
galdor ui        --db ./interpret-traces.db
```

## What it shows

- **Structured output as the API contract.** `/interpret` returns whatever
  the model produced, constrained to the `Interpretation` struct via
  `provider.GenerateStructured` — callers get the same JSON shape every
  time.
- **Observability for free.** `observability.InstrumentProvider` + a SQLite
  exporter means every request's model call is a span you can open in
  `galdor scry` / `ui`.
- **Production hygiene.** A health endpoint, a read-header timeout, and
  graceful shutdown that drains in-flight requests and flushes spans on
  SIGINT/SIGTERM.

## Make it real

Swap the scripted provider for a real adapter (it reports
`StructuredOutput: true`; nothing else changes):

```go
import anthropic "github.com/YasserCR/galdor/providers/anthropic"

raw, _ := anthropic.New(anthropic.Config{APIKey: os.Getenv("ANTHROPIC_API_KEY")})
s := &server{
    provider: observability.InstrumentProvider(raw, tracer, observability.WithCaptureContent(true)),
    model:    "claude-haiku-4-5",
}
```
