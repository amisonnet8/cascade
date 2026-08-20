package codegen_test

import (
	"strings"
	"testing"

	"github.com/amisonnet8/cascade/internal/codegen"
	"github.com/amisonnet8/cascade/internal/parser"
	"github.com/amisonnet8/cascade/internal/sema"
)

// generate parses, checks, and compiles src, failing the test on any
// error — codegen.Generate assumes sema.Check already ran (see its doc
// comment), so every test case here goes through the full front end
// rather than hand-building an ast.File.
func generate(t *testing.T, src string) string {
	t.Helper()
	f, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if err := sema.Check(f); err != nil {
		t.Fatalf("sema error: %v", err)
	}
	ir, err := codegen.Generate(f)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	return ir
}

func TestGenerate_MainBridge(t *testing.T) {
	ir := generate(t, `
func main(): int {
	return 0
}
`)
	// The user's main must not be emitted as amivm's !main directly (see
	// codegen.go's package doc) — it must be renamed, and a wrapper must
	// bridge its return value out via os.Exit.
	if strings.Contains(ir, "FUNC\t!main\t:\t^int") {
		t.Fatalf("user's main must not be emitted as `!main` with a return type; got:\n%s", ir)
	}
	if !strings.Contains(ir, "FUNC\t!cascade_main\t:\t^int") {
		t.Fatalf("expected a FUNC !cascade_main : ^int block; got:\n%s", ir)
	}
	if !strings.Contains(ir, "CALL\t%exitcode\t:\t!cascade_main") || !strings.Contains(ir, "?os.Exit\t%exitcode") {
		t.Fatalf("expected !main to call !cascade_main and bridge its result to os.Exit; got:\n%s", ir)
	}
}

func TestGenerate_VarHoisting(t *testing.T) {
	// Every VAR must precede every SET/CALL in the function body, per
	// seed_implementation_notes.md §1 — this is what keeps a later GOTO
	// (added in Step 5) from ever jumping over a variable declaration.
	ir := generate(t, `
func main(): int {
	let a: int = 1
	print("x")
	let b: int = 2
	return 0
}
`)
	mainBody := ir[:strings.Index(ir, "ENDFUNC")] // just the !cascade_main block
	lastVar := strings.LastIndex(mainBody, "\tVAR\t")
	firstNonVar := -1
	for _, instr := range []string{"\tSET\t", "\tCALL\t"} {
		if i := strings.Index(mainBody, instr); i != -1 && (firstNonVar == -1 || i < firstNonVar) {
			firstNonVar = i
		}
	}
	if lastVar == -1 || firstNonVar == -1 || lastVar > firstNonVar {
		t.Fatalf("expected every VAR to precede every SET/CALL; got:\n%s", ir)
	}
}

func TestGenerate_NullableGetsIssetFlag(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let x: int? = 5
	x = none
	return 0
}
`)
	if !strings.Contains(ir, "VAR\t%x_1_isset\t^bool") {
		t.Fatalf("expected a companion _isset VAR for a nullable declaration; got:\n%s", ir)
	}
	if !strings.Contains(ir, "SET\t%x_1\t5\n\tSET\t%x_1_isset\ttrue") {
		t.Fatalf("expected assigning 5 to set the value and the isset flag to true; got:\n%s", ir)
	}
	if !strings.Contains(ir, "SET\t%x_1\t0\n\tSET\t%x_1_isset\tfalse") {
		t.Fatalf("expected assigning none to reset the value to zero and the isset flag to false; got:\n%s", ir)
	}
}

func TestGenerate_NonNullableHasNoIssetFlag(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let x: int = 5
	return 0
}
`)
	if strings.Contains(ir, "_isset") {
		t.Fatalf("a non-nullable declaration must not get an _isset companion variable; got:\n%s", ir)
	}
}

func TestGenerate_BinaryOperators(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		wantIR string
	}{
		{"add", `let x = 1 + 2`, "\tADD\t"},
		{"sub", `let x = 1 - 2`, "\tSUB\t"},
		{"mul", `let x = 1 * 2`, "\tMUL\t"},
		{"div", `let x = 1 / 2`, "\tDIV\t"},
		{"mod", `let x = 1 % 2`, "\tMOD\t"},
		{"string concat", `let x = "a" + "b"`, "\tCONCAT\t"},
		{"eq", `let x = 1 == 2`, "\tEQ\t"},
		{"neq", `let x = 1 != 2`, "\tNEQ\t"},
		{"lt", `let x = 1 < 2`, "\tLT\t"},
		{"lte", `let x = 1 <= 2`, "\tLTE\t"},
		{"gt", `let x = 1 > 2`, "\tGT\t"},
		{"gte", `let x = 1 >= 2`, "\tGTE\t"},
		{"and", `let x = true && false`, "\tAND\t"},
		{"or", `let x = true || false`, "\tOR\t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ir := generate(t, "func main(): int {\n\t"+tt.src+"\n\treturn 0\n}\n")
			if !strings.Contains(ir, tt.wantIR) {
				t.Fatalf("expected IR to contain %q; got:\n%s", tt.wantIR, ir)
			}
		})
	}
}

func TestGenerate_UnaryOperators(t *testing.T) {
	// AMIVM-IR has no unary-minus instruction, so "-x" must be emitted as
	// "SUB tmp 0 x" (seed_implementation_notes.md §5.6).
	ir := generate(t, `
func main(): int {
	let x = 5
	let y = -x
	return 0
}
`)
	if !strings.Contains(ir, "SUB\t%tmp_") || !strings.Contains(ir, "\t0\t%x_1") {
		t.Fatalf("expected unary '-' to emit SUB tmp 0 x; got:\n%s", ir)
	}

	ir = generate(t, `
func main(): int {
	let x = true
	let y = !x
	return 0
}
`)
	if !strings.Contains(ir, "\tNOT\t") {
		t.Fatalf("expected unary '!' to emit NOT; got:\n%s", ir)
	}
}

func TestGenerate_PrecedenceMultiplicationBeforeAddition(t *testing.T) {
	// "2 + 3 * 4" must compute the MUL into a temp before the ADD
	// consumes it — i.e. MUL appears first in the emitted instruction
	// order — which is exactly what §6's precedence table requires.
	ir := generate(t, `
func main(): int {
	let x = 2 + 3 * 4
	return 0
}
`)
	mulIdx := strings.Index(ir, "\tMUL\t")
	addIdx := strings.Index(ir, "\tADD\t")
	if mulIdx == -1 || addIdx == -1 || mulIdx > addIdx {
		t.Fatalf("expected MUL to be emitted before ADD (precedence); got:\n%s", ir)
	}
}

func TestGenerate_StringConversion(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantCall string
	}{
		{"int", `print(string(1))`, "?strconv.Itoa"},
		{"float", `print(string(1.5))`, "?strconv.FormatFloat"},
		{"bool", `print(string(true))`, "?strconv.FormatBool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ir := generate(t, "func main(): int {\n\t"+tt.src+"\n\treturn 0\n}\n")
			if !strings.Contains(ir, tt.wantCall) {
				t.Fatalf("expected IR to call %s; got:\n%s", tt.wantCall, ir)
			}
		})
	}
}
