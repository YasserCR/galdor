package tool_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/schema"
	"github.com/YasserCR/galdor/pkg/tool"
)

type bombIn struct {
	Msg string `json:"msg"`
}
type bombOut struct{}

func makeBomb(t *testing.T, name string, fn func(context.Context, bombIn) (bombOut, error)) tool.AnyTool {
	t.Helper()
	tl, err := tool.NewTool(name, "test", fn)
	if err != nil {
		t.Fatal(err)
	}
	return tl
}

func TestExecuteCalls_RecoversToolPanic(t *testing.T) {
	t.Parallel()
	bomb := makeBomb(t, "bomb", func(_ context.Context, _ bombIn) (bombOut, error) {
		panic("nil deref or whatever")
	})
	reg, err := tool.NewRegistry(bomb)
	if err != nil {
		t.Fatal(err)
	}
	results := tool.ExecuteCalls(context.Background(), reg, []schema.ToolCall{
		{ID: "c1", Name: "bomb", Arguments: []byte(`{}`)},
	})
	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}
	if results[0].Err == nil {
		t.Fatal("expected an error from a panicking tool")
	}
	var pe *tool.PanicError
	if !errors.As(results[0].Err, &pe) {
		t.Fatalf("err = %v, want *tool.PanicError", results[0].Err)
	}
	if pe.Tool != "bomb" {
		t.Errorf("PanicError.Tool = %q, want %q", pe.Tool, "bomb")
	}
	if pe.Value != "nil deref or whatever" {
		t.Errorf("PanicError.Value = %v", pe.Value)
	}
	if !errors.Is(results[0].Err, tool.ErrPanic) {
		t.Errorf("errors.Is(err, ErrPanic) = false")
	}
	if !strings.Contains(string(pe.Stack), "bomb") && len(pe.Stack) == 0 {
		t.Errorf("expected a non-empty stack snapshot")
	}
}

func TestExecuteCalls_PanicWithErrorValueUnwraps(t *testing.T) {
	t.Parallel()
	inner := errors.New("inner kaboom")
	bomb := makeBomb(t, "bomb2", func(_ context.Context, _ bombIn) (bombOut, error) {
		panic(inner)
	})
	reg, _ := tool.NewRegistry(bomb)
	results := tool.ExecuteCalls(context.Background(), reg, []schema.ToolCall{
		{ID: "c", Name: "bomb2", Arguments: []byte(`{}`)},
	})
	if !errors.Is(results[0].Err, inner) {
		t.Errorf("expected the wrapped error to be reachable via errors.Is")
	}
}

func TestExecuteCalls_PanicInOneDoesNotKillSiblings(t *testing.T) {
	t.Parallel()
	bomb := makeBomb(t, "bomb3", func(_ context.Context, _ bombIn) (bombOut, error) {
		panic("only this one")
	})
	ok := makeBomb(t, "ok3", func(_ context.Context, _ bombIn) (bombOut, error) {
		return bombOut{}, nil
	})
	reg, _ := tool.NewRegistry(bomb, ok)
	results := tool.ExecuteCalls(context.Background(), reg, []schema.ToolCall{
		{ID: "a", Name: "bomb3", Arguments: []byte(`{}`)},
		{ID: "b", Name: "ok3", Arguments: []byte(`{}`)},
	})
	if len(results) != 2 {
		t.Fatalf("got %d results", len(results))
	}
	// Order is preserved: index 0 is the panicking call, index 1 is the ok one.
	if results[0].Err == nil {
		t.Error("panicking tool should have an error")
	}
	if results[1].Err != nil {
		t.Errorf("sibling tool should have succeeded; got %v", results[1].Err)
	}
}
