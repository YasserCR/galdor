package builtins

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/YasserCR/galdor/pkg/tool"
)

// MathIn is the input shape of the math tool. The Op enum chooses the
// operation; A and B are the operands. Unary operations ignore B.
type MathIn struct {
	Op string  `json:"op" jsonschema:"Operation,enum=add;sub;mul;div;mod;pow;sqrt;abs;ln;log10;exp"`
	A  float64 `json:"a" jsonschema:"First operand"`
	B  float64 `json:"b,omitempty" jsonschema:"Second operand (binary ops only)"`
}

// MathOut is the result of a math operation.
type MathOut struct {
	Result float64 `json:"result"`
}

// NewMathTool returns a deterministic, side-effect-free math tool. It
// is safe to enable for any agent. The operation enum is documented
// inline; division by zero and domain errors (sqrt of a negative
// number, ln of a non-positive number) return descriptive errors.
func NewMathTool() (tool.Tool[MathIn, MathOut], error) {
	return tool.NewTool("math",
		"Evaluate a basic math operation: add/sub/mul/div/mod/pow/sqrt/abs/ln/log10/exp.",
		runMath)
}

// MustNewMathTool is the panicking variant of NewMathTool.
func MustNewMathTool() tool.Tool[MathIn, MathOut] {
	return tool.MustNewTool("math",
		"Evaluate a basic math operation: add/sub/mul/div/mod/pow/sqrt/abs/ln/log10/exp.", runMath)
}

func runMath(_ context.Context, in MathIn) (MathOut, error) {
	switch strings.ToLower(strings.TrimSpace(in.Op)) {
	case "add":
		return MathOut{Result: in.A + in.B}, nil
	case "sub":
		return MathOut{Result: in.A - in.B}, nil
	case "mul":
		return MathOut{Result: in.A * in.B}, nil
	case "div":
		if in.B == 0 {
			return MathOut{}, fmt.Errorf("math: division by zero")
		}
		return MathOut{Result: in.A / in.B}, nil
	case "mod":
		if in.B == 0 {
			return MathOut{}, fmt.Errorf("math: mod by zero")
		}
		return MathOut{Result: math.Mod(in.A, in.B)}, nil
	case "pow":
		return MathOut{Result: math.Pow(in.A, in.B)}, nil
	case "sqrt":
		if in.A < 0 {
			return MathOut{}, fmt.Errorf("math: sqrt of negative number")
		}
		return MathOut{Result: math.Sqrt(in.A)}, nil
	case "abs":
		return MathOut{Result: math.Abs(in.A)}, nil
	case "ln":
		if in.A <= 0 {
			return MathOut{}, fmt.Errorf("math: ln domain error (a must be > 0)")
		}
		return MathOut{Result: math.Log(in.A)}, nil
	case "log10":
		if in.A <= 0 {
			return MathOut{}, fmt.Errorf("math: log10 domain error (a must be > 0)")
		}
		return MathOut{Result: math.Log10(in.A)}, nil
	case "exp":
		return MathOut{Result: math.Exp(in.A)}, nil
	default:
		return MathOut{}, fmt.Errorf("math: unknown op %q", in.Op)
	}
}
