// Command scry-store runs a tiny ReAct agent with the OTel
// instrumentation pointing at galdor's embedded SQLite span store,
// then prints the resulting trace using the `galdor scry` CLI.
//
//	go run ./examples/scry-store
//
// What it shows:
//
//  1. observability.NewSQLiteExporter is a regular OTel
//     SpanExporter — drop it into sdktrace.NewTracerProvider
//     wherever you'd put stdouttrace / OTLP.
//
//  2. The same DB the exporter writes to is the DB the CLI reads.
//     No external service, no daemon — the binary IS the
//     observability backend.
//
//  3. After running the demo the file path printed at the end
//     contains the spans. You can poke at it with sqlite3 too:
//
//     sqlite3 -header -column /path/to/traces.db \
//     'SELECT run_id, name, status_code FROM spans;'
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/YasserCR/galdor/internal/store"
	"github.com/YasserCR/galdor/pkg/agent"
	"github.com/YasserCR/galdor/pkg/graph"
	"github.com/YasserCR/galdor/pkg/observability"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
	"github.com/YasserCR/galdor/pkg/tool"
	"github.com/YasserCR/galdor/pkg/tool/builtins"
)

type scriptedProvider struct{ turn atomic.Int32 }

func (*scriptedProvider) Name() string { return "scripted" }
func (*scriptedProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{ToolCalling: true}
}
func (*scriptedProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}
func (p *scriptedProvider) Generate(_ context.Context, _ provider.Request) (*provider.Response, error) {
	t := p.turn.Add(1)
	if t == 1 {
		return &provider.Response{
			Message: schema.Message{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "m1", Name: "math", Arguments: json.RawMessage(`{"op":"add","a":2,"b":3}`)},
				},
			},
			StopReason: schema.StopReasonEndTurn,
			Usage:      schema.Usage{InputTokens: 30, OutputTokens: 10},
			Model:      "scripted-1",
		}, nil
	}
	return &provider.Response{
		Message:    schema.AssistantMessage("the answer is 5"),
		StopReason: schema.StopReasonEndTurn,
		Usage:      schema.Usage{InputTokens: 50, OutputTokens: 8},
		Model:      "scripted-1",
	}, nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dir, err := os.MkdirTemp("", "galdor-scry-demo-*")
	if err != nil {
		return err
	}
	dbPath := filepath.Join(dir, "traces.db")

	// 1. SQLite-backed OTel exporter wired into a fresh TracerProvider.
	exporter, err := observability.NewSQLiteExporter(dbPath)
	if err != nil {
		return err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
	tracer := tp.Tracer("galdor-demo")

	// 2. Build the agent against instrumented primitives.
	reg, err := tool.NewRegistry(builtins.MustNewMathTool())
	if err != nil {
		return err
	}
	tracedProvider := observability.InstrumentProvider(&scriptedProvider{}, tracer,
		observability.WithCaptureContent(true))
	tracedReg, err := observability.InstrumentRegistry(reg, tracer)
	if err != nil {
		return err
	}
	r, err := agent.NewReAct(agent.Config{
		Provider: tracedProvider,
		Tools:    tracedReg,
		Model:    "scripted-1",
	})
	if err != nil {
		return err
	}

	// 3. Run with TraceHooks so every node hop becomes a span.
	final, err := r.InvokeWith(context.Background(), agent.State{
		Messages: []schema.Message{
			schema.SystemMessage("Use tools when helpful."),
			schema.UserMessage("What's 2 + 3?"),
		},
	}, graph.RunOptions[agent.State]{
		Hooks: observability.TraceHooks[agent.State](tracer),
		RunID: "demo-run-1",
	})
	if err != nil {
		return err
	}

	// 4. Flush spans through the exporter and shut things down so
	//    the next step can read the DB.
	if shutErr := tp.Shutdown(context.Background()); shutErr != nil {
		return shutErr
	}

	fmt.Printf("agent reply: %s\n\n", final.FinalText)
	fmt.Printf("traces stored in: %s\n", dbPath)
	fmt.Printf("explore with:\n  galdor scry list --db %s\n  galdor scry show --db %s demo-run-1\n\n", dbPath, dbPath)

	// 5. Demonstrate the read path by querying the store directly,
	//    which is exactly what the CLI does internally.
	s, err := store.Open(context.Background(), dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	runs, err := s.ListRuns(context.Background(), 10)
	if err != nil {
		return err
	}
	fmt.Println("ListRuns says:")
	for _, run := range runs {
		fmt.Printf("  %s  status=%s  spans=%d\n", run.RunID, run.Status(), run.SpanCount)
	}
	return nil
}
