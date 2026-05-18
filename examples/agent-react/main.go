// Command agent-react demonstrates the ReAct loop in pkg/agent
// using a deterministic scriptedProvider so the example runs
// offline. The README documents how to swap in a real provider
// (Anthropic / OpenAI / Google / Bedrock) without touching anything
// else.
//
//	go run ./examples/agent-react
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"

	"github.com/YasserCR/galdor/pkg/agent"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
	"github.com/YasserCR/galdor/pkg/tool"
	"github.com/YasserCR/galdor/pkg/tool/builtins"
)

// --- tools ---------------------------------------------------------

// A weather tool that returns canned data so the example doesn't
// require network access. In a real agent you'd fetch from an API.
type weatherIn struct {
	City string `json:"city" jsonschema:"City to look up"`
}
type weatherOut struct {
	TempC int    `json:"temp_c"`
	Brief string `json:"brief"`
}

func weather(_ context.Context, in weatherIn) (weatherOut, error) {
	return weatherOut{TempC: 21, Brief: "sunny in " + in.City}, nil
}

// --- scripted provider ---------------------------------------------

// scriptedProvider returns: turn 1) a weather tool call,
// turn 2) a math tool call using the weather result, turn 3) a
// final-text answer. It demonstrates a 2-tool ReAct cycle.
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
	switch t {
	case 1:
		return canned(schema.Message{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{
				{ID: "w1", Name: "weather", Arguments: json.RawMessage(`{"city":"Quito"}`)},
			},
		}), nil
	case 2:
		return canned(schema.Message{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{
				{ID: "m1", Name: "math", Arguments: json.RawMessage(`{"op":"mul","a":21,"b":1.8}`)},
			},
		}), nil
	default:
		return canned(schema.AssistantMessage(
			"Quito is sunny at 21°C (about 37.8 °F).")), nil
	}
}

func canned(m schema.Message) *provider.Response {
	return &provider.Response{
		Message:    m,
		StopReason: schema.StopReasonEndTurn,
		Usage:      schema.Usage{InputTokens: 20, OutputTokens: 10},
		Model:      "scripted-1",
	}
}

// --- main ----------------------------------------------------------

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	weatherTool := tool.MustNewTool("weather", "Look up the weather for a city", weather)
	mathTool := builtins.MustNewMathTool()

	reg, err := tool.NewRegistry(weatherTool, mathTool)
	if err != nil {
		return err
	}

	r, err := agent.NewReAct(agent.Config{
		Provider: &scriptedProvider{},
		Tools:    reg,
		Model:    "scripted-1",
	})
	if err != nil {
		return err
	}

	initial := agent.State{
		Messages: []schema.Message{
			schema.SystemMessage("You may use tools when helpful."),
			schema.UserMessage("What's the weather in Quito and what is that in Fahrenheit?"),
		},
	}
	final, err := r.Invoke(context.Background(), initial)
	if err != nil {
		return err
	}

	fmt.Printf("iterations: %d\n", final.Iterations)
	fmt.Printf("answer:     %s\n\n", final.FinalText)

	fmt.Println("conversation:")
	for i, m := range final.Messages {
		switch {
		case m.Role == schema.RoleAssistant && len(m.ToolCalls) > 0:
			fmt.Printf("  %d  assistant: tool calls\n", i)
			for _, tc := range m.ToolCalls {
				fmt.Printf("        %s(%s)\n", tc.Name, tc.Arguments)
			}
		case m.Role == schema.RoleTool:
			fmt.Printf("  %d  tool:      %s\n", i, m.Text())
		default:
			fmt.Printf("  %d  %s: %s\n", i, m.Role, m.Text())
		}
	}
	return nil
}
