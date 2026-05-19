package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

func TestPlanAndExecute_FinishesOnFirstReplan(t *testing.T) {
	t.Parallel()
	// Script: planner emits two steps; one executor answer per step;
	// replanner returns final after the first step (so the second
	// step is dropped). We expect: 1 plan + 1 execute + 1 replan = 3 calls.
	p := &scriptedProvider{Plan: []schema.Message{
		schema.AssistantMessage(`["look up the answer", "format it"]`),
		schema.AssistantMessage("42"),
		schema.AssistantMessage(`{"plan": [], "final": "The answer is 42."}`),
	}}

	final, err := RunPlanAndExecute(context.Background(), PlanExecuteConfig{
		Provider: p,
		Model:    "scripted-1",
	}, "what is the answer?")
	if err != nil {
		t.Fatal(err)
	}
	if final != "The answer is 42." {
		t.Errorf("final = %q", final)
	}
	if got := p.calls.Load(); got != 3 {
		t.Errorf("provider calls = %d, want 3 (plan + execute + replan)", got)
	}
}

func TestPlanAndExecute_RunsAllStepsThenFinishes(t *testing.T) {
	t.Parallel()
	// Two-step plan, replanner keeps the remaining plan after step 1,
	// then finishes after step 2. Total calls: plan + execute1 +
	// replan1 + execute2 + replan2 = 5.
	p := &scriptedProvider{Plan: []schema.Message{
		schema.AssistantMessage(`["step one", "step two"]`),
		schema.AssistantMessage("did step one"),
		schema.AssistantMessage(`{"plan": ["step two"], "final": ""}`),
		schema.AssistantMessage("did step two"),
		schema.AssistantMessage(`{"plan": [], "final": "done"}`),
	}}

	r, err := NewPlanAndExecute(PlanExecuteConfig{Provider: p, Model: "scripted-1"})
	if err != nil {
		t.Fatal(err)
	}
	final, err := r.Invoke(context.Background(), PlanExecuteState{Input: "do two things"})
	if err != nil {
		t.Fatal(err)
	}
	if final.Final != "done" {
		t.Errorf("Final = %q", final.Final)
	}
	if len(final.Past) != 2 {
		t.Fatalf("Past = %d, want 2", len(final.Past))
	}
	if final.Past[0].Step != "step one" || final.Past[1].Step != "step two" {
		t.Errorf("Past order wrong: %+v", final.Past)
	}
	if got := p.calls.Load(); got != 5 {
		t.Errorf("calls = %d, want 5", got)
	}
}

func TestPlanAndExecute_TolerantOfMarkdownFences(t *testing.T) {
	t.Parallel()
	// LLMs love adding ```json fences despite instructions; ensure
	// parsePlan / parseReplan strip them.
	p := &scriptedProvider{Plan: []schema.Message{
		schema.AssistantMessage("```json\n[\"one\"]\n```"),
		schema.AssistantMessage("ok"),
		schema.AssistantMessage("```json\n{\"plan\": [], \"final\": \"finished\"}\n```"),
	}}

	final, err := RunPlanAndExecute(context.Background(), PlanExecuteConfig{
		Provider: p,
		Model:    "scripted-1",
	}, "anything")
	if err != nil {
		t.Fatal(err)
	}
	if final != "finished" {
		t.Errorf("final = %q", final)
	}
}

func TestPlanAndExecute_TerminatesWhenPlanGoesEmpty(t *testing.T) {
	t.Parallel()
	// Replanner returns empty plan + empty final. Router must END
	// (no infinite loop, no panic). Final stays "" in that case.
	p := &scriptedProvider{Plan: []schema.Message{
		schema.AssistantMessage(`["only step"]`),
		schema.AssistantMessage("did it"),
		schema.AssistantMessage(`{"plan": [], "final": ""}`),
	}}

	r, err := NewPlanAndExecute(PlanExecuteConfig{Provider: p, Model: "scripted-1"})
	if err != nil {
		t.Fatal(err)
	}
	final, err := r.Invoke(context.Background(), PlanExecuteState{Input: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if final.Final != "" {
		t.Errorf("Final = %q, want empty (no synthesized answer)", final.Final)
	}
	if len(final.Past) != 1 {
		t.Errorf("Past = %d, want 1", len(final.Past))
	}
}

func TestPlanAndExecute_MaxIterationsCap(t *testing.T) {
	t.Parallel()
	// Replanner perpetually returns a non-empty plan and no final.
	// The loop must terminate once Iter hits MaxIterations.
	keepGoing := schema.AssistantMessage(`{"plan": ["loop"], "final": ""}`)
	step := schema.AssistantMessage("step output")
	p := &scriptedProvider{Plan: []schema.Message{
		schema.AssistantMessage(`["loop"]`),
		step, keepGoing,
		step, keepGoing,
		step, keepGoing,
		step, keepGoing, // unreached: cap is 3
	}}

	r, err := NewPlanAndExecute(PlanExecuteConfig{
		Provider:      p,
		Model:         "scripted-1",
		MaxIterations: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	final, err := r.Invoke(context.Background(), PlanExecuteState{Input: "loop"})
	if err != nil {
		t.Fatal(err)
	}
	if final.Iter != 3 {
		t.Errorf("Iter = %d, want 3 (capped)", final.Iter)
	}
}

func TestPlanAndExecute_RejectsMissingProvider(t *testing.T) {
	t.Parallel()
	if _, err := NewPlanAndExecute(PlanExecuteConfig{Model: "x"}); err == nil {
		t.Fatal("expected error for missing Provider")
	}
	if _, err := NewPlanAndExecute(PlanExecuteConfig{Provider: &scriptedProvider{}}); err == nil {
		t.Fatal("expected error for missing Model")
	}
}

func TestParsePlan_HandlesProseAroundJSON(t *testing.T) {
	t.Parallel()
	plan, err := parsePlan(`Sure! Here is the plan: ["a", "b", "c"] -- let me know.`)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 3 || plan[0] != "a" || plan[2] != "c" {
		t.Errorf("plan = %+v", plan)
	}
}

func TestParseReplan_HandlesProseAroundJSON(t *testing.T) {
	t.Parallel()
	plan, final, err := parseReplan(`Decision: {"plan": ["next"], "final": ""} -- continue.`)
	if err != nil {
		t.Fatal(err)
	}
	if final != "" {
		t.Errorf("final = %q", final)
	}
	if len(plan) != 1 || plan[0] != "next" {
		t.Errorf("plan = %+v", plan)
	}
}

func TestPlanAndExecute_ExecutorSeesPriorSteps(t *testing.T) {
	t.Parallel()
	// Verify the second executor call sees the first step's result
	// embedded in its user prompt.
	rec := &capturingProvider{inner: &scriptedProvider{Plan: []schema.Message{
		schema.AssistantMessage(`["first", "second"]`),
		schema.AssistantMessage("result of first"),
		schema.AssistantMessage(`{"plan": ["second"], "final": ""}`),
		schema.AssistantMessage("result of second"),
		schema.AssistantMessage(`{"plan": [], "final": "all done"}`),
	}}}

	final, err := RunPlanAndExecute(context.Background(), PlanExecuteConfig{
		Provider: rec,
		Model:    "scripted-1",
	}, "do two things")
	if err != nil {
		t.Fatal(err)
	}
	if final != "all done" {
		t.Errorf("final = %q", final)
	}
	// Calls: plan=0, exec1=1, replan1=2, exec2=3, replan2=4.
	if len(rec.seen) < 4 {
		t.Fatalf("only saw %d calls", len(rec.seen))
	}
	secondExec := rec.seen[3]
	if !strings.Contains(secondExec, "first") {
		t.Errorf("second executor prompt should reference the first step; got:\n%s", secondExec)
	}
	if !strings.Contains(secondExec, "result of first") {
		t.Errorf("second executor prompt should include the first step's result; got:\n%s", secondExec)
	}
}

// capturingProvider proxies Generate to a scriptedProvider while
// recording the concatenated user-role message text of each call.
type capturingProvider struct {
	inner *scriptedProvider
	seen  []string
}

func (*capturingProvider) Name() string { return "capturing" }
func (*capturingProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{ToolCalling: true}
}
func (*capturingProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}

func (cp *capturingProvider) Generate(ctx context.Context, req provider.Request) (*provider.Response, error) {
	var b strings.Builder
	for _, m := range req.Messages {
		if m.Role == schema.RoleUser {
			b.WriteString(m.Text())
			b.WriteString("\n")
		}
	}
	cp.seen = append(cp.seen, b.String())
	return cp.inner.Generate(ctx, req)
}
