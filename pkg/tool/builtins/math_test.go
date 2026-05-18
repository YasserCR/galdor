package builtins

import (
	"context"
	"math"
	"testing"
)

func TestMath_BinaryOps(t *testing.T) {
	t.Parallel()
	tt := MustNewMathTool()
	cases := []struct {
		op   string
		a, b float64
		want float64
	}{
		{"add", 2, 3, 5},
		{"sub", 5, 2, 3},
		{"mul", 4, 5, 20},
		{"div", 10, 4, 2.5},
		{"mod", 10, 3, 1},
		{"pow", 2, 10, 1024},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			t.Parallel()
			out, err := tt.Execute(context.Background(), MathIn{Op: c.op, A: c.a, B: c.b})
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(out.Result-c.want) > 1e-9 {
				t.Errorf("%s(%v,%v) = %v, want %v", c.op, c.a, c.b, out.Result, c.want)
			}
		})
	}
}

func TestMath_UnaryOps(t *testing.T) {
	t.Parallel()
	tt := MustNewMathTool()
	cases := []struct {
		op   string
		a    float64
		want float64
	}{
		{"sqrt", 16, 4},
		{"abs", -7, 7},
		{"ln", math.E, 1},
		{"log10", 1000, 3},
		{"exp", 0, 1},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			t.Parallel()
			out, err := tt.Execute(context.Background(), MathIn{Op: c.op, A: c.a})
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(out.Result-c.want) > 1e-9 {
				t.Errorf("%s(%v) = %v, want %v", c.op, c.a, out.Result, c.want)
			}
		})
	}
}

func TestMath_DivByZero(t *testing.T) {
	t.Parallel()
	tt := MustNewMathTool()
	if _, err := tt.Execute(context.Background(), MathIn{Op: "div", A: 1, B: 0}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := tt.Execute(context.Background(), MathIn{Op: "mod", A: 1, B: 0}); err == nil {
		t.Fatal("expected error")
	}
}

func TestMath_DomainErrors(t *testing.T) {
	t.Parallel()
	tt := MustNewMathTool()
	for _, op := range []string{"sqrt", "ln", "log10"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			input := MathIn{Op: op, A: -1}
			if op != "sqrt" {
				input.A = 0
			}
			if _, err := tt.Execute(context.Background(), input); err == nil {
				t.Fatalf("%s of invalid input should error", op)
			}
		})
	}
}

func TestMath_UnknownOpRejected(t *testing.T) {
	t.Parallel()
	tt := MustNewMathTool()
	if _, err := tt.Execute(context.Background(), MathIn{Op: "wat"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestMath_NewMathToolReturnsNoError(t *testing.T) {
	t.Parallel()
	if _, err := NewMathTool(); err != nil {
		t.Fatal(err)
	}
}
