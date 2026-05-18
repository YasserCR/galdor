package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YasserCR/galdor/pkg/schema"
)

// addIn / addOut and friends are simple shapes reused across tests.
type addIn struct {
	A int `json:"a" jsonschema:"First addend"`
	B int `json:"b" jsonschema:"Second addend"`
}

type addOut struct {
	Sum int `json:"sum"`
}

func newAddTool(t *testing.T) Tool[addIn, addOut] {
	t.Helper()
	tt, err := NewTool("add", "Add two integers", func(_ context.Context, in addIn) (addOut, error) {
		return addOut{Sum: in.A + in.B}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tt
}

func TestNewTool_RejectsEmptyName(t *testing.T) {
	t.Parallel()
	_, err := NewTool("", "x", func(_ context.Context, in addIn) (addOut, error) { return addOut{}, nil })
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestNewTool_RejectsNilFn(t *testing.T) {
	t.Parallel()
	_, err := NewTool[addIn, addOut]("x", "y", nil)
	if err == nil {
		t.Fatal("expected error for nil fn")
	}
}

func TestNewTool_RejectsInvalidInputType(t *testing.T) {
	t.Parallel()
	// chan is not JSON-Schema-able; NewTool should refuse to build it.
	_, err := NewTool("bad", "x", func(_ context.Context, in chan int) (int, error) {
		return 0, nil
	})
	if err == nil {
		t.Fatal("expected error for unsupported In type")
	}
}

func TestTool_SchemaReflectsInput(t *testing.T) {
	t.Parallel()
	tt := newAddTool(t)
	s := tt.Schema()
	if s.Type != "object" {
		t.Errorf("Type = %q", s.Type)
	}
	if _, ok := s.Properties["a"]; !ok {
		t.Errorf("missing property a: %+v", s.Properties)
	}
	if s.Properties["a"].Description != "First addend" {
		t.Errorf("description = %q", s.Properties["a"].Description)
	}
}

func TestTool_ExecuteTyped(t *testing.T) {
	t.Parallel()
	tt := newAddTool(t)
	out, err := tt.Execute(context.Background(), addIn{A: 2, B: 3})
	if err != nil {
		t.Fatal(err)
	}
	if out.Sum != 5 {
		t.Errorf("Sum = %d", out.Sum)
	}
}

func TestTool_ExecuteJSON_HappyPath(t *testing.T) {
	t.Parallel()
	tt := newAddTool(t)
	raw, err := tt.ExecuteJSON(context.Background(), json.RawMessage(`{"a":2,"b":40}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"sum":42}` {
		t.Errorf("got %s", raw)
	}
}

func TestTool_ExecuteJSON_InvalidInput(t *testing.T) {
	t.Parallel()
	tt := newAddTool(t)
	_, err := tt.ExecuteJSON(context.Background(), json.RawMessage(`{"a":"not-an-int"}`))
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestTool_ExecuteJSON_EmptyInputAllowed(t *testing.T) {
	t.Parallel()
	type noInput struct{}
	tt, err := NewTool("noop", "no input", func(_ context.Context, _ noInput) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []json.RawMessage{nil, []byte(""), []byte("null"), []byte("{}")} {
		out, err := tt.ExecuteJSON(context.Background(), raw)
		if err != nil {
			t.Fatalf("raw=%q err=%v", raw, err)
		}
		if string(out) != `"ok"` {
			t.Errorf("raw=%q out=%s", raw, out)
		}
	}
}

func TestTool_ExecuteJSON_PropagatesToolError(t *testing.T) {
	t.Parallel()
	want := errors.New("boom")
	tt, err := NewTool("explode", "", func(_ context.Context, _ addIn) (addOut, error) {
		return addOut{}, want
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tt.ExecuteJSON(context.Background(), json.RawMessage(`{"a":1,"b":1}`))
	if !errors.Is(err, want) {
		t.Errorf("got %v, want %v", err, want)
	}
}

func TestMustNewTool_PanicsOnError(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = MustNewTool[addIn, addOut]("", "x", func(_ context.Context, _ addIn) (addOut, error) {
		return addOut{}, nil
	})
}

func TestRegistry_AddAndGet(t *testing.T) {
	t.Parallel()
	reg, err := NewRegistry(newAddTool(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("add"); !ok {
		t.Error("add should be present")
	}
	if _, ok := reg.Get("missing"); ok {
		t.Error("missing should be absent")
	}
	if reg.Len() != 1 {
		t.Errorf("Len = %d", reg.Len())
	}
}

func TestRegistry_DuplicateName(t *testing.T) {
	t.Parallel()
	a := newAddTool(t)
	_, err := NewRegistry(a, a)
	if err == nil {
		t.Fatal("expected duplicate-name error")
	}
}

func TestRegistry_NilToolRejected(t *testing.T) {
	t.Parallel()
	reg := mustEmptyRegistry(t)
	if err := reg.Add(nil); err == nil {
		t.Fatal("expected error for nil tool")
	}
}

func TestRegistry_EmptyNameRejected(t *testing.T) {
	t.Parallel()
	reg := mustEmptyRegistry(t)
	// Bypass NewTool's empty-name guard by constructing the struct
	// directly — Registry.Add must reject it independently.
	if err := reg.Add(&typedTool[addIn, addOut]{name: ""}); err == nil {
		t.Fatal("expected error for empty-name tool")
	}
}

func TestRegistry_ToolsSortedByName(t *testing.T) {
	t.Parallel()
	add := newAddTool(t)
	mul, err := NewTool("mul", "Multiply", func(_ context.Context, in addIn) (addOut, error) {
		return addOut{Sum: in.A * in.B}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry(mul, add)
	if err != nil {
		t.Fatal(err)
	}
	tools := reg.Tools()
	if len(tools) != 2 || tools[0].Name() != "add" || tools[1].Name() != "mul" {
		t.Errorf("not sorted: %+v", []string{tools[0].Name(), tools[1].Name()})
	}
}

func TestRegistry_ToolDefs(t *testing.T) {
	t.Parallel()
	reg, err := NewRegistry(newAddTool(t))
	if err != nil {
		t.Fatal(err)
	}
	defs, err := reg.ToolDefs()
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 || defs[0].Name != "add" {
		t.Fatalf("defs = %+v", defs)
	}
	if defs[0].Description != "Add two integers" {
		t.Errorf("Description = %q", defs[0].Description)
	}
	if !strings.Contains(string(defs[0].Schema), `"type":"object"`) {
		t.Errorf("Schema = %s", defs[0].Schema)
	}
}

func TestExecuteCalls_Concurrent(t *testing.T) {
	t.Parallel()
	// Two slow tools running concurrently should finish in roughly
	// max(latency_a, latency_b), not sum. We verify by timing.
	slow := func(d time.Duration) func(context.Context, addIn) (addOut, error) {
		return func(ctx context.Context, in addIn) (addOut, error) {
			select {
			case <-time.After(d):
				return addOut{Sum: in.A + in.B}, nil
			case <-ctx.Done():
				return addOut{}, ctx.Err()
			}
		}
	}
	a, err := NewTool("slowA", "", slow(80*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewTool("slowB", "", slow(80*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry(a, b)
	if err != nil {
		t.Fatal(err)
	}

	calls := []schema.ToolCall{
		{ID: "1", Name: "slowA", Arguments: json.RawMessage(`{"a":1,"b":2}`)},
		{ID: "2", Name: "slowB", Arguments: json.RawMessage(`{"a":3,"b":4}`)},
	}
	start := time.Now()
	results := ExecuteCalls(context.Background(), reg, calls)
	elapsed := time.Since(start)

	if len(results) != 2 {
		t.Fatalf("results = %d", len(results))
	}
	if results[0].ID != "1" || results[1].ID != "2" {
		t.Errorf("order not preserved: %+v", results)
	}
	if elapsed > 150*time.Millisecond {
		t.Errorf("tools did not run concurrently (elapsed=%v)", elapsed)
	}
}

func TestExecuteCalls_UnknownTool(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry(newAddTool(t))
	results := ExecuteCalls(context.Background(), reg, []schema.ToolCall{
		{ID: "x", Name: "missing", Arguments: json.RawMessage(`{}`)},
	})
	if !errors.Is(results[0].Err, ErrUnknownTool) {
		t.Errorf("err = %v, want ErrUnknownTool", results[0].Err)
	}
}

func TestExecuteCalls_ContextCancelShortCircuits(t *testing.T) {
	t.Parallel()
	// A tool that increments a counter when started; we cancel
	// immediately and want zero of them to actually run.
	var started atomic.Int32
	t1, err := NewTool("t1", "", func(_ context.Context, _ addIn) (addOut, error) {
		started.Add(1)
		return addOut{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := NewRegistry(t1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := ExecuteCalls(ctx, reg, []schema.ToolCall{
		{ID: "1", Name: "t1", Arguments: json.RawMessage(`{}`)},
	})
	if res[0].Err == nil {
		t.Errorf("expected canceled error, got nil")
	}
	if started.Load() != 0 {
		t.Errorf("tool body ran despite canceled context (started=%d)", started.Load())
	}
}

func TestExecuteCalls_NilRegistry(t *testing.T) {
	t.Parallel()
	res := ExecuteCalls(context.Background(), nil, []schema.ToolCall{{ID: "1", Name: "x"}})
	if res[0].Err == nil {
		t.Error("expected error for nil registry")
	}
}

func TestAsToolResultMessages_FromMixedResults(t *testing.T) {
	t.Parallel()
	results := []Result{
		{ID: "ok", Name: "n", Output: json.RawMessage(`{"x":1}`)},
		{ID: "err", Name: "n", Err: errors.New("boom")},
		{ID: "empty", Name: "n"},
	}
	msgs := AsToolResultMessages(results)
	if len(msgs) != 3 {
		t.Fatalf("messages = %d", len(msgs))
	}
	if msgs[0].ToolCallID != "ok" || msgs[0].Text() != `{"x":1}` {
		t.Errorf("ok msg = %+v", msgs[0])
	}
	if !strings.Contains(msgs[1].Text(), "boom") {
		t.Errorf("err msg should embed the error: %s", msgs[1].Text())
	}
	if msgs[2].Text() != "null" {
		t.Errorf("empty msg should be 'null', got %s", msgs[2].Text())
	}
}

// mustEmptyRegistry returns an empty Registry, failing the test if the
// constructor errors (it shouldn't with no inputs).
func mustEmptyRegistry(t *testing.T) *Registry {
	t.Helper()
	r, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	return r
}
