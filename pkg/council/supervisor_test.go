package council

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// scriptedProvider returns a canned reply per Generate call. Shared
// between supervisor and swarm tests.
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
		return nil, errors.New("scripted: plan exhausted")
	}
	return &provider.Response{
		Message:    p.Plan[idx],
		StopReason: schema.StopReasonEndTurn,
		Model:      "scripted-1",
	}, nil
}

func TestSupervisor_RoutesAndFinalizes(t *testing.T) {
	t.Parallel()
	// Two-turn supervisor: dispatch to "math", then finish.
	p := &scriptedProvider{Plan: []schema.Message{
		schema.AssistantMessage(`{"worker":"math","task":"compute 2+3"}`),
		schema.AssistantMessage(`{"final":"The answer is 5."}`),
	}}
	mathRan := false
	final, err := RunSupervisor(context.Background(), SupervisorConfig{
		Provider: p,
		Model:    "x",
		Workers: []Worker{
			{
				Name:        "math",
				Description: "performs calculations",
				Run: func(_ context.Context, task string) (string, error) {
					mathRan = true
					if !strings.Contains(task, "2+3") {
						t.Errorf("math worker got unexpected task: %q", task)
					}
					return "5", nil
				},
			},
		},
	}, "what is 2 plus 3?")
	if err != nil {
		t.Fatal(err)
	}
	if !mathRan {
		t.Error("math worker was never invoked")
	}
	if final != "The answer is 5." {
		t.Errorf("final = %q", final)
	}
	if got := p.calls.Load(); got != 2 {
		t.Errorf("supervisor calls = %d, want 2", got)
	}
}

func TestSupervisor_RecordsHistoryAcrossHops(t *testing.T) {
	t.Parallel()
	p := &scriptedProvider{Plan: []schema.Message{
		schema.AssistantMessage(`{"worker":"a","task":"step one"}`),
		schema.AssistantMessage(`{"worker":"b","task":"step two"}`),
		schema.AssistantMessage(`{"final":"done"}`),
	}}
	r, err := NewSupervisor(SupervisorConfig{
		Provider: p,
		Model:    "x",
		Workers: []Worker{
			{Name: "a", Description: "first worker", Run: func(_ context.Context, _ string) (string, error) { return "out-a", nil }},
			{Name: "b", Description: "second worker", Run: func(_ context.Context, _ string) (string, error) { return "out-b", nil }},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	final, err := r.Invoke(context.Background(), SupervisorState{Input: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if final.Final != "done" {
		t.Errorf("Final = %q", final.Final)
	}
	if len(final.History) != 2 {
		t.Fatalf("History = %d, want 2", len(final.History))
	}
	if final.History[0].Worker != "a" || final.History[0].Output != "out-a" {
		t.Errorf("history[0] = %+v", final.History[0])
	}
	if final.History[1].Worker != "b" || final.History[1].Output != "out-b" {
		t.Errorf("history[1] = %+v", final.History[1])
	}
}

func TestSupervisor_TolerantOfFences(t *testing.T) {
	t.Parallel()
	p := &scriptedProvider{Plan: []schema.Message{
		schema.AssistantMessage("```json\n{\"worker\":\"only\",\"task\":\"do it\"}\n```"),
		schema.AssistantMessage("```json\n{\"final\":\"finished\"}\n```"),
	}}
	final, err := RunSupervisor(context.Background(), SupervisorConfig{
		Provider: p,
		Model:    "x",
		Workers: []Worker{
			{Name: "only", Description: "single worker", Run: func(_ context.Context, _ string) (string, error) { return "ok", nil }},
		},
	}, "anything")
	if err != nil {
		t.Fatal(err)
	}
	if final != "finished" {
		t.Errorf("final = %q", final)
	}
}

func TestSupervisor_MaxHopsCap(t *testing.T) {
	t.Parallel()
	keepGoing := schema.AssistantMessage(`{"worker":"loop","task":"again"}`)
	p := &scriptedProvider{Plan: []schema.Message{keepGoing, keepGoing, keepGoing, keepGoing, keepGoing}}
	r, err := NewSupervisor(SupervisorConfig{
		Provider: p,
		Model:    "x",
		MaxHops:  3,
		Workers: []Worker{
			{Name: "loop", Description: "x", Run: func(_ context.Context, _ string) (string, error) { return "x", nil }},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	final, err := r.Invoke(context.Background(), SupervisorState{Input: "loop"})
	if err != nil {
		t.Fatal(err)
	}
	if final.Hops != 3 {
		t.Errorf("Hops = %d, want 3 (capped)", final.Hops)
	}
}

func TestSupervisor_RejectsUnknownWorker(t *testing.T) {
	t.Parallel()
	p := &scriptedProvider{Plan: []schema.Message{
		schema.AssistantMessage(`{"worker":"ghost","task":"x"}`),
	}}
	_, err := RunSupervisor(context.Background(), SupervisorConfig{
		Provider: p,
		Model:    "x",
		Workers: []Worker{
			{Name: "real", Description: "y", Run: func(_ context.Context, _ string) (string, error) { return "", nil }},
		},
	}, "hi")
	if err == nil {
		t.Fatal("expected error for unknown worker")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("err should name the unknown worker: %v", err)
	}
}

func TestSupervisor_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	cases := map[string]SupervisorConfig{
		"nil provider":          {Model: "x", Workers: []Worker{{Name: "a", Run: func(_ context.Context, _ string) (string, error) { return "", nil }}}},
		"empty model":           {Provider: &scriptedProvider{}, Workers: []Worker{{Name: "a", Run: func(_ context.Context, _ string) (string, error) { return "", nil }}}},
		"no workers":            {Provider: &scriptedProvider{}, Model: "x"},
		"reserved worker name":  {Provider: &scriptedProvider{}, Model: "x", Workers: []Worker{{Name: "supervisor", Run: func(_ context.Context, _ string) (string, error) { return "", nil }}}},
		"unsafe worker name":    {Provider: &scriptedProvider{}, Model: "x", Workers: []Worker{{Name: "bad name", Run: func(_ context.Context, _ string) (string, error) { return "", nil }}}},
		"duplicate worker name": {Provider: &scriptedProvider{}, Model: "x", Workers: []Worker{{Name: "a", Run: func(_ context.Context, _ string) (string, error) { return "", nil }}, {Name: "a", Run: func(_ context.Context, _ string) (string, error) { return "", nil }}}},
		"nil Run":               {Provider: &scriptedProvider{}, Model: "x", Workers: []Worker{{Name: "a"}}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewSupervisor(cfg); err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}
