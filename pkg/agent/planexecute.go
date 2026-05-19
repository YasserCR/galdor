package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/YasserCR/galdor/pkg/graph"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
	"github.com/YasserCR/galdor/pkg/tool"
)

// PlanExecuteState is the value that flows through the Plan-and-Execute
// graph. Construct it with the user Input and pass it to Invoke / Stream.
// The runtime fills in Plan after the first turn, then appends to Past as
// each step is executed, and finally sets Final when the loop terminates.
type PlanExecuteState struct {
	// Input is the original user request.
	Input string

	// Plan is the list of remaining steps to execute, in order. The
	// first element is the next step to run.
	Plan []string

	// Past records each executed step and the executor's textual
	// result, in the order they were run.
	Past []StepRecord

	// Final, when non-empty, terminates the loop with this text as
	// the agent's answer.
	Final string

	// Iter counts how many full plan / execute / replan cycles have
	// elapsed.
	Iter int
}

// StepRecord is one entry in PlanExecuteState.Past.
type StepRecord struct {
	Step   string
	Result string
}

// PlanExecuteConfig configures the Plan-and-Execute loop.
//
// The planner produces an initial multi-step plan. Each step is run by
// an inner ReAct sub-agent. After each step, the replanner decides to
// continue with the remaining steps, replace the plan, or finish with
// a final answer.
type PlanExecuteConfig struct {
	// Provider serves every LLM call (planner, executor, replanner)
	// unless an override below is set. Required.
	Provider provider.Provider

	// PlannerProvider, ExecutorProvider, ReplannerProvider, when
	// non-nil, override Provider for the corresponding role.
	PlannerProvider   provider.Provider
	ExecutorProvider  provider.Provider
	ReplannerProvider provider.Provider

	// Model is the model ID used by every role unless overridden.
	// Required.
	Model string

	// PlannerModel, ExecutorModel, ReplannerModel, when non-empty,
	// override Model for the corresponding role.
	PlannerModel   string
	ExecutorModel  string
	ReplannerModel string

	// Tools is the registry the executor sub-agent uses. Optional;
	// when nil, the executor runs without tools.
	Tools *tool.Registry

	// MaxIterations caps the number of plan / execute / replan
	// cycles. The loop terminates with whatever Final is set on the
	// state once the cap is hit. Default 8.
	MaxIterations int

	// MaxStepIterations caps the inner ReAct loop per step. Default 6.
	MaxStepIterations int

	// PlannerPrompt and ReplannerPrompt override the built-in system
	// prompts. The defaults instruct the LLM to emit strict JSON; if
	// you override them, the parser still expects a JSON array
	// (planner) or a JSON object with "plan" and "final" fields
	// (replanner).
	PlannerPrompt   string
	ReplannerPrompt string
}

func (cfg *PlanExecuteConfig) validate() error {
	if cfg.Provider == nil && (cfg.PlannerProvider == nil || cfg.ExecutorProvider == nil || cfg.ReplannerProvider == nil) {
		return errors.New("agent: PlanExecuteConfig.Provider is required (or override every role)")
	}
	if cfg.Model == "" {
		if cfg.PlannerModel == "" || cfg.ExecutorModel == "" || cfg.ReplannerModel == "" {
			return errors.New("agent: PlanExecuteConfig.Model is required (or override every role)")
		}
	}
	return nil
}

func (cfg *PlanExecuteConfig) plannerProvider() provider.Provider {
	if cfg.PlannerProvider != nil {
		return cfg.PlannerProvider
	}
	return cfg.Provider
}
func (cfg *PlanExecuteConfig) executorProvider() provider.Provider {
	if cfg.ExecutorProvider != nil {
		return cfg.ExecutorProvider
	}
	return cfg.Provider
}
func (cfg *PlanExecuteConfig) replannerProvider() provider.Provider {
	if cfg.ReplannerProvider != nil {
		return cfg.ReplannerProvider
	}
	return cfg.Provider
}
func (cfg *PlanExecuteConfig) plannerModel() string {
	if cfg.PlannerModel != "" {
		return cfg.PlannerModel
	}
	return cfg.Model
}
func (cfg *PlanExecuteConfig) executorModel() string {
	if cfg.ExecutorModel != "" {
		return cfg.ExecutorModel
	}
	return cfg.Model
}
func (cfg *PlanExecuteConfig) replannerModel() string {
	if cfg.ReplannerModel != "" {
		return cfg.ReplannerModel
	}
	return cfg.Model
}

const defaultPlannerPrompt = `You are a planning agent. Given a user request, produce a short, ordered plan of concrete steps that, executed in order, will answer the request.

Output ONLY a JSON array of strings. No prose, no markdown, no code fences.
Each string is one step. Keep the plan short (1-5 steps). Do not number the steps; the array order is the order.

Example output: ["look up the population of Quito", "compute the answer"]`

const defaultReplannerPrompt = `You are a replanning agent. You will be shown the original user request, the steps already executed (each with its observed result), and the remaining plan. Decide one of:

1. Continue: the remaining plan is still correct. Repeat it back as-is.
2. Revise: replace the remaining plan with a different list of steps.
3. Finish: there is enough information to answer; produce the final answer.

Output ONLY a JSON object of the form:
  {"plan": ["next step", "..."], "final": ""}
If "final" is a non-empty string, the loop terminates and returns that text as the answer; "plan" is ignored.
If "final" is empty, the loop continues with "plan" as the new remaining steps. If "plan" is also empty, the loop terminates with no answer.
No prose, no markdown, no code fences.`

// NewPlanAndExecute compiles a graph.Runnable[PlanExecuteState] that
// implements the Plan-and-Execute pattern:
//
//	START -> plan -> execute -> replan -> (execute | END)
//
// The planner emits an initial JSON list of steps. The executor runs
// each step using an inner ReAct sub-agent with the configured Tools.
// The replanner, after every step, decides to continue, revise the
// remaining plan, or finish with a final answer.
//
// The returned Runnable can be driven exactly like any other graph
// (Invoke, Stream, InvokeWith, Resume), so checkpointing and streaming
// integrate the same way as ReAct.
func NewPlanAndExecute(cfg PlanExecuteConfig) (*graph.Runnable[PlanExecuteState], error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 8
	}
	maxStep := cfg.MaxStepIterations
	if maxStep <= 0 {
		maxStep = 6
	}

	plannerPrompt := cfg.PlannerPrompt
	if plannerPrompt == "" {
		plannerPrompt = defaultPlannerPrompt
	}
	replannerPrompt := cfg.ReplannerPrompt
	if replannerPrompt == "" {
		replannerPrompt = defaultReplannerPrompt
	}

	planNode := func(ctx context.Context, s PlanExecuteState) (PlanExecuteState, error) {
		if s.Input == "" {
			return s, errors.New("agent: PlanExecuteState.Input is empty")
		}
		resp, err := cfg.plannerProvider().Generate(ctx, provider.Request{
			Model: cfg.plannerModel(),
			Messages: []schema.Message{
				schema.SystemMessage(plannerPrompt),
				schema.UserMessage(s.Input),
			},
		})
		if err != nil {
			return s, fmt.Errorf("agent: planner: %w", err)
		}
		plan, err := parsePlan(resp.Message.Text())
		if err != nil {
			return s, fmt.Errorf("agent: planner output: %w", err)
		}
		s.Plan = plan
		return s, nil
	}

	executeNode := func(ctx context.Context, s PlanExecuteState) (PlanExecuteState, error) {
		if len(s.Plan) == 0 {
			return s, errors.New("agent: execute node reached with empty Plan")
		}
		step := s.Plan[0]
		s.Plan = s.Plan[1:]

		reactCfg := Config{
			Provider:      cfg.executorProvider(),
			Tools:         cfg.Tools,
			Model:         cfg.executorModel(),
			MaxIterations: maxStep,
		}
		// Build the per-step prompt: original input + completed steps
		// for context + the current step. The executor sub-agent
		// shouldn't replan; it just produces a result for this step.
		sysPrompt := "You are executing one step of a larger plan. Use tools if helpful. Respond with the result of THIS step only; do not announce the next step."
		ctxBuilder := strings.Builder{}
		ctxBuilder.WriteString("Original request: ")
		ctxBuilder.WriteString(s.Input)
		if len(s.Past) > 0 {
			ctxBuilder.WriteString("\n\nSteps already completed:\n")
			for i, r := range s.Past {
				fmt.Fprintf(&ctxBuilder, "  %d. %s -> %s\n", i+1, r.Step, r.Result)
			}
		}
		ctxBuilder.WriteString("\nCurrent step to execute: ")
		ctxBuilder.WriteString(step)

		out, err := Run(ctx, reactCfg, ctxBuilder.String(), sysPrompt)
		if err != nil {
			return s, fmt.Errorf("agent: execute step %q: %w", step, err)
		}
		s.Past = append(s.Past, StepRecord{Step: step, Result: out})
		return s, nil
	}

	replanNode := func(ctx context.Context, s PlanExecuteState) (PlanExecuteState, error) {
		s.Iter++

		userPayload := buildReplannerPayload(s)
		resp, err := cfg.replannerProvider().Generate(ctx, provider.Request{
			Model: cfg.replannerModel(),
			Messages: []schema.Message{
				schema.SystemMessage(replannerPrompt),
				schema.UserMessage(userPayload),
			},
		})
		if err != nil {
			return s, fmt.Errorf("agent: replanner: %w", err)
		}
		plan, final, err := parseReplan(resp.Message.Text())
		if err != nil {
			return s, fmt.Errorf("agent: replanner output: %w", err)
		}
		if final != "" {
			s.Final = final
			s.Plan = nil
			return s, nil
		}
		s.Plan = plan
		return s, nil
	}

	router := func(s PlanExecuteState) string {
		if s.Final != "" {
			return graph.END
		}
		if s.Iter >= maxIter {
			return graph.END
		}
		if len(s.Plan) == 0 {
			return graph.END
		}
		return "execute"
	}

	g := graph.New[PlanExecuteState]().
		AddNode("plan", planNode).
		AddNode("execute", executeNode).
		AddNode("replan", replanNode).
		AddEdge(graph.START, "plan").
		AddEdge("plan", "execute").
		AddEdge("execute", "replan").
		AddConditionalEdge("replan", router)

	r, err := g.Compile()
	if err != nil {
		return nil, err
	}
	// Step budget: plan + (execute + replan) * maxIter + END. Triple
	// the bound for headroom; the soft cap in the router is the one
	// users feel.
	r.MaxSteps = maxIter*4 + 6
	return r, nil
}

// RunPlanAndExecute is the one-shot convenience wrapper, analogous to
// Run for ReAct. It builds a fresh Runnable, invokes it with input
// and returns the Final text from the resulting state.
func RunPlanAndExecute(ctx context.Context, cfg PlanExecuteConfig, input string) (string, error) {
	r, err := NewPlanAndExecute(cfg)
	if err != nil {
		return "", err
	}
	final, err := r.Invoke(ctx, PlanExecuteState{Input: input})
	if err != nil {
		return final.Final, err
	}
	return final.Final, nil
}

// parsePlan extracts a JSON array of strings from raw LLM output.
// Surrounding prose or ```json fences are tolerated. An empty array
// is a valid plan (the loop will terminate immediately).
func parsePlan(raw string) ([]string, error) {
	body := stripFences(raw)
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, errors.New("empty planner response")
	}
	// Trim any leading prose before the first '['.
	if i := strings.IndexByte(body, '['); i > 0 {
		body = body[i:]
	}
	// Trim any trailing prose after the last ']'.
	if j := strings.LastIndexByte(body, ']'); j >= 0 && j < len(body)-1 {
		body = body[:j+1]
	}
	var plan []string
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		return nil, fmt.Errorf("not a JSON array of strings: %w", err)
	}
	return plan, nil
}

// parseReplan extracts a {"plan": [...], "final": ""} object from raw
// LLM output. The two return values are (plan, final, err).
func parseReplan(raw string) ([]string, string, error) {
	body := stripFences(raw)
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, "", errors.New("empty replanner response")
	}
	if i := strings.IndexByte(body, '{'); i > 0 {
		body = body[i:]
	}
	if j := strings.LastIndexByte(body, '}'); j >= 0 && j < len(body)-1 {
		body = body[:j+1]
	}
	var v struct {
		Plan  []string `json:"plan"`
		Final string   `json:"final"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return nil, "", fmt.Errorf("not a JSON {plan,final} object: %w", err)
	}
	return v.Plan, v.Final, nil
}

// stripFences removes leading/trailing ```json ... ``` (or plain ```)
// fences, which LLMs add despite instructions not to.
func stripFences(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return s
	}
	// Drop the opening fence (line including ```json or ```).
	if nl := strings.IndexByte(t, '\n'); nl >= 0 {
		t = t[nl+1:]
	} else {
		t = strings.TrimPrefix(t, "```")
	}
	// Drop the closing fence.
	if i := strings.LastIndex(t, "```"); i >= 0 {
		t = t[:i]
	}
	return t
}

// buildReplannerPayload formats the state for the replanner LLM.
func buildReplannerPayload(s PlanExecuteState) string {
	var b strings.Builder
	b.WriteString("Original request:\n")
	b.WriteString(s.Input)
	b.WriteString("\n\nSteps already completed:\n")
	if len(s.Past) == 0 {
		b.WriteString("  (none)\n")
	} else {
		for i, r := range s.Past {
			fmt.Fprintf(&b, "  %d. %s -> %s\n", i+1, r.Step, r.Result)
		}
	}
	b.WriteString("\nRemaining plan:\n")
	if len(s.Plan) == 0 {
		b.WriteString("  (none)\n")
	} else {
		for i, step := range s.Plan {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, step)
		}
	}
	return b.String()
}
