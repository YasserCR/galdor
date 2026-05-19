package council

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
	"github.com/YasserCR/galdor/pkg/tool"
)

// perAgentProvider routes Generate calls by sniffing the system
// prompt: each agent's prompt embeds its name, so we can return the
// right script for the right agent.
type perAgentProvider struct {
	plans map[string][]schema.Message
	calls map[string]int
}

func (*perAgentProvider) Name() string { return "per-agent-scripted" }
func (*perAgentProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{ToolCalling: true}
}
func (*perAgentProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}

func (p *perAgentProvider) Generate(_ context.Context, req provider.Request) (*provider.Response, error) {
	var sys string
	for _, m := range req.Messages {
		if m.Role == schema.RoleSystem {
			sys = m.Text()
			break
		}
	}
	// Find which agent owns this system prompt.
	for name, script := range p.plans {
		if strings.Contains(sys, "agent "+name) || strings.Contains(sys, "I am "+name) {
			idx := p.calls[name]
			if idx >= len(script) {
				return nil, errInvalid("plan exhausted for agent " + name)
			}
			p.calls[name] = idx + 1
			return &provider.Response{
				Message:    script[idx],
				StopReason: schema.StopReasonEndTurn,
				Model:      "scripted-1",
			}, nil
		}
	}
	return nil, errInvalid("no plan matched system prompt: " + sys)
}

type errString string

func (e errString) Error() string { return string(e) }
func errInvalid(s string) error   { return errString(s) }

func handoffCall(target, task string) schema.Message {
	args := []byte(`{}`)
	if task != "" {
		args, _ = json.Marshal(map[string]string{"task": task})
	}
	return schema.Message{
		Role: schema.RoleAssistant,
		ToolCalls: []schema.ToolCall{{
			ID: "h1", Name: handoffToolName(target), Arguments: args,
		}},
	}
}

func TestSwarm_HandoffTransfersControl(t *testing.T) {
	t.Parallel()
	p := &perAgentProvider{
		plans: map[string][]schema.Message{
			"researcher": {handoffCall("writer", "summarize the findings")},
			"writer":     {schema.AssistantMessage("Here is the summary.")},
		},
		calls: map[string]int{},
	}

	researcher := &SwarmAgent{
		Name:        "researcher",
		Description: "looks up facts",
		Provider:    p,
		Model:       "x",
		Handoffs:    []string{"writer"},
	}
	writer := &SwarmAgent{
		Name:        "writer",
		Description: "writes summaries",
		Provider:    p,
		Model:       "x",
	}
	final, err := RunSwarm(context.Background(), SwarmConfig{
		Agents: []*SwarmAgent{researcher, writer},
		Start:  "researcher",
	}, "research the topic")
	if err != nil {
		t.Fatal(err)
	}
	if final != "Here is the summary." {
		t.Errorf("final = %q", final)
	}
	if p.calls["researcher"] != 1 {
		t.Errorf("researcher calls = %d, want 1", p.calls["researcher"])
	}
	if p.calls["writer"] != 1 {
		t.Errorf("writer calls = %d, want 1", p.calls["writer"])
	}
}

func TestSwarm_DomainToolThenFinal(t *testing.T) {
	t.Parallel()
	// Single agent uses a domain tool, then produces a final answer
	// without handing off. Tests that the inner loop executes tools.
	type addIn struct{ A, B int }
	type addOut struct{ Sum int }
	add, err := tool.NewTool("add", "add two numbers",
		func(_ context.Context, in addIn) (addOut, error) { return addOut{Sum: in.A + in.B}, nil })
	if err != nil {
		t.Fatal(err)
	}
	reg, err := tool.NewRegistry(add)
	if err != nil {
		t.Fatal(err)
	}

	p := &perAgentProvider{
		plans: map[string][]schema.Message{
			"solo": {
				{
					Role: schema.RoleAssistant,
					ToolCalls: []schema.ToolCall{{
						ID: "c1", Name: "add", Arguments: json.RawMessage(`{"a":2,"b":3}`),
					}},
				},
				schema.AssistantMessage("the sum is 5"),
			},
		},
		calls: map[string]int{},
	}
	agent := &SwarmAgent{
		Name:        "solo",
		Description: "does math",
		Provider:    p,
		Model:       "x",
		Tools:       reg,
	}
	final, err := RunSwarm(context.Background(), SwarmConfig{
		Agents: []*SwarmAgent{agent},
		Start:  "solo",
	}, "add 2 and 3")
	if err != nil {
		t.Fatal(err)
	}
	if final != "the sum is 5" {
		t.Errorf("final = %q", final)
	}
}

func TestSwarm_MaxHopsCap(t *testing.T) {
	t.Parallel()
	// Two agents that hand off to each other forever; the swarm
	// must terminate at MaxHops.
	p := &perAgentProvider{
		plans: map[string][]schema.Message{
			"a": {handoffCall("b", ""), handoffCall("b", ""), handoffCall("b", "")},
			"b": {handoffCall("a", ""), handoffCall("a", ""), handoffCall("a", "")},
		},
		calls: map[string]int{},
	}
	a := &SwarmAgent{Name: "a", Description: "x", Provider: p, Model: "x", Handoffs: []string{"b"}}
	b := &SwarmAgent{Name: "b", Description: "y", Provider: p, Model: "x", Handoffs: []string{"a"}}
	r, err := NewSwarm(SwarmConfig{
		Agents:  []*SwarmAgent{a, b},
		Start:   "a",
		MaxHops: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	final, err := r.Invoke(context.Background(), SwarmState{
		Messages: []schema.Message{schema.UserMessage("loop")},
		Active:   "a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if final.Hops != 3 {
		t.Errorf("Hops = %d, want 3 (capped)", final.Hops)
	}
	if final.Final != "" {
		t.Errorf("Final should be empty when capped: %q", final.Final)
	}
}

func TestSwarm_HandoffEmitsToolResult(t *testing.T) {
	t.Parallel()
	// After a handoff, the shared Messages must include a tool-result
	// message for the handoff tool — otherwise the receiving LLM
	// would see an orphaned tool_use block.
	p := &perAgentProvider{
		plans: map[string][]schema.Message{
			"researcher": {handoffCall("writer", "do it")},
			"writer":     {schema.AssistantMessage("done")},
		},
		calls: map[string]int{},
	}
	r, err := NewSwarm(SwarmConfig{
		Agents: []*SwarmAgent{
			{Name: "researcher", Description: "r", Provider: p, Model: "x", Handoffs: []string{"writer"}},
			{Name: "writer", Description: "w", Provider: p, Model: "x"},
		},
		Start: "researcher",
	})
	if err != nil {
		t.Fatal(err)
	}
	final, err := r.Invoke(context.Background(), SwarmState{
		Messages: []schema.Message{schema.UserMessage("go")},
		Active:   "researcher",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Locate the tool-result for the handoff in the shared log.
	found := false
	for _, m := range final.Messages {
		if m.Role == schema.RoleTool && strings.Contains(m.Text(), "handed off to writer") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a tool-result acknowledging the handoff in final.Messages: %+v", final.Messages)
	}
}

func TestSwarm_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	good := &SwarmAgent{Name: "a", Description: "x", Provider: &scriptedProvider{}, Model: "x"}
	cases := map[string]SwarmConfig{
		"no agents":               {},
		"missing start":           {Agents: []*SwarmAgent{good}},
		"unknown start":           {Agents: []*SwarmAgent{good}, Start: "ghost"},
		"handoff to self":         {Agents: []*SwarmAgent{{Name: "a", Description: "x", Provider: &scriptedProvider{}, Model: "x", Handoffs: []string{"a"}}}, Start: "a"},
		"handoff to ghost":        {Agents: []*SwarmAgent{{Name: "a", Description: "x", Provider: &scriptedProvider{}, Model: "x", Handoffs: []string{"ghost"}}}, Start: "a"},
		"duplicate agent name":    {Agents: []*SwarmAgent{good, {Name: "a", Description: "y", Provider: &scriptedProvider{}, Model: "x"}}, Start: "a"},
		"unsafe agent name":       {Agents: []*SwarmAgent{{Name: "bad name", Description: "x", Provider: &scriptedProvider{}, Model: "x"}}, Start: "bad name"},
		"agent with nil Provider": {Agents: []*SwarmAgent{{Name: "a", Description: "x", Model: "x"}}, Start: "a"},
		"agent with empty Model":  {Agents: []*SwarmAgent{{Name: "a", Description: "x", Provider: &scriptedProvider{}}}, Start: "a"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewSwarm(cfg); err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}
