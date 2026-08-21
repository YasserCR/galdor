// Package agent ships high-level agent helpers built on pkg/graph,
// pkg/provider and pkg/tool.
//
// NewReAct compiles a ReAct loop (model → tools → model → ...) into
// a graph.Runnable[State] you can Invoke, Stream, checkpoint or
// Resume just like any other graph.
//
//	cfg := agent.Config{
//	    Provider: anthropic.MustNew(anthropic.Config{APIKey: ...}),
//	    Tools:    registry,
//	    Model:    "claude-haiku-4-5",
//	}
//	answer, err := agent.Run(ctx, cfg, "What's the weather in Quito?",
//	    "Use tools when helpful.")
//
// NewPlanAndExecute compiles a Plan-and-Execute loop (plan → execute
// → replan → ... → END) into a graph.Runnable[PlanExecuteState]. The
// planner emits a JSON list of steps, the executor runs each step in
// an inner ReAct sub-agent, and the replanner decides after every
// step to continue, revise, or finish with a final answer.
//
//	final, err := agent.RunPlanAndExecute(ctx, agent.PlanExecuteConfig{
//	    Provider: p, Model: "claude-haiku-4-5", Tools: registry,
//	}, "research X then summarize")
//
// Both Config and PlanExecuteConfig accept optional guardrails (see
// pkg/guardrail). ReAct vets each user message before it first reaches
// the model — including messages appended between re-invocations of a
// carried-over State — and every assistant message the model produces.
// Plan-and-Execute vets the request before the planner, every executor
// assistant turn, and the final answer; the planner's and replanner's
// JSON emissions are not guarded.
//
// Construction returns an error rather than panicking, so configs
// can be validated at startup. Run / RunPlanAndExecute are one-shot
// wrappers; for finer control over the conversation (multi-turn
// chat, mid-run pauses, etc.) call the New* helpers and drive the
// Runnable directly.
//
// Reflexion and Supervisor helpers are tracked for follow-up
// sessions.
package agent
