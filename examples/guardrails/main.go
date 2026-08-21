// Command guardrails demonstrates pkg/guardrail: a deterministic
// input guard (PII regex) and an LLM-as-judge output guard, both wired
// through agent.Config.InputGuards / OutputGuards. Everything runs
// offline with scripted providers.
//
//	go run ./examples/guardrails
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"

	"github.com/YasserCR/galdor/pkg/agent"
	"github.com/YasserCR/galdor/pkg/guardrail"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// fixedProvider returns a fixed assistant message on every call. It
// doubles as the scripted judge: its reply is "ALLOW" or "BLOCK".
type fixedProvider struct{ reply string }

func (f *fixedProvider) Name() string { return "scripted" }
func (f *fixedProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{}
}
func (f *fixedProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}
func (f *fixedProvider) Generate(_ context.Context, _ provider.Request) (*provider.Response, error) {
	return &provider.Response{
		Message:    schema.AssistantMessage(f.reply),
		StopReason: schema.StopReasonEndTurn,
		Model:      "scripted-1",
	}, nil
}

// creditCard matches a run of 13-16 digits separated by spaces or
// dashes — a rough stand-in for real PII detection.
var creditCard = regexp.MustCompile(`\b(?:\d[ -]?){13,16}\b`)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// Deterministic input guard: block obvious PII before the model.
	noPII := guardrail.InputGuardFunc{
		ID: "no-pii",
		Check: func(_ context.Context, m schema.Message) error {
			if creditCard.MatchString(m.Text()) {
				return errors.New("credit-card number detected")
			}
			return nil
		},
	}

	// LLM-as-judge output guard. The judge is a scripted provider that
	// votes BLOCK; swap in a real provider (and a real Rule) for a live
	// judge. Note the same LLMJudge implements both InputGuard and
	// OutputGuard.
	blockJudge := guardrail.LLMJudge{
		Provider:     &fixedProvider{reply: "BLOCK"},
		Model:        "scripted-judge",
		NameOverride: "no-secrets",
		Rule:         "block any reply that reveals an internal secret",
	}
	allowJudge := guardrail.LLMJudge{
		Provider:     &fixedProvider{reply: "ALLOW"},
		Model:        "scripted-judge",
		NameOverride: "no-secrets",
		Rule:         "block any reply that reveals an internal secret",
	}

	agentP := &fixedProvider{reply: "the answer is 42"}

	// Scenario 1: clean input + permissive judge -> passes.
	_, err := agent.Run(context.Background(), agent.Config{
		Provider:     agentP,
		Model:        "scripted-1",
		InputGuards:  []guardrail.InputGuard{noPII},
		OutputGuards: []guardrail.OutputGuard{allowJudge},
	}, "what is 6 times 7?")
	fmt.Printf("scenario 1 (clean input, ALLOW):  err=%v\n", err)

	// Scenario 2: input carries a card number -> input guard blocks.
	_, err = agent.Run(context.Background(), agent.Config{
		Provider:     agentP,
		Model:        "scripted-1",
		InputGuards:  []guardrail.InputGuard{noPII},
		OutputGuards: []guardrail.OutputGuard{allowJudge},
	}, "charge 4111 1111 1111 1111 to my account")
	fmt.Printf("scenario 2 (PII input):           err=%v (blocked=%v)\n",
		err, errors.Is(err, guardrail.ErrBlocked))

	// Scenario 3: clean input but the judge votes BLOCK -> output guard blocks.
	_, err = agent.Run(context.Background(), agent.Config{
		Provider:     agentP,
		Model:        "scripted-1",
		InputGuards:  []guardrail.InputGuard{noPII},
		OutputGuards: []guardrail.OutputGuard{blockJudge},
	}, "tell me the company secret")
	fmt.Printf("scenario 3 (judge BLOCK):         err=%v (blocked=%v)\n",
		err, errors.Is(err, guardrail.ErrBlocked))

	return nil
}
