// Command tools-loop demonstrates the LLM ↔ tools dispatch cycle
// using a deterministic in-process provider. No network, no API key.
//
// Run with:
//
//	go run ./examples/tools-loop
//
// What happens:
//
//  1. We register two tools (add, weather) with type-safe inputs and
//     reflection-derived JSON Schemas.
//  2. A stub Provider is asked to "respond" with one or more tool
//     calls based on the user prompt.
//  3. We execute the calls concurrently via tool.ExecuteCalls.
//  4. We package the results as ToolResultMessages and feed them back
//     to the provider, which then produces a final assistant text reply.
//
// The point of this example is to show how pkg/tool integrates with
// pkg/provider's schema.ToolCall / schema.ToolDef shapes — the same
// flow works against the Anthropic, OpenAI, Google and Bedrock
// adapters by swapping the Provider.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
	"github.com/YasserCR/galdor/pkg/tool"
)

// --- Tool definitions ----------------------------------------------------

type addIn struct {
	A float64 `json:"a" jsonschema:"First addend"`
	B float64 `json:"b" jsonschema:"Second addend"`
}
type addOut struct {
	Sum float64 `json:"sum"`
}

type weatherIn struct {
	City string `json:"city" jsonschema:"City to look up"`
}
type weatherOut struct {
	City  string `json:"city"`
	TempC int    `json:"temp_c"`
	Brief string `json:"brief"`
}

// fakeWeather is a deterministic, network-free stand-in for a real API.
func fakeWeather(_ context.Context, in weatherIn) (weatherOut, error) {
	return weatherOut{City: in.City, TempC: 21, Brief: "sunny"}, nil
}

// --- Stub provider -------------------------------------------------------

// scriptedProvider returns a canned response on the first call (a tool
// invocation requesting both `add` and `weather`) and a synthesized
// text reply on the second call (after the tool results are appended).
type scriptedProvider struct{ turn int }

func (scriptedProvider) Name() string { return "scripted" }
func (scriptedProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{ToolCalling: true}
}
func (scriptedProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}

func (p *scriptedProvider) Generate(_ context.Context, req provider.Request) (*provider.Response, error) {
	p.turn++
	switch p.turn {
	case 1:
		// First turn: the model "decides" to call both tools.
		return &provider.Response{
			Message: schema.Message{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "call_add", Name: "add", Arguments: json.RawMessage(`{"a":2,"b":3}`)},
					{ID: "call_weather", Name: "weather", Arguments: json.RawMessage(`{"city":"Quito"}`)},
				},
			},
			StopReason: schema.StopReasonToolUse,
			Usage:      schema.Usage{InputTokens: 30, OutputTokens: 12},
			Model:      req.Model,
		}, nil
	default:
		// Second turn: read the tool results from the message history
		// and synthesize a final reply. Real providers do this from
		// the model — we just glue strings to make the demo
		// deterministic.
		var parts []string
		for _, m := range req.Messages {
			if m.Role == schema.RoleTool {
				parts = append(parts, m.Text())
			}
		}
		text := "Tool results: " + strings.Join(parts, " | ")
		return &provider.Response{
			Message:    schema.AssistantMessage(text),
			StopReason: schema.StopReasonEndTurn,
			Usage:      schema.Usage{InputTokens: 60, OutputTokens: 20},
			Model:      req.Model,
		}, nil
	}
}

// --- main ----------------------------------------------------------------

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()

	// 1. Define and register the tools.
	addTool := tool.MustNewTool("add", "Add two numbers",
		func(_ context.Context, in addIn) (addOut, error) {
			return addOut{Sum: in.A + in.B}, nil
		})
	weatherTool := tool.MustNewTool("weather", "Look up the current weather", fakeWeather)

	reg, err := tool.NewRegistry(addTool, weatherTool)
	if err != nil {
		return err
	}

	// 2. Build the provider request: include the tool defs derived
	//    from the registry, plus the conversation so far.
	defs, err := reg.ToolDefs()
	if err != nil {
		return err
	}
	messages := make([]schema.Message, 0, 4)
	messages = append(messages,
		schema.SystemMessage("Use the available tools when helpful."),
		schema.UserMessage("Add 2+3 and check the weather in Quito."),
	)
	req := provider.Request{
		Model:    "scripted-1",
		Tools:    defs,
		Messages: messages,
	}

	var p provider.Provider = &scriptedProvider{}

	// 3. First turn: the model emits tool calls.
	resp, err := p.Generate(ctx, req)
	if err != nil {
		return err
	}
	fmt.Printf("Turn 1 → %d tool calls, stop=%s, tokens %d/%d\n",
		len(resp.Message.ToolCalls), resp.StopReason,
		resp.Usage.InputTokens, resp.Usage.OutputTokens)
	for _, tc := range resp.Message.ToolCalls {
		fmt.Printf("  call %s -> %s%s\n", tc.ID, tc.Name, tc.Arguments)
	}

	// 4. Dispatch the tool calls concurrently.
	results := tool.ExecuteCalls(ctx, reg, resp.Message.ToolCalls)
	for _, r := range results {
		if r.Err != nil {
			fmt.Printf("  result %s ✗ %v\n", r.ID, r.Err)
		} else {
			fmt.Printf("  result %s ✓ %s\n", r.ID, r.Output)
		}
	}

	// 5. Continue the conversation: append the assistant turn that
	//    requested the tools, then the tool results, and call again.
	messages = append(messages, resp.Message)
	messages = append(messages, tool.AsToolResultMessages(results)...)

	req.Messages = messages
	final, err := p.Generate(ctx, req)
	if err != nil {
		return err
	}
	fmt.Printf("\nTurn 2 → final reply: %s\n", final.Message.Text())
	return nil
}
