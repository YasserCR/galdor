// Command integration-http-interpret is a complete, copy-paste HTTP
// service that wraps a galdor agent: structured output, OTel tracing into
// the embedded SQLite store, a health endpoint, and graceful shutdown.
//
// It is deliberately NOT a framework. galdor ships no `pkg/serve`; an agent
// is a plain Go value, so exposing one over HTTP is your `net/http` handler
// plus the few lines below. Copy this file, swap the scripted provider for
// a real adapter, and you have a service.
//
// Run with:
//
//	go run ./examples/integration-http-interpret
//	curl -s localhost:8088/healthz
//	curl -s localhost:8088/interpret -d 'book a flight to Quito next Friday'
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/YasserCR/galdor/pkg/observability"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// Interpretation is the structured result the model returns for each
// request — the shape the service guarantees to its callers.
type Interpretation struct {
	Intent     string   `json:"intent" jsonschema:"the user's goal in a few words"`
	Entities   []string `json:"entities" jsonschema:"key nouns mentioned"`
	Confidence float64  `json:"confidence" jsonschema:"0..1 confidence in the intent"`
}

type server struct {
	provider provider.Provider
	model    string
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	const addr = "127.0.0.1:8088"
	const dbPath = "./interpret-traces.db"

	// Observability: every model call becomes a span in the SQLite store,
	// inspectable with `galdor scry` / `ui`.
	exporter, err := observability.NewSQLiteExporter(dbPath)
	if err != nil {
		return fmt.Errorf("trace store: %w", err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
	tracer := tp.Tracer("interpret")

	// Swap scriptedProvider for anthropic.New(...) / openai.New(...) — it
	// reports StructuredOutput: true and the rest is identical.
	raw := &scriptedProvider{reply: `{"intent":"book travel","entities":["flight","Quito","Friday"],"confidence":0.92}`}
	s := &server{
		provider: observability.InstrumentProvider(raw, tracer, observability.WithCaptureContent(true)),
		model:    "scripted-1",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /interpret", s.handleInterpret)

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Graceful shutdown: SIGINT/SIGTERM drain in-flight requests, then flush
	// spans.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("listening on http://%s  (traces: %s)", addr, dbPath)
		if serveErr := httpSrv.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Println("shutting down…")
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
		_ = tp.Shutdown(shutCtx)
		return nil
	}
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

func (s *server) handleInterpret(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || len(body) == 0 {
		http.Error(w, "empty request body", http.StatusBadRequest)
		return
	}

	// One call: the model's reply is constrained to Interpretation's schema
	// and decoded back into the struct.
	result, err := provider.GenerateStructured[Interpretation](r.Context(), s.provider, provider.Request{
		Model: s.model,
		Messages: []schema.Message{
			schema.SystemMessage("Extract the user's intent and key entities."),
			schema.UserMessage(string(body)),
		},
	})
	if err != nil {
		http.Error(w, "interpret: "+err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)
}

// scriptedProvider stands in for a real adapter so the example runs
// offline. A real provider (anthropic/openai/google) drops in unchanged.
type scriptedProvider struct {
	reply string
}

func (*scriptedProvider) Name() string { return "scripted" }
func (*scriptedProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{StructuredOutput: true}
}
func (*scriptedProvider) Stream(context.Context, provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}
func (p *scriptedProvider) Generate(context.Context, provider.Request) (*provider.Response, error) {
	return &provider.Response{Message: schema.AssistantMessage(p.reply), StopReason: schema.StopReasonEndTurn}, nil
}
