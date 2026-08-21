package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/YasserCR/galdor/pkg/graph"
	"github.com/YasserCR/galdor/pkg/guardrail"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
	"github.com/YasserCR/galdor/pkg/tool"
)

// State is the value that flows through the ReAct graph. Construct
// it with the seed conversation (system + user) and pass it to
// Invoke / Stream. The runtime appends each assistant turn and each
// tool-result batch as the loop iterates.
type State struct {
	// Messages is the running conversation.
	Messages []schema.Message

	// FinalText is populated when the loop terminates with an
	// assistant message that has no tool calls. It mirrors the last
	// message's Text() and exists as a convenience for callers.
	FinalText string

	// Iterations counts how many times the model node executed in
	// this run.
	Iterations int

	// InputGuarded is the index into Messages up to which input guards
	// have already run. The model node vets user messages from this
	// watermark on and then advances it, so re-invoking the runnable
	// with a carried-over State guards newly appended user turns
	// exactly once instead of skipping or re-judging them. Leave it
	// zero when seeding a fresh State.
	InputGuarded int

	// StoppedAtIterationCap is set when the loop terminated because it
	// hit MaxIterations while the model's last turn still had pending
	// tool calls — i.e. the run was truncated, not completed. FinalText
	// will be a best-effort value (often empty) in that case. Run
	// surfaces this as ErrMaxIterations.
	StoppedAtIterationCap bool
}

// ErrMaxIterations is returned by Run when the loop stopped at
// MaxIterations with tool calls still pending, so an empty result isn't
// mistaken for a completed (empty) answer.
var ErrMaxIterations = errors.New("agent: stopped at MaxIterations with tool calls still pending")

// Config configures the ReAct loop.
type Config struct {
	// Provider is required.
	Provider provider.Provider

	// Tools is optional. When non-nil, the registry's ToolDefs are
	// attached to each model call and the model is free to call
	// tools.
	Tools *tool.Registry

	// Model is the model ID forwarded as provider.Request.Model.
	// Required.
	Model string

	// MaxIterations bounds the number of model<->tools cycles. The
	// loop terminates with the current state once the cap is hit,
	// even if the model would have asked for more tool calls.
	// Default 10.
	MaxIterations int

	// Optional sampling parameters forwarded to provider.Request.
	Temperature *float64
	TopP        *float64
	MaxTokens   *int

	// StopSequences, when non-nil, is forwarded as
	// provider.Request.StopSequences.
	StopSequences []string

	// ForceToolUse, when true, sets provider.Request.ToolChoice to
	// ToolChoiceRequired so the first model turn must invoke a tool.
	// Useful for "always answer through tools" agents; default is
	// ToolChoiceAuto when Tools is set, none otherwise.
	ForceToolUse bool

	// InputGuards validate each user message before it first reaches
	// the model: the seed conversation on the first turn, plus any user
	// messages appended before a later re-invocation with a carried-over
	// State. They run in order; the first non-nil error blocks the run
	// (matchable with errors.Is(err, guardrail.ErrBlocked)). Optional.
	InputGuards []guardrail.InputGuard

	// OutputGuards validate every assistant message produced by the
	// model before it is recorded or returned. They run in order; the
	// first non-nil error blocks the run. Optional.
	OutputGuards []guardrail.OutputGuard
}

func (cfg *Config) validate() error {
	if cfg.Provider == nil {
		return errors.New("agent: Config.Provider is required")
	}
	if cfg.Model == "" {
		return errors.New("agent: Config.Model is required")
	}
	// Capability-aware validation: catch obvious mismatches at
	// construction instead of waiting for the first provider call
	// to fail with a less informative error.
	caps := cfg.Provider.Capabilities()
	if cfg.Tools != nil && !caps.ToolCalling {
		return fmt.Errorf("agent: provider %q does not support tool calling (Capabilities.ToolCalling=false) but Config.Tools is set",
			cfg.Provider.Name())
	}
	if cfg.ForceToolUse && cfg.Tools == nil {
		return errors.New("agent: Config.ForceToolUse=true requires Config.Tools to be set")
	}
	if cfg.MaxIterations < 0 {
		return fmt.Errorf("agent: Config.MaxIterations must be >= 0 (got %d); use 0 for the default", cfg.MaxIterations)
	}
	return nil
}

// NewReAct compiles a graph.Runnable[State] that implements the
// classic ReAct loop:
//
//	START -> model
//	model -> (tool calls?  tools  :  END)
//	tools -> model
//
// The returned Runnable can be driven exactly like any other:
// Invoke / Stream / InvokeWith / Resume all work, so checkpointing,
// streaming and human-in-the-loop integration come for free.
func NewReAct(cfg Config) (*graph.Runnable[State], error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 10
	}

	var toolDefs []schema.ToolDef
	if cfg.Tools != nil {
		defs, err := cfg.Tools.ToolDefs()
		if err != nil {
			return nil, fmt.Errorf("agent: build tool defs: %w", err)
		}
		toolDefs = defs
	}

	modelNode := func(ctx context.Context, s State) (State, error) {
		// Vet every user message appended since the InputGuarded
		// watermark: the seed conversation on the first turn, and any
		// user turns the caller added before re-invoking with a
		// carried-over State. Tool-result and assistant messages are
		// not input, so mid-run turns add nothing to check.
		if s.InputGuarded > len(s.Messages) {
			s.InputGuarded = len(s.Messages)
		}
		for _, m := range s.Messages[s.InputGuarded:] {
			if m.Role != schema.RoleUser {
				continue
			}
			if err := guardrail.CheckInput(ctx, cfg.InputGuards, m); err != nil {
				return s, err
			}
		}
		s.InputGuarded = len(s.Messages)

		req := provider.Request{
			Model:         cfg.Model,
			Messages:      s.Messages,
			Tools:         toolDefs,
			Temperature:   cfg.Temperature,
			TopP:          cfg.TopP,
			MaxTokens:     cfg.MaxTokens,
			StopSequences: cfg.StopSequences,
		}
		switch {
		case cfg.ForceToolUse && len(toolDefs) > 0:
			req.ToolChoice = provider.ToolChoiceRequired
		case len(toolDefs) > 0:
			req.ToolChoice = provider.ToolChoiceAuto
		}

		resp, err := cfg.Provider.Generate(ctx, req)
		if err != nil {
			return s, fmt.Errorf("agent: model: %w", err)
		}
		if err := guardrail.CheckOutput(ctx, cfg.OutputGuards, resp.Message); err != nil {
			return s, err
		}
		s.Messages = append(s.Messages, resp.Message)
		s.Iterations++
		if len(resp.Message.ToolCalls) == 0 {
			s.FinalText = resp.Message.Text()
		} else if s.Iterations >= maxIter {
			// Cap reached with tool calls still pending: the router will
			// END this cycle without executing the tools. Flag the
			// truncation and surface any text the model did produce, so
			// callers don't read the empty FinalText as a clean answer.
			s.StoppedAtIterationCap = true
			s.FinalText = resp.Message.Text()
		}
		return s, nil
	}

	toolsNode := func(ctx context.Context, s State) (State, error) {
		if cfg.Tools == nil {
			return s, errors.New("agent: tools node reached but Config.Tools is nil")
		}
		if len(s.Messages) == 0 {
			return s, errors.New("agent: tools node reached with empty Messages")
		}
		last := s.Messages[len(s.Messages)-1]
		if last.Role != schema.RoleAssistant || len(last.ToolCalls) == 0 {
			return s, errors.New("agent: tools node reached without pending tool calls")
		}
		results := tool.ExecuteCalls(ctx, cfg.Tools, last.ToolCalls)
		s.Messages = append(s.Messages, tool.AsToolResultMessages(results)...)
		return s, nil
	}

	// Router: terminate when the model produced no tool calls or
	// when the iteration cap is reached. Otherwise, dispatch.
	router := func(s State) string {
		if s.Iterations >= maxIter {
			return graph.END
		}
		if len(s.Messages) == 0 {
			return graph.END
		}
		last := s.Messages[len(s.Messages)-1]
		if last.Role == schema.RoleAssistant && len(last.ToolCalls) > 0 {
			return "tools"
		}
		return graph.END
	}

	g := graph.New[State]().
		AddNode("model", modelNode).
		AddEdge(graph.START, "model").
		AddConditionalEdge("model", router)
	// Add the tools node only when a registry is configured; without
	// it the conditional router never returns "tools".
	if cfg.Tools != nil {
		g = g.AddNode("tools", toolsNode).
			AddEdge("tools", "model")
	}

	// Each model<->tools cycle is two node hops, plus the terminal
	// hop to END. Allow some headroom (3x) so the cap is unambiguous
	// in the runtime's step counter — the soft cap inside the
	// router is what users actually feel.
	r, err := g.Compile()
	if err != nil {
		return nil, err
	}
	r.MaxSteps = maxIter*3 + 4
	return r, nil
}

// Run is the one-shot convenience wrapper. It builds an initial
// State from optional system prompts plus the user input, invokes a
// fresh ReAct runnable, and returns the final assistant text.
//
// For multi-turn chat, mid-run pauses, streaming or checkpointing,
// build the Runnable directly via NewReAct and drive it like any
// other graph.
func Run(ctx context.Context, cfg Config, input string, system ...string) (string, error) {
	r, err := NewReAct(cfg)
	if err != nil {
		return "", err
	}
	msgs := make([]schema.Message, 0, 1+len(system))
	for _, s := range system {
		if s == "" {
			continue
		}
		msgs = append(msgs, schema.SystemMessage(s))
	}
	msgs = append(msgs, schema.UserMessage(input))
	final, err := r.Invoke(ctx, State{Messages: msgs})
	if err != nil {
		return final.FinalText, err
	}
	if final.StoppedAtIterationCap {
		return final.FinalText, ErrMaxIterations
	}
	return final.FinalText, nil
}
