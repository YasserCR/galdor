package council

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/YasserCR/galdor/pkg/graph"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// Worker is the routable unit a Supervisor dispatches to. Workers are
// plain Go functions so a user can wrap anything — a ReAct runnable,
// a deterministic function, a sub-Supervisor, an external HTTP call —
// behind the same interface.
//
// Run receives the task the supervisor decided to delegate (a natural-
// language instruction synthesized by the routing LLM) and returns a
// text answer. Errors halt the supervisor's run.
type Worker struct {
	// Name uniquely identifies the worker within a Supervisor. Used
	// as the graph-node name and as the value the routing LLM emits
	// to select this worker; must match [a-zA-Z0-9_-]+ so it
	// survives JSON and a few downstream contexts cleanly.
	Name string

	// Description is shown to the routing LLM verbatim. Make it
	// specific: "looks up factual information" beats "research".
	Description string

	// Run executes the worker. The supervisor wraps panics, but
	// returning an error is the canonical failure mode.
	Run func(ctx context.Context, task string) (string, error)
}

// SupervisorConfig configures the Supervisor loop.
type SupervisorConfig struct {
	// Provider is the routing LLM. Required.
	Provider provider.Provider

	// Model is the routing model ID. Required.
	Model string

	// Workers is the set of callable workers. Must be non-empty;
	// names must be unique.
	Workers []Worker

	// MaxHops caps the number of supervisor → worker → supervisor
	// cycles. Default 8.
	MaxHops int

	// SystemPrompt overrides the built-in routing system prompt. The
	// custom prompt must still instruct the LLM to emit the same
	// strict JSON shape ({"worker": "...", "task": "..."} or
	// {"final": "..."}) — otherwise parsing will fail.
	SystemPrompt string
}

// SupervisorState is the value that flows through the supervisor
// graph. Construct it with Input set; the runtime populates the rest.
type SupervisorState struct {
	// Input is the original user request.
	Input string

	// History records every supervisor → worker invocation in order.
	History []WorkerInvocation

	// Final, when non-empty, terminates the loop and is returned as
	// the supervisor's answer.
	Final string

	// Hops counts how many times the supervisor LLM has been
	// consulted.
	Hops int

	// Next is the worker selected by the supervisor for the next
	// turn. Internal to the graph; callers can ignore it.
	Next string

	// NextTask is the task string the supervisor decided to delegate
	// alongside Next. Internal.
	NextTask string
}

// WorkerInvocation is one row of SupervisorState.History.
type WorkerInvocation struct {
	Worker string
	Task   string
	Output string
}

const defaultSupervisorPrompt = `You are a routing supervisor coordinating specialized workers.

Each turn, decide one of:
1. Delegate to a worker: respond with {"worker": "name", "task": "what to do"}.
   The worker's answer will come back to you next turn.
2. Finish: respond with {"final": "your final answer to the user"}.

Pick the worker whose Description best matches the next step. Keep "task"
short and specific — the worker only sees that string, not the full history.
If the user's request is fully addressed, finish.

Respond with ONLY a JSON object. No prose, no markdown, no code fences.`

// NewSupervisor compiles a graph.Runnable[SupervisorState] that
// implements the supervisor pattern: a router LLM picks one of the
// configured workers each turn, sees the worker's answer the next
// turn, and finishes when the user's request is satisfied.
//
// The graph shape is:
//
//	START -> supervisor -> (worker_1 | worker_2 | ... | END)
//	worker_n -> supervisor
//
// Workers run inside the same goroutine as the supervisor by default;
// they can spawn their own concurrency internally. The supervisor
// graph integrates with checkpointing, streaming and observability
// the same way any other compiled graph does.
func NewSupervisor(cfg SupervisorConfig) (*graph.Runnable[SupervisorState], error) {
	if cfg.Provider == nil {
		return nil, errors.New("council: SupervisorConfig.Provider is required")
	}
	if cfg.Model == "" {
		return nil, errors.New("council: SupervisorConfig.Model is required")
	}
	if len(cfg.Workers) == 0 {
		return nil, errors.New("council: SupervisorConfig.Workers must be non-empty")
	}
	seen := make(map[string]struct{}, len(cfg.Workers))
	for _, w := range cfg.Workers {
		if w.Name == "" {
			return nil, errors.New("council: Worker.Name is empty")
		}
		if !isSafeWorkerName(w.Name) {
			return nil, fmt.Errorf("council: Worker.Name %q must match [a-zA-Z0-9_-]+", w.Name)
		}
		if w.Name == graph.START || w.Name == graph.END || w.Name == "supervisor" {
			return nil, fmt.Errorf("council: Worker.Name %q is reserved", w.Name)
		}
		if w.Run == nil {
			return nil, fmt.Errorf("council: Worker %q has nil Run", w.Name)
		}
		if _, dup := seen[w.Name]; dup {
			return nil, fmt.Errorf("council: duplicate Worker.Name %q", w.Name)
		}
		seen[w.Name] = struct{}{}
	}

	maxHops := cfg.MaxHops
	if maxHops <= 0 {
		maxHops = 8
	}
	sysPrompt := cfg.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = defaultSupervisorPrompt
	}

	supervisorNode := func(ctx context.Context, s SupervisorState) (SupervisorState, error) {
		s.Hops++
		userPayload := buildSupervisorPayload(s, cfg.Workers)
		resp, err := cfg.Provider.Generate(ctx, provider.Request{
			Model: cfg.Model,
			Messages: []schema.Message{
				schema.SystemMessage(sysPrompt),
				schema.UserMessage(userPayload),
			},
		})
		if err != nil {
			return s, fmt.Errorf("council: supervisor LLM: %w", err)
		}
		decision, err := parseSupervisorDecision(resp.Message.Text())
		if err != nil {
			return s, fmt.Errorf("council: supervisor decision: %w", err)
		}
		if decision.Final != "" {
			s.Final = decision.Final
			s.Next = ""
			s.NextTask = ""
			return s, nil
		}
		if _, known := seen[decision.Worker]; !known {
			return s, fmt.Errorf("council: supervisor chose unknown worker %q", decision.Worker)
		}
		s.Next = decision.Worker
		s.NextTask = decision.Task
		return s, nil
	}

	router := func(s SupervisorState) string {
		if s.Final != "" {
			return graph.END
		}
		if s.Hops >= maxHops {
			return graph.END
		}
		if s.Next == "" {
			return graph.END
		}
		return s.Next
	}

	g := graph.New[SupervisorState]().
		AddNode("supervisor", supervisorNode).
		AddEdge(graph.START, "supervisor").
		AddConditionalEdge("supervisor", router)

	for _, w := range cfg.Workers {
		w := w // capture
		g = g.
			AddNode(w.Name, func(ctx context.Context, s SupervisorState) (SupervisorState, error) {
				out, err := w.Run(ctx, s.NextTask)
				if err != nil {
					return s, fmt.Errorf("council: worker %q: %w", w.Name, err)
				}
				s.History = append(s.History, WorkerInvocation{
					Worker: w.Name,
					Task:   s.NextTask,
					Output: out,
				})
				// Clear the next slot so a malformed supervisor
				// decision next turn fails loudly instead of silently
				// re-routing to the same worker.
				s.Next = ""
				s.NextTask = ""
				return s, nil
			}).
			AddEdge(w.Name, "supervisor")
	}

	r, err := g.Compile()
	if err != nil {
		return nil, err
	}
	// Each hop is supervisor + worker + back-edge = 3 transitions.
	r.MaxSteps = maxHops*3 + 4
	return r, nil
}

// RunSupervisor is the one-shot convenience wrapper.
func RunSupervisor(ctx context.Context, cfg SupervisorConfig, input string) (string, error) {
	r, err := NewSupervisor(cfg)
	if err != nil {
		return "", err
	}
	final, err := r.Invoke(ctx, SupervisorState{Input: input})
	if err != nil {
		return final.Final, err
	}
	return final.Final, nil
}

// supervisorDecision is the parsed shape of the routing LLM's reply.
type supervisorDecision struct {
	Worker string `json:"worker"`
	Task   string `json:"task"`
	Final  string `json:"final"`
}

func parseSupervisorDecision(raw string) (supervisorDecision, error) {
	body := stripFences(raw)
	body = strings.TrimSpace(body)
	if body == "" {
		return supervisorDecision{}, errors.New("empty supervisor response")
	}
	if i := strings.IndexByte(body, '{'); i > 0 {
		body = body[i:]
	}
	if j := strings.LastIndexByte(body, '}'); j >= 0 && j < len(body)-1 {
		body = body[:j+1]
	}
	var d supervisorDecision
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		return supervisorDecision{}, fmt.Errorf("not a JSON {worker,task,final} object: %w", err)
	}
	if d.Final == "" && d.Worker == "" {
		return supervisorDecision{}, errors.New("supervisor response is neither a delegation nor a final answer")
	}
	return d, nil
}

func buildSupervisorPayload(s SupervisorState, workers []Worker) string {
	var b strings.Builder
	b.WriteString("Workers available:\n")
	for _, w := range workers {
		fmt.Fprintf(&b, "  - %s: %s\n", w.Name, w.Description)
	}
	b.WriteString("\nUser request:\n")
	b.WriteString(s.Input)
	b.WriteString("\n\nWork completed so far:\n")
	if len(s.History) == 0 {
		b.WriteString("  (none)\n")
	} else {
		for i, h := range s.History {
			fmt.Fprintf(&b, "  %d. [%s] task=%q -> %s\n", i+1, h.Worker, h.Task, h.Output)
		}
	}
	return b.String()
}

func isSafeWorkerName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// stripFences removes ```json ... ``` (or plain ```) fences that LLMs
// add despite instructions not to. Shared with the Plan-and-Execute
// helper; copied here so pkg/council doesn't import pkg/agent (the
// dependency direction matters for the long-term layering).
func stripFences(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return s
	}
	if nl := strings.IndexByte(t, '\n'); nl >= 0 {
		t = t[nl+1:]
	} else {
		t = strings.TrimPrefix(t, "```")
	}
	if i := strings.LastIndex(t, "```"); i >= 0 {
		t = t[:i]
	}
	return t
}
