// Command eval-suite shows how to use pkg/eval to gate prompt and
// agent changes from CI. It evaluates a deterministic Subject (a
// stand-in for a ReAct agent) against a small dataset using three
// scorers: ExactMatch, Contains, and an LLM-judge that uses a
// scripted Provider so this example never hits the network.
//
// Run with:
//
//	go run ./examples/eval-suite
//
// In a real setup, replace the scripted bits with:
//   - Subject:        agent.Run(ctx, cfg, in) over Anthropic/MiniMax/etc.
//   - LLMJudge.Provider: a stronger model (Opus, GPT-4o) than the Subject
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"

	"github.com/YasserCR/galdor/pkg/eval"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

func main() {
	ctx := context.Background()

	// The Subject is the system under evaluation. In production you
	// wrap your agent.Run, council.RunSupervisor, etc. Here we use
	// a deterministic function that returns canned answers based on
	// the input so the example runs offline.
	subject := func(_ context.Context, input string) (string, error) {
		switch {
		case strings.Contains(strings.ToLower(input), "capital of ecuador"):
			return "Quito is the capital of Ecuador.", nil
		case strings.Contains(strings.ToLower(input), "capital of peru"):
			return "Lima is the capital of Peru.", nil
		case strings.Contains(strings.ToLower(input), "2 plus 2"):
			// Intentionally wrong, to exercise a fail.
			return "5", nil
		default:
			return "I don't know.", nil
		}
	}

	// The judge LLM is mocked too — a scripted provider that always
	// returns "85" so we can demonstrate the LLMJudge scorer without
	// depending on a real API key.
	judgeProvider := &scriptedProvider{Reply: "85"}

	cfg := eval.Config{
		Dataset: eval.Dataset{
			Name:    "geography-and-math",
			Version: "1",
			Cases: []eval.Case{
				{ID: "geo-ec", Input: "What is the capital of Ecuador?", Expected: "Quito"},
				{ID: "geo-pe", Input: "What is the capital of Peru?", Expected: "Lima"},
				{ID: "math-1", Input: "What is 2 plus 2?", Expected: "4"},
			},
		},
		Subject: subject,
		Scorers: []eval.Scorer{
			eval.Contains{},
			eval.LLMJudge{
				Provider: judgeProvider,
				Model:    "scripted-judge",
				Rubric:   "Score 100 only when the answer is factually correct and concise.",
			},
		},
		Parallel: 3,
		MinPass:  eval.Threshold(0.8),
	}

	report, err := eval.Run(ctx, cfg)
	if err != nil {
		log.Fatalf("eval setup: %v", err)
	}
	report.PrintSummary(os.Stdout)

	// In CI, you'd use eval.RunAndExit(ctx, cfg) directly — it
	// prints the summary to stderr and exits with status 1 when
	// PassRate < MinPass. We split it here so the example finishes
	// with a normal main() return regardless of the verdict.
	if !report.Meets(*cfg.MinPass) {
		fmt.Printf("\n%.1f%% < %.1f%% threshold — would exit 1 in CI mode\n",
			report.PassRate()*100, *cfg.MinPass*100)
	} else {
		fmt.Println()
		fmt.Println("CI gate passed.")
	}
}

// scriptedProvider is a tiny Provider that always returns Reply.
// Used by the LLMJudge scorer in this offline example.
type scriptedProvider struct {
	Reply string
	calls atomic.Int32
}

func (*scriptedProvider) Name() string                        { return "scripted-judge" }
func (*scriptedProvider) Capabilities() provider.Capabilities { return provider.Capabilities{} }
func (*scriptedProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}

func (p *scriptedProvider) Generate(_ context.Context, _ provider.Request) (*provider.Response, error) {
	p.calls.Add(1)
	return &provider.Response{
		Message:    schema.AssistantMessage(p.Reply),
		StopReason: schema.StopReasonEndTurn,
	}, nil
}
