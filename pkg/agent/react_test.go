package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
	"github.com/YasserCR/galdor/pkg/tool"
)

// scriptedProvider is a deterministic Provider for tests. The Plan
// field is a sequence of canned assistant messages — one per
// Generate call. After exhaustion it returns an error so a runaway
// loop fails the test loudly.
type scriptedProvider struct {
	Plan  []schema.Message
	calls atomic.Int32
}

func (*scriptedProvider) Name() string { return "scripted" }
func (*scriptedProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{ToolCalling: true}
}
func (*scriptedProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}

func (p *scriptedProvider) Generate(_ context.Context, _ provider.Request) (*provider.Response, error) {
	idx := int(p.calls.Add(1)) - 1
	if idx >= len(p.Plan) {
		return nil, errors.New("scriptedProvider: plan exhausted")
	}
	return &provider.Response{
		Message:    p.Plan[idx],
		StopReason: schema.StopReasonEndTurn,
		Usage:      schema.Usage{InputTokens: 10, OutputTokens: 5},
		Model:      "scripted-1",
	}, nil
}

// helper: registry holding two tools used across tests.
func toolsRegistry(t *testing.T) *tool.Registry {
	t.Helper()
	type addIn struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	type addOut struct {
		Sum int `json:"sum"`
	}
	add, err := tool.NewTool("add", "Add",
		func(_ context.Context, in addIn) (addOut, error) { return addOut{Sum: in.A + in.B}, nil })
	if err != nil {
		t.Fatal(err)
	}
	reg, err := tool.NewRegistry(add)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestConfig_ValidateRejectsMissing(t *testing.T) {
	t.Parallel()
	if _, err := NewReAct(Config{}); err == nil {
		t.Fatal("expected error for missing Provider")
	}
	if _, err := NewReAct(Config{Provider: &scriptedProvider{}}); err == nil {
		t.Fatal("expected error for missing Model")
	}
}

// scriptedProviderNoTools mirrors scriptedProvider but advertises
// ToolCalling=false, so the capability-aware validator can catch a
// mismatched Config.Tools at construction.
type scriptedProviderNoTools struct{ scriptedProvider }

func (*scriptedProviderNoTools) Capabilities() provider.Capabilities {
	return provider.Capabilities{ToolCalling: false}
}

func TestConfig_ValidateRejectsToolsOnNonToolingProvider(t *testing.T) {
	t.Parallel()
	reg := toolsRegistry(t)
	_, err := NewReAct(Config{
		Provider: &scriptedProviderNoTools{},
		Model:    "x",
		Tools:    reg,
	})
	if err == nil {
		t.Fatal("expected error when Tools is set on a non-tooling provider")
	}
}

func TestConfig_ValidateRejectsForceToolUseWithoutTools(t *testing.T) {
	t.Parallel()
	_, err := NewReAct(Config{
		Provider:     &scriptedProvider{},
		Model:        "x",
		ForceToolUse: true,
	})
	if err == nil {
		t.Fatal("expected error: ForceToolUse=true without Tools is nonsensical")
	}
}

func TestConfig_ValidateRejectsNegativeMaxIterations(t *testing.T) {
	t.Parallel()
	_, err := NewReAct(Config{
		Provider:      &scriptedProvider{},
		Model:         "x",
		MaxIterations: -1,
	})
	if err == nil {
		t.Fatal("expected error for negative MaxIterations")
	}
}

func TestReAct_ImmediateFinalAnswer(t *testing.T) {
	t.Parallel()
	p := &scriptedProvider{Plan: []schema.Message{
		schema.AssistantMessage("the answer is 5"),
	}}
	final, err := Run(context.Background(), Config{Provider: p, Model: "x"},
		"what is 2+3?")
	if err != nil {
		t.Fatal(err)
	}
	if final != "the answer is 5" {
		t.Errorf("FinalText = %q", final)
	}
	if got := p.calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
}

func TestReAct_ToolCallThenAnswer(t *testing.T) {
	t.Parallel()
	reg := toolsRegistry(t)
	// Turn 1: ask to call add(2,3). Turn 2: produce the final text.
	p := &scriptedProvider{Plan: []schema.Message{
		{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{
				{ID: "c1", Name: "add", Arguments: json.RawMessage(`{"a":2,"b":3}`)},
			},
		},
		schema.AssistantMessage("the sum is 5"),
	}}

	r, err := NewReAct(Config{Provider: p, Tools: reg, Model: "x"})
	if err != nil {
		t.Fatal(err)
	}
	final, err := r.Invoke(context.Background(), State{
		Messages: []schema.Message{schema.UserMessage("add 2 and 3")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if final.FinalText != "the sum is 5" {
		t.Errorf("FinalText = %q", final.FinalText)
	}
	if final.Iterations != 2 {
		t.Errorf("Iterations = %d, want 2", final.Iterations)
	}
	// Conversation should be: user, assistant (tool call), tool result, assistant (final).
	if len(final.Messages) != 4 {
		t.Fatalf("Messages len = %d (%+v)", len(final.Messages), final.Messages)
	}
	if final.Messages[2].Role != schema.RoleTool {
		t.Errorf("expected tool message at idx 2, got %+v", final.Messages[2])
	}
}

func TestReAct_MaxIterationsCap(t *testing.T) {
	t.Parallel()
	reg := toolsRegistry(t)
	// Plan that NEVER terminates: every turn returns a tool call.
	toolCall := schema.Message{
		Role: schema.RoleAssistant,
		ToolCalls: []schema.ToolCall{
			{ID: "c", Name: "add", Arguments: json.RawMessage(`{"a":1,"b":1}`)},
		},
	}
	p := &scriptedProvider{Plan: []schema.Message{toolCall, toolCall, toolCall, toolCall, toolCall}}

	r, err := NewReAct(Config{Provider: p, Tools: reg, Model: "x", MaxIterations: 3})
	if err != nil {
		t.Fatal(err)
	}
	final, err := r.Invoke(context.Background(), State{
		Messages: []schema.Message{schema.UserMessage("loop")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if final.Iterations != 3 {
		t.Errorf("Iterations = %d, want 3 (capped)", final.Iterations)
	}
	// FinalText stays empty because every turn had tool calls.
	if final.FinalText != "" {
		t.Errorf("FinalText should be empty after a tool-only cap, got %q", final.FinalText)
	}
}

func TestReAct_NoToolsRegisteredStillRuns(t *testing.T) {
	t.Parallel()
	// A configuration without tools is legal — agents that only do
	// reasoning. Verify the graph compiles and reaches END.
	p := &scriptedProvider{Plan: []schema.Message{schema.AssistantMessage("done")}}
	final, err := Run(context.Background(), Config{Provider: p, Model: "x"},
		"hello")
	if err != nil {
		t.Fatal(err)
	}
	if final != "done" {
		t.Errorf("FinalText = %q", final)
	}
}

func TestReAct_PropagatesProviderError(t *testing.T) {
	t.Parallel()
	boom := errors.New("network down")
	p := &errorProvider{err: boom}
	_, err := Run(context.Background(), Config{Provider: p, Model: "x"}, "hi")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

// errorProvider always returns err.
type errorProvider struct {
	err error
}

func (errorProvider) Name() string                        { return "err" }
func (errorProvider) Capabilities() provider.Capabilities { return provider.Capabilities{} }
func (errorProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}
func (e errorProvider) Generate(_ context.Context, _ provider.Request) (*provider.Response, error) {
	return nil, e.err
}

func TestReAct_PassesThroughSystemPrompt(t *testing.T) {
	t.Parallel()
	p := &recordingProvider{
		next: schema.AssistantMessage("ok"),
	}
	if _, err := Run(context.Background(), Config{Provider: p, Model: "x"},
		"hi", "be terse"); err != nil {
		t.Fatal(err)
	}
	if len(p.lastReq.Messages) < 2 {
		t.Fatalf("messages too short: %+v", p.lastReq.Messages)
	}
	if p.lastReq.Messages[0].Role != schema.RoleSystem {
		t.Errorf("expected system prompt first, got %+v", p.lastReq.Messages[0])
	}
}

// recordingProvider captures the last request for assertions.
type recordingProvider struct {
	next    schema.Message
	lastReq provider.Request
}

func (recordingProvider) Name() string { return "rec" }
func (recordingProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{ToolCalling: true}
}
func (recordingProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}
func (r *recordingProvider) Generate(_ context.Context, req provider.Request) (*provider.Response, error) {
	r.lastReq = req
	return &provider.Response{Message: r.next, StopReason: schema.StopReasonEndTurn}, nil
}

func TestReAct_ForceToolUseSetsRequired(t *testing.T) {
	t.Parallel()
	reg := toolsRegistry(t)
	p := &recordingProvider{next: schema.AssistantMessage("done")}
	_, err := Run(context.Background(), Config{
		Provider: p, Tools: reg, Model: "x", ForceToolUse: true,
	}, "go")
	if err != nil {
		t.Fatal(err)
	}
	if p.lastReq.ToolChoice != provider.ToolChoiceRequired {
		t.Errorf("ToolChoice = %q, want required", p.lastReq.ToolChoice)
	}
}
