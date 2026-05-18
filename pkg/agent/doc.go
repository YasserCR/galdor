// Package agent ships high-level agent helpers built on pkg/graph,
// pkg/provider and pkg/tool.
//
// The flagship is NewReAct, which compiles a ReAct loop (model →
// tools → model → ...) into a graph.Runnable[State] you can Invoke,
// Stream, checkpoint or Resume just like any other graph.
//
//	cfg := agent.Config{
//	    Provider: anthropic.MustNew(anthropic.Config{APIKey: ...}),
//	    Tools:    registry,
//	    Model:    "claude-haiku-4-5",
//	}
//	answer, err := agent.Run(ctx, cfg, "What's the weather in Quito?",
//	    "Use tools when helpful.")
//
// Construction returns an error rather than panicking, so configs
// can be validated at startup. Run is the one-shot wrapper; for
// finer control over the conversation (multi-turn chat, mid-run
// pauses, etc.) call NewReAct and drive the Runnable directly.
//
// PlanAndExecute, Reflexion and Supervisor helpers are tracked for
// follow-up sessions; ReAct is the loop that covers the most ground.
package agent
