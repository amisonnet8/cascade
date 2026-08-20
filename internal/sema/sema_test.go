package sema_test

import (
	"strings"
	"testing"

	"github.com/amisonnet8/cascade/internal/parser"
	"github.com/amisonnet8/cascade/internal/sema"
)

func check(t *testing.T, src string) error {
	t.Helper()
	f, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return sema.Check(f)
}

func TestCheck_ValidProgram(t *testing.T) {
	src := `
func main(): int {
	let greeting: string = "hi"
	print(greeting)

	let name: string? = "Cascade"
	name = none

	const version: string = "0.1.0"

	let count = 3
	let pi: float = 3.14
	let ready: bool = true
	let empty: int?

	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_ValidOperators(t *testing.T) {
	src := `
func main(): int {
	let sum = 2 + 3 * 4
	let grouped = (2 + 3) * 4
	let ratio = 7 / 2
	let remainder = 7 % 2
	let negated = -sum

	let isBig = sum > 5
	let isEqual = sum == 14
	let combined = isBig && !isEqual || isEqual

	let greeting = "Hello, " + "Cascade" + "!"
	print(greeting)
	print(string(sum) + string(grouped) + string(ratio) + string(remainder) + string(negated))
	print(string(combined))

	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_ValidBitwiseAndShiftOperators(t *testing.T) {
	src := `
func main(): int {
	let a = 12
	let b = 10
	let and = a & b
	let or = a | b
	let xor = a ^ b
	let andNot = a &^ b
	let inverted = ~a
	let shiftedLeft = a << 2
	let shiftedRight = a >> 2
	print(string(and) + string(or) + string(xor) + string(andNot) + string(inverted) + string(shiftedLeft) + string(shiftedRight))
	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_BitwiseAndShiftErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "bitwise and requires int operands",
			src: `
func main(): int {
	let x = 1.0 & 2.0
	return 0
}
`,
			wantErr: "operator \"&\" requires int operands",
		},
		{
			name: "bitwise or rejects bool",
			src: `
func main(): int {
	let x = true | false
	return 0
}
`,
			wantErr: "operator \"|\" requires int operands",
		},
		{
			name: "shift left requires int operands",
			src: `
func main(): int {
	let x = "a" << "b"
	return 0
}
`,
			wantErr: "operator \"<<\" requires int operands",
		},
		{
			name: "and-not requires int operands",
			src: `
func main(): int {
	let x = 1.5 &^ 2.5
	return 0
}
`,
			wantErr: "operator \"&^\" requires int operands",
		},
		{
			name: "unary bitwise not requires int",
			src: `
func main(): int {
	let x = ~1.5
	return 0
}
`,
			wantErr: "unary ~ requires int",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, tt.src)
			if err == nil {
				t.Fatalf("Check() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check() = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCheck_OperatorErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "mismatched operand types",
			src: `
func main(): int {
	let x = 1 + 1.0
	return 0
}
`,
			wantErr: "mismatched operand types int and float",
		},
		{
			name: "modulo requires int",
			src: `
func main(): int {
	let x = 1.0 % 2.0
	return 0
}
`,
			wantErr: "operator % requires int operands",
		},
		{
			name: "logical and requires bool",
			src: `
func main(): int {
	let x = 1 && 2
	return 0
}
`,
			wantErr: "operator \"&&\" requires bool operands",
		},
		{
			name: "comparison rejects bool",
			src: `
func main(): int {
	let x = true < false
	return 0
}
`,
			wantErr: "operator \"<\" requires int, float, or string operands",
		},
		{
			name: "unary minus requires numeric operand",
			src: `
func main(): int {
	let x = -"oops"
	return 0
}
`,
			wantErr: "unary - requires int or float",
		},
		{
			name: "unary not requires bool operand",
			src: `
func main(): int {
	let x = !1
	return 0
}
`,
			wantErr: "unary ! requires bool",
		},
		{
			name: "binary operator rejects a nullable operand",
			src: `
func main(): int {
	let x: int? = 1
	let y = x + 1
	return 0
}
`,
			wantErr: "needs non-nullable operands",
		},
		{
			name: "string() rejects a string argument",
			src: `
func main(): int {
	let x = string("already a string")
	return 0
}
`,
			wantErr: "string() does not support string",
		},
		{
			name: "+ does not support bool",
			src: `
func main(): int {
	let x = true + false
	return 0
}
`,
			wantErr: "operator + does not support bool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, tt.src)
			if err == nil {
				t.Fatalf("Check() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check() = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCheck_Errors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "no main",
			src: `
func notMain(): int {
	return 0
}
`,
			wantErr: "no 'main' function found",
		},
		{
			name: "main takes a parameter",
			src: `
func main(x: int): int {
	return 0
}
`,
			wantErr: "must take no parameters",
		},
		{
			name: "main returns wrong type",
			src: `
func main(): string {
	return "0"
}
`,
			wantErr: "must return int",
		},
		{
			name: "reserved internal name",
			src: `
func cascade_main(): int {
	return 0
}
func main(): int {
	return 0
}
`,
			wantErr: "reserved name",
		},
		{
			name: "duplicate main",
			src: `
func main(): int {
	return 0
}
func main(): int {
	return 0
}
`,
			wantErr: "duplicate 'main'",
		},
		{
			name: "assign to const",
			src: `
func main(): int {
	const x: int = 1
	x = 2
	return 0
}
`,
			wantErr: "cannot assign to \"x\" (declared const)",
		},
		{
			name: "const without initializer",
			src: `
func main(): int {
	const x: int
	return 0
}
`,
			wantErr: "requires an initializer",
		},
		{
			name: "none into non-nullable type",
			src: `
func main(): int {
	let x: int = none
	return 0
}
`,
			wantErr: "cannot assign 'none' to non-nullable type int",
		},
		{
			name: "none without an explicit type cannot be inferred",
			src: `
func main(): int {
	let x = none
	return 0
}
`,
			wantErr: "cannot infer a type from 'none'",
		},
		{
			name: "undefined name on assignment",
			src: `
func main(): int {
	y = 1
	return 0
}
`,
			wantErr: "undefined name \"y\"",
		},
		{
			name: "redeclaration in the same scope",
			src: `
func main(): int {
	let x: int = 1
	let x: int = 2
	return 0
}
`,
			wantErr: "already declared in this scope",
		},
		{
			name: "type mismatch on assignment",
			src: `
func main(): int {
	let x: int = 1
	x = "oops"
	return 0
}
`,
			wantErr: "cannot assign string to int",
		},
		{
			name: "print rejects a nullable string (no narrowing yet)",
			src: `
func main(): int {
	let x: string? = "hi"
	print(x)
	return 0
}
`,
			wantErr: "print expects string, got string?",
		},
		{
			name: "return count mismatch",
			src: `
func main(): int {
	return
}
`,
			wantErr: "expected 1 return value(s), got 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, tt.src)
			if err == nil {
				t.Fatalf("Check() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check() = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}
