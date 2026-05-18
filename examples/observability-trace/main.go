// Command observability-trace runs a tiny ReAct agent with full
// OpenTelemetry instrumentation: provider calls, tool executions
// and graph node hops all emit spans. The spans are exported to
// stdout in JSON via the stdouttrace exporter so you can read them
// directly.
//
//	go run ./examples/observability-trace
//
// The agent is the same offline scriptedProvider + builtin math
// tool combo used by examples/agent-react, so the run is
// deterministic. Swap in a real provider (Anthropic, OpenAI,
// Google, Bedrock) and the same instrumentation captures real LLM
// + tool spans.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync/atomic"

	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/YasserCR/galdor/pkg/agent"
	"github.com/YasserCR/galdor/pkg/graph"
	"github.com/YasserCR/galdor/pkg/observability"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
	"github.com/YasserCR/galdor/pkg/tool"
	"github.com/YasserCR/galdor/pkg/tool/builtins"
)

// --- offline scripted provider ----------------------------------------

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

// --- main -------------------------------------------------------------

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// 1. Boot a TracerProvider that writes pretty JSON to stdout.
	//    In production you'd use OTLP / Jaeger / your favorite
	//    backend; the rest of the code is unchanged.
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewSchemaless(
			semconv.ServiceName("galdor-demo"),
		)),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()
	tracer := tp.Tracer("galdor-demo")

	// 2. Wrap the provider and the registry so every Generate /
	//    Stream / tool call emits its own span. The agent code
	//    itself does not change.
	rawProvider := &scriptedProvider{}
	rawRegistry, err := tool.NewRegistry(builtins.MustNewMathTool())
	if err != nil {
		return err
	}

	tracedProvider := observability.InstrumentProvider(rawProvider, tracer)
	tracedRegistry, err := observability.InstrumentRegistry(rawRegistry, tracer)
	if err != nil {
		return err
	}

	// 3. Build the ReAct agent against the traced primitives.
	r, err := agent.NewReAct(agent.Config{
		Provider: tracedProvider,
		Tools:    tracedRegistry,
		Model:    "scripted-1",
	})
	if err != nil {
		return err
	}

	// 4. Run it. TraceHooks emits one root span for the run and one
	//    child span per node hop; tool and provider spans nest
	//    inside the node spans automatically.
	hooks := observability.TraceHooks[agent.State](tracer)
	fmt.Fprintln(os.Stderr, "--- running agent (spans below come from the stdouttrace exporter) ---")
	final, err := r.InvokeWith(context.Background(), agent.State{
		Messages: []schema.Message{
			schema.SystemMessage("Use tools when helpful."),
			schema.UserMessage("What's 2 + 3?"),
		},
	}, graph.RunOptions[agent.State]{
		Hooks: hooks,
		RunID: "run-demo-1",
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\n--- final answer: %s\n", final.FinalText)
	return nil
}
