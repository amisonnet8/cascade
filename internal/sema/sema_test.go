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
