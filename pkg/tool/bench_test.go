package tool_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/YasserCR/galdor/pkg/schema"
	"github.com/YasserCR/galdor/pkg/tool"
)

type benchIn struct {
	X int `json:"x"`
}
type benchOut struct {
	X int `json:"x"`
}

// noOpTool is the cheapest possible tool body. Used to isolate the
// dispatcher's overhead from anything the tool body would do.
func noOpTool(name string) tool.AnyTool {
	return tool.MustNewTool(name, "test", func(_ context.Context, in benchIn) (benchOut, error) {
		return benchOut{X: in.X}, nil
	})
}

// BenchmarkExecuteCalls_1 measures the per-call cost of a single
// tool dispatch. Floor for the executor's plumbing.
func BenchmarkExecuteCalls_1(b *testing.B) {
	reg, err := tool.NewRegistry(noOpTool("t0"))
	if err != nil {
		b.Fatal(err)
	}
	calls := []schema.ToolCall{
		{ID: "c0", Name: "t0", Arguments: []byte(`{"x":1}`)},
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tool.ExecuteCalls(ctx, reg, calls)
	}
}

// BenchmarkExecuteCalls_10 measures parallel fan-out: 10 tool
// calls dispatched concurrently. Normalized per call this should
// stay flat-ish (Go's goroutine spawn is cheap), but it confirms
// the orchestration doesn't bottleneck.
func BenchmarkExecuteCalls_10(b *testing.B) {
	tools := make([]tool.AnyTool, 10)
	for i := range tools {
		tools[i] = noOpTool(fmt.Sprintf("t%d", i))
	}
	reg, err := tool.NewRegistry(tools...)
	if err != nil {
		b.Fatal(err)
	}
	calls := make([]schema.ToolCall, 10)
	for i := range calls {
		calls[i] = schema.ToolCall{
			ID:        fmt.Sprintf("c%d", i),
			Name:      fmt.Sprintf("t%d", i),
			Arguments: []byte(`{"x":1}`),
		}
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tool.ExecuteCalls(ctx, reg, calls)
	}
}
