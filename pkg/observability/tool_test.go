package observability

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/codes"

	"github.com/YasserCR/galdor/pkg/tool"
)

type addIn struct {
	A int `json:"a"`
	B int `json:"b"`
}
type addOut struct {
	Sum int `json:"sum"`
}

func newAddTool(t *testing.T) tool.AnyTool {
	t.Helper()
	tt, err := tool.NewTool("add", "Add", func(_ context.Context, in addIn) (addOut, error) {
		return addOut{Sum: in.A + in.B}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tt
}

func TestInstrumentTool_HappyPath(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")
	tt := InstrumentTool(newAddTool(t), tr)
	out, err := tt.ExecuteJSON(context.Background(), json.RawMessage(`{"a":2,"b":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"sum":5}` {
		t.Errorf("out = %s", out)
	}
	_ = tp.ForceFlush(context.Background())
	spans := exp.GetSpans()
	if len(spans) != 1 || spans[0].Name != SpanToolExecute {
		t.Fatalf("spans = %+v", spans)
	}
	if !hasAttr(spans[0].Attributes, AttrGenAIToolName, "add") {
		t.Errorf("missing tool name attr")
	}
	if !hasIntAttr(spans[0].Attributes, AttrGenAIToolInputSize, len(`{"a":2,"b":3}`)) {
		t.Errorf("missing input size attr")
	}
}

func TestInstrumentTool_RecordsErrors(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")

	// Use a real tool helper that returns an error.
	failing, err := tool.NewTool("explode", "", func(_ context.Context, _ addIn) (addOut, error) {
		return addOut{}, errors.New("boom")
	})
	if err != nil {
		t.Fatal(err)
	}
	tt := InstrumentTool(failing, tr)
	if _, err := tt.ExecuteJSON(context.Background(), json.RawMessage(`{"a":1,"b":1}`)); err == nil {
		t.Fatal("expected error")
	}
	_ = tp.ForceFlush(context.Background())
	spans := exp.GetSpans()
	if spans[0].Status.Code != codes.Error {
		t.Errorf("status = %v", spans[0].Status.Code)
	}
}

func TestInstrumentRegistry_WrapsEveryTool(t *testing.T) {
	t.Parallel()
	exp, tp := newRecorder(t)
	tr := tp.Tracer("test")

	reg, err := tool.NewRegistry(newAddTool(t))
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := InstrumentRegistry(reg, tr)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := wrapped.Get("add")
	if !ok {
		t.Fatal("add missing")
	}
	if _, err := got.ExecuteJSON(context.Background(), json.RawMessage(`{"a":2,"b":3}`)); err != nil {
		t.Fatal(err)
	}
	_ = tp.ForceFlush(context.Background())
	if len(exp.GetSpans()) != 1 {
		t.Errorf("wrapped tool should emit a span, got %d", len(exp.GetSpans()))
	}
}

func TestInstrumentTool_Passthrough(t *testing.T) {
	t.Parallel()
	_, tp := newRecorder(t)
	tr := tp.Tracer("test")
	tt := InstrumentTool(newAddTool(t), tr)
	if tt.Name() != "add" {
		t.Errorf("Name = %q", tt.Name())
	}
	if tt.Description() != "Add" {
		t.Errorf("Description = %q", tt.Description())
	}
	if tt.Schema() == nil {
		t.Error("Schema is nil")
	}
}
