// Command integration-cost-tracked demonstrates per-run budget
// enforcement: a Provider middleware tallies input + output tokens
// across every call and aborts the agent the moment the configured
// budget is exceeded.
//
// This is the production answer to "the model went off the rails
// and burned $200 on retries before someone noticed."
//
// What this exercises end-to-end:
//
//   - Custom Provider wrapper that composes around any real provider
//   - Atomic counters so concurrent tool fan-out doesn't undercount
//   - A pricing table keyed by model ID, dollar-cost printout
//   - Hard-abort by returning an ErrBudgetExceeded that the agent
//     surfaces up to the run boundary
//
// Run with:
//
//	go run ./examples/integration-cost-tracked
//
// The example runs three scenarios with progressively larger
// scripted responses. The third one is sized to blow the budget;
// the run aborts mid-flight and reports total $ consumed.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/YasserCR/galdor/pkg/agent"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// ErrBudgetExceeded is returned by BudgetProvider when a Generate
// call would push the run past its configured token budget.
var ErrBudgetExceeded = errors.New("budget exceeded")

// BudgetProvider wraps any provider.Provider and tracks cumulative
// input + output tokens. When the running total exceeds Budget,
// subsequent Generate calls return ErrBudgetExceeded immediately
// without invoking the inner provider.
//
// Prices are dollars per 1000 tokens, indexed by model ID. Unknown
// models default to PriceDefault.
type BudgetProvider struct {
	inner        provider.Provider
	budgetTokens int64

	mu          sync.Mutex
	prices      map[string]Pricing
	priceFallbk Pricing

	// Cumulative counters — atomic for safe concurrent updates
	// from parallel tool-result Generate calls in the same run.
	tokensIn  atomic.Int64
	tokensOut atomic.Int64
}

// Pricing is dollars per 1000 input or output tokens for one model.
type Pricing struct {
	InputPer1K  float64
	OutputPer1K float64
}

// BudgetConfig configures NewBudgetProvider.
type BudgetConfig struct {
	// Inner is the real provider to wrap. Required.
	Inner provider.Provider

	// BudgetTokens is the cumulative cap. Use 0 to disable
	// enforcement and use the provider purely for cost reporting.
	BudgetTokens int

	// Prices maps model ID to per-1k pricing. Models not listed
	// fall back to PriceDefault.
	Prices map[string]Pricing

	// PriceDefault is used for any model not in Prices.
	PriceDefault Pricing
}

// NewBudgetProvider constructs a BudgetProvider.
func NewBudgetProvider(cfg BudgetConfig) *BudgetProvider {
	if cfg.Inner == nil {
		log.Fatal("BudgetProvider: Inner is required")
	}
	return &BudgetProvider{
		inner:        cfg.Inner,
		budgetTokens: int64(cfg.BudgetTokens),
		prices:       cfg.Prices,
		priceFallbk:  cfg.PriceDefault,
	}
}

// Name implements provider.Provider.
func (b *BudgetProvider) Name() string { return b.inner.Name() }

// Capabilities implements provider.Provider.
func (b *BudgetProvider) Capabilities() provider.Capabilities { return b.inner.Capabilities() }

// Stream is not implemented in this example — agents that need
// streaming can wrap the StreamReader with a counting filter,
// but Generate-only is the more common production pattern.
func (b *BudgetProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}

// Generate enforces the budget at TWO points:
//
//   - Pre-call: if the running total already meets or exceeds the
//     budget, refuse immediately without invoking the inner provider.
//   - Post-call: if the call's usage pushed the total past the
//     budget, return ErrBudgetExceeded *along with* the response
//     (so the caller can see what they got for the over-budget
//     spend, but the agent loop still aborts).
//
// Both checks are gated by budgetTokens > 0. With budgetTokens == 0
// the wrapper is pure accounting — useful for "tell me what this
// costs without enforcing".
func (b *BudgetProvider) Generate(ctx context.Context, req provider.Request) (*provider.Response, error) {
	if b.budgetTokens > 0 {
		used := b.tokensIn.Load() + b.tokensOut.Load()
		if used >= b.budgetTokens {
			return nil, fmt.Errorf("%w: %d tokens already consumed, budget %d",
				ErrBudgetExceeded, used, b.budgetTokens)
		}
	}
	resp, err := b.inner.Generate(ctx, req)
	if err != nil {
		return resp, err
	}
	b.tokensIn.Add(int64(resp.Usage.InputTokens))
	b.tokensOut.Add(int64(resp.Usage.OutputTokens))
	if b.budgetTokens > 0 {
		used := b.tokensIn.Load() + b.tokensOut.Load()
		if used >= b.budgetTokens {
			return resp, fmt.Errorf("%w: this call pushed the total to %d (budget %d)",
				ErrBudgetExceeded, used, b.budgetTokens)
		}
	}
	return resp, nil
}

// Report returns the cumulative usage + cost. Call it after a run
// (or periodically during one) to surface the actual $ spent.
type Report struct {
	TokensIn  int64
	TokensOut int64
	CostUSD   float64
	Model     string
}

func (b *BudgetProvider) Report(model string) Report {
	in := b.tokensIn.Load()
	out := b.tokensOut.Load()
	price := b.priceFallbk
	if p, ok := b.prices[model]; ok {
		price = p
	}
	cost := (float64(in)/1000)*price.InputPer1K + (float64(out)/1000)*price.OutputPer1K
	return Report{TokensIn: in, TokensOut: out, CostUSD: cost, Model: model}
}

// Compile-time assertion.
var _ provider.Provider = (*BudgetProvider)(nil)

// ----- demo wiring ----------------------------------------------------------

const model = "demo-model"

func main() {
	ctx := context.Background()

	// Pricing similar in shape to Claude Haiku 4.5 — kept fake
	// so the example doesn't go stale when real prices change.
	prices := map[string]Pricing{
		model: {InputPer1K: 0.001, OutputPer1K: 0.005},
	}

	scenarios := []struct {
		name           string
		userMessage    string
		responsesSize  int // chars per scripted response (drives output tokens)
		expectedAbort  bool
		expectedBudget int
	}{
		{name: "small", userMessage: "hello", responsesSize: 200, expectedBudget: 10_000},
		{name: "medium", userMessage: "summarize a 1-page doc", responsesSize: 1500, expectedBudget: 10_000},
		{name: "huge",
			userMessage:    "rewrite the entire constitution in 5 styles",
			responsesSize:  9000,
			expectedAbort:  true,
			expectedBudget: 2_000,
		},
	}

	for _, sc := range scenarios {
		fmt.Printf("\n=== scenario: %s (budget = %d tokens) ===\n", sc.name, sc.expectedBudget)
		bp := NewBudgetProvider(BudgetConfig{
			Inner:        newScriptedProvider(sc.responsesSize),
			BudgetTokens: sc.expectedBudget,
			Prices:       prices,
			PriceDefault: Pricing{InputPer1K: 0.001, OutputPer1K: 0.005},
		})

		r, err := agent.NewReAct(agent.Config{
			Provider:      bp,
			Model:         model,
			MaxIterations: 10,
		})
		if err != nil {
			log.Fatal(err)
		}

		final, err := r.Invoke(ctx, agent.State{
			Messages: []schema.Message{schema.UserMessage(sc.userMessage)},
		})
		rep := bp.Report(model)

		switch {
		case err == nil:
			fmt.Printf("  finished: %q\n", truncate(final.FinalText, 80))
		case errors.Is(err, ErrBudgetExceeded):
			fmt.Printf("  aborted: budget exceeded mid-run\n")
		default:
			fmt.Printf("  unexpected err: %v\n", err)
		}
		fmt.Printf("  usage: %d in + %d out = %d tokens, cost ~$%.4f\n",
			rep.TokensIn, rep.TokensOut, rep.TokensIn+rep.TokensOut, rep.CostUSD)
		if sc.expectedAbort && err == nil {
			fmt.Printf("  WARNING: expected abort but run completed\n")
		}
	}
}

// scriptedProvider returns responses of a fixed character size so
// the example produces predictable token counts. In a real demo
// you replace it with anthropic / openai / etc — the BudgetProvider
// wraps the same way.
type scriptedProvider struct {
	respSize int
	calls    atomic.Int32
}

func newScriptedProvider(respSize int) *scriptedProvider {
	return &scriptedProvider{respSize: respSize}
}

func (*scriptedProvider) Name() string { return "scripted-cost-demo" }
func (*scriptedProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{ToolCalling: true}
}
func (*scriptedProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}

func (p *scriptedProvider) Generate(_ context.Context, req provider.Request) (*provider.Response, error) {
	n := int(p.calls.Add(1))
	body := strings.Repeat("lorem ipsum ", p.respSize/12+1)
	body = body[:p.respSize]

	// Approximate tokens: ~4 chars per token (matches the
	// in-tree estimator). The numbers are what BudgetProvider
	// reads to enforce the budget.
	inputTokens := roughTokens(req)
	outputTokens := len(body) / 4

	return &provider.Response{
		Message:    schema.AssistantMessage(fmt.Sprintf("call %d: %s", n, truncate(body, 60))),
		StopReason: schema.StopReasonEndTurn,
		Model:      model,
		Usage: schema.Usage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
		},
	}, nil
}

func roughTokens(req provider.Request) int {
	total := 0
	for _, m := range req.Messages {
		total += len(m.Text())
	}
	return total / 4
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "..."
}
