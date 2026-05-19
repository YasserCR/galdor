package council

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/YasserCR/galdor/pkg/graph"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
	"github.com/YasserCR/galdor/pkg/tool"
)

// SwarmAgent is one peer in a Swarm. Each agent has its own LLM
// provider/model, its own domain tools, and a list of other agents
// it is permitted to hand off control to. Handoffs surface to the
// LLM as synthetic tools named "handoff_to_<name>" that the framework
// intercepts.
type SwarmAgent struct {
	// Name uniquely identifies the agent within a Swarm. Must match
	// [a-zA-Z0-9_-]+.
	Name string

	// Description is appended to the agent's system prompt for the
	// other agents to see ("you may hand off to <name>: <description>").
	// Keep it specific so the routing LLM picks the right peer.
	Description string

	// Provider is the LLM serving this agent. Required.
	Provider provider.Provider

	// Model is the model ID. Required.
	Model string

	// Tools is the agent's domain registry. Optional; nil means the
	// agent has only the synthetic handoff tools.
	Tools *tool.Registry

	// Handoffs lists the names of other Swarm agents this one may
	// transfer control to. The framework injects a handoff tool for
	// each. An empty list means the agent cannot hand off — its
	// final answer terminates the swarm.
	Handoffs []string

	// SystemPrompt overrides the built-in agent system prompt. The
	// framework still appends a description of available handoff
	// tools to whatever you provide.
	SystemPrompt string

	// MaxIterations bounds the inner model<->tools loop on each
	// activation. Default 6.
	MaxIterations int
}

// SwarmConfig configures the Swarm.
type SwarmConfig struct {
	Agents []*SwarmAgent

	// Start names the agent that handles the user's first message.
	// Must match one of Agents[].Name.
	Start string

	// MaxHops caps the number of times control is transferred between
	// agents (including the initial activation). Default 8.
	MaxHops int
}

// SwarmState is the value that flows through the swarm graph.
type SwarmState struct {
	// Messages is the shared conversation across all agents. Each
	// activation appends one or more assistant / tool messages.
	Messages []schema.Message

	// Active is the agent currently holding the conversation. The
	// runtime updates this on handoff.
	Active string

	// Hops counts handoffs (including the initial activation).
	Hops int

	// Final is set when an agent terminates the conversation with a
	// non-tool-call answer.
	Final string
}

// handoffToolName is the synthetic tool name generated for handoffs.
func handoffToolName(target string) string { return "handoff_to_" + target }

// handoffInput is the (empty) input schema we generate for handoff
// tools. We accept an optional "task" string so the relinquishing
// LLM can summarize what the receiving agent should do; the
// framework appends it to the shared history as a system note.
type handoffInput struct {
	Task string `json:"task,omitempty" jsonschema:"Optional brief description of what the receiving agent should do next"`
}

// NewSwarm compiles a graph.Runnable[SwarmState] that implements the
// swarm pattern: peer agents collaborate over a shared message log,
// and any agent can transfer control to another by calling a
// handoff_to_<name> tool.
//
// The graph shape is one node per agent. Each agent node runs a
// bounded inner ReAct-style loop: model → tools → model → ... until
// either (a) the model produces a final assistant text (terminate),
// or (b) the model calls a handoff tool (route to the named peer).
//
// The shared SwarmState.Messages is the canonical conversation; the
// receiving agent sees it verbatim when it takes over.
func NewSwarm(cfg SwarmConfig) (*graph.Runnable[SwarmState], error) {
	if len(cfg.Agents) == 0 {
		return nil, errors.New("council: SwarmConfig.Agents must be non-empty")
	}
	byName := make(map[string]*SwarmAgent, len(cfg.Agents))
	for _, a := range cfg.Agents {
		if a == nil {
			return nil, errors.New("council: SwarmConfig.Agents contains nil")
		}
		if !isSafeWorkerName(a.Name) {
			return nil, fmt.Errorf("council: SwarmAgent.Name %q must match [a-zA-Z0-9_-]+", a.Name)
		}
		if a.Name == graph.START || a.Name == graph.END {
			return nil, fmt.Errorf("council: SwarmAgent.Name %q is reserved", a.Name)
		}
		if a.Provider == nil {
			return nil, fmt.Errorf("council: SwarmAgent %q has nil Provider", a.Name)
		}
		if a.Model == "" {
			return nil, fmt.Errorf("council: SwarmAgent %q has empty Model", a.Name)
		}
		if _, dup := byName[a.Name]; dup {
			return nil, fmt.Errorf("council: duplicate SwarmAgent.Name %q", a.Name)
		}
		byName[a.Name] = a
	}
	if cfg.Start == "" {
		return nil, errors.New("council: SwarmConfig.Start is required")
	}
	if _, ok := byName[cfg.Start]; !ok {
		return nil, fmt.Errorf("council: SwarmConfig.Start %q is not a registered agent", cfg.Start)
	}
	for _, a := range cfg.Agents {
		for _, target := range a.Handoffs {
			if _, ok := byName[target]; !ok {
				return nil, fmt.Errorf("council: agent %q lists unknown handoff target %q", a.Name, target)
			}
			if target == a.Name {
				return nil, fmt.Errorf("council: agent %q cannot hand off to itself", a.Name)
			}
		}
	}

	maxHops := cfg.MaxHops
	if maxHops <= 0 {
		maxHops = 8
	}

	g := graph.New[SwarmState]()
	// Conditional entry: the START → first-active edge is wired
	// statically to cfg.Start; once we land on an agent node we use
	// a router to either terminate or hop to the next agent.
	for _, a := range cfg.Agents {
		// Go 1.22+ scopes loop variables per-iteration.
		g = g.AddNode(a.Name, makeSwarmAgentNode(a, byName, &maxHops))
		g = g.AddConditionalEdge(a.Name, makeSwarmRouter(maxHops, byName))
	}
	g = g.AddEdge(graph.START, cfg.Start)

	r, err := g.Compile()
	if err != nil {
		return nil, err
	}
	// Each handoff is one node transition; pad generously since the
	// router's terminal hop also counts.
	r.MaxSteps = maxHops*4 + 4
	return r, nil
}

// RunSwarm is the one-shot convenience wrapper. It builds a Swarm,
// seeds the conversation with the user's input and returns the final
// text once the swarm terminates.
func RunSwarm(ctx context.Context, cfg SwarmConfig, input string) (string, error) {
	r, err := NewSwarm(cfg)
	if err != nil {
		return "", err
	}
	state := SwarmState{
		Messages: []schema.Message{schema.UserMessage(input)},
		Active:   cfg.Start,
	}
	final, err := r.Invoke(ctx, state)
	if err != nil {
		return final.Final, err
	}
	return final.Final, nil
}

func makeSwarmRouter(maxHops int, byName map[string]*SwarmAgent) graph.Router[SwarmState] {
	return func(s SwarmState) string {
		if s.Final != "" {
			return graph.END
		}
		if s.Hops >= maxHops {
			return graph.END
		}
		if s.Active == "" {
			return graph.END
		}
		if _, ok := byName[s.Active]; !ok {
			return graph.END
		}
		return s.Active
	}
}

// makeSwarmAgentNode produces the NodeFunc that runs one activation
// of an agent — an inner ReAct loop that terminates on either a
// final assistant text or a handoff tool call.
func makeSwarmAgentNode(a *SwarmAgent, byName map[string]*SwarmAgent, _ *int) graph.NodeFunc[SwarmState] {
	maxIter := a.MaxIterations
	if maxIter <= 0 {
		maxIter = 6
	}
	return func(ctx context.Context, s SwarmState) (SwarmState, error) {
		s.Hops++
		// Build the augmented tool registry: domain tools + handoff tools.
		toolDefs, err := buildSwarmToolDefs(a)
		if err != nil {
			return s, fmt.Errorf("council: agent %q tool defs: %w", a.Name, err)
		}

		// The system prompt is the user-provided one plus a description
		// of the available handoff targets.
		sys := buildSwarmSystemPrompt(a, byName)
		// Build the messages to send: a system message (overwriting
		// any previous system slot from a different agent), then the
		// rest of the shared conversation.
		msgs := buildSwarmMessages(sys, s.Messages)

		for i := 0; i < maxIter; i++ {
			req := provider.Request{
				Model:    a.Model,
				Messages: msgs,
				Tools:    toolDefs,
			}
			if len(toolDefs) > 0 {
				req.ToolChoice = provider.ToolChoiceAuto
			}
			resp, err := a.Provider.Generate(ctx, req)
			if err != nil {
				return s, fmt.Errorf("council: agent %q LLM: %w", a.Name, err)
			}
			s.Messages = append(s.Messages, resp.Message)
			msgs = append(msgs, resp.Message)

			if len(resp.Message.ToolCalls) == 0 {
				// Final answer — terminate the swarm.
				s.Final = resp.Message.Text()
				s.Active = ""
				return s, nil
			}

			// Inspect tool calls for handoffs. A handoff short-circuits
			// the remaining tool calls — control goes to the target
			// agent on the next graph hop with the conversation
			// state intact.
			handoffTarget, handoffTask, isHandoff := detectHandoff(resp.Message.ToolCalls)
			if isHandoff {
				// Acknowledge the handoff call so the conversation
				// history stays consistent (tool calls without
				// matching results confuse Anthropic and OpenAI).
				results := acknowledgeHandoff(resp.Message.ToolCalls, handoffTarget, handoffTask)
				s.Messages = append(s.Messages, results...)
				s.Active = handoffTarget
				return s, nil
			}

			// Domain tool calls — execute them against the agent's
			// registry and append the results.
			if a.Tools == nil {
				return s, fmt.Errorf("council: agent %q produced tool calls but has nil Tools", a.Name)
			}
			toolResults := tool.ExecuteCalls(ctx, a.Tools, resp.Message.ToolCalls)
			resMsgs := tool.AsToolResultMessages(toolResults)
			s.Messages = append(s.Messages, resMsgs...)
			msgs = append(msgs, resMsgs...)
		}

		// Inner loop bailed without converging. Treat as terminal
		// failure for THIS activation but leave Final empty so the
		// caller can inspect Messages.
		return s, fmt.Errorf("council: agent %q exhausted MaxIterations (%d)", a.Name, maxIter)
	}
}

func buildSwarmToolDefs(a *SwarmAgent) ([]schema.ToolDef, error) {
	var defs []schema.ToolDef
	if a.Tools != nil {
		td, err := a.Tools.ToolDefs()
		if err != nil {
			return nil, err
		}
		defs = append(defs, td...)
	}
	for _, target := range a.Handoffs {
		defs = append(defs, schema.ToolDef{
			Name:        handoffToolName(target),
			Description: "Hand off control of the conversation to agent " + target + ". Use when " + target + " is better suited to the next step.",
			Schema:      handoffSchema(),
		})
	}
	return defs, nil
}

func handoffSchema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "task": {
      "type": "string",
      "description": "Optional brief description of what the receiving agent should do next."
    }
  },
  "additionalProperties": false
}`)
}

func buildSwarmSystemPrompt(a *SwarmAgent, byName map[string]*SwarmAgent) string {
	body := a.SystemPrompt
	if body == "" {
		body = "You are agent " + a.Name + " — " + a.Description + ".\nCollaborate with the other agents in the swarm. Use the available tools when helpful."
	}
	if len(a.Handoffs) == 0 {
		return body
	}
	body += "\n\nYou may hand off to:"
	for _, target := range a.Handoffs {
		peer := byName[target]
		body += "\n  - " + target
		if peer != nil && peer.Description != "" {
			body += ": " + peer.Description
		}
	}
	body += "\n\nTo hand off, call the corresponding handoff_to_<name> tool. Once you hand off you cannot speak again until you are handed back to."
	return body
}

// buildSwarmMessages prepends/replaces the system message before
// forwarding the shared conversation to the active agent. Earlier
// agents' system prompts are filtered out so the new agent sees
// only its own instructions.
func buildSwarmMessages(systemPrompt string, shared []schema.Message) []schema.Message {
	out := make([]schema.Message, 0, len(shared)+1)
	out = append(out, schema.SystemMessage(systemPrompt))
	for _, m := range shared {
		if m.Role == schema.RoleSystem {
			continue
		}
		out = append(out, m)
	}
	return out
}

func detectHandoff(calls []schema.ToolCall) (target, task string, ok bool) {
	const prefix = "handoff_to_"
	for _, c := range calls {
		if len(c.Name) > len(prefix) && c.Name[:len(prefix)] == prefix {
			target = c.Name[len(prefix):]
			task = extractHandoffTask(c.Arguments)
			return target, task, true
		}
	}
	return "", "", false
}

func extractHandoffTask(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var v handoffInput
	if err := json.Unmarshal(args, &v); err != nil {
		return ""
	}
	return v.Task
}

// acknowledgeHandoff produces ToolResultMessages for every tool call
// in the same turn as the handoff (the handoff itself + any other
// tool calls). The handoff's result includes the target name so the
// next agent sees an explicit transition marker in its history.
func acknowledgeHandoff(calls []schema.ToolCall, target, task string) []schema.Message {
	out := make([]schema.Message, 0, len(calls))
	for _, c := range calls {
		var body string
		if c.Name == handoffToolName(target) {
			body = fmt.Sprintf("Control handed off to %s.", target)
			if task != "" {
				body += " Task: " + task
			}
		} else {
			// Sibling tool calls made in the same turn as a handoff
			// are dropped — the receiving agent decides what to do
			// next. We still acknowledge them so the conversation
			// history is well-formed.
			body = fmt.Sprintf("Tool %q skipped because control was handed off to %s in the same turn.", c.Name, target)
		}
		out = append(out, schema.ToolResultMessage(c.ID, body))
	}
	return out
}
