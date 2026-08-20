package codegen_test

import (
	"regexp"
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
		{"bitwise and", `let x = 1 & 2`, "\tBAND\t"},
		{"bitwise or", `let x = 1 | 2`, "\tBOR\t"},
		{"bitwise xor", `let x = 1 ^ 2`, "\tBXOR\t"},
		{"bitwise and-not", `let x = 1 &^ 2`, "\tBCLEAR\t"},
		{"shift left", `let x = 1 << 2`, "\tSHL\t"},
		{"shift right", `let x = 1 >> 2`, "\tSHR\t"},
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

	ir = generate(t, `
func main(): int {
	let x = 5
	let y = ~x
	return 0
}
`)
	if !strings.Contains(ir, "\tBNOT\t") {
		t.Fatalf("expected unary '~' to emit BNOT; got:\n%s", ir)
	}
}

// TestGenerate_ShiftPrecedenceLooserThanAdditive locks in
// cascade_spec.md §6's precedence table, which (unlike C/Go) places
// shift (priority 5) *looser* than +/- (priority 4): "4 << 1 + 1" must
// parse as "4 << (1 + 1)", so ADD must be emitted before SHL.
func TestGenerate_ShiftPrecedenceLooserThanAdditive(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let x = 4 << 1 + 1
	return 0
}
`)
	addIdx := strings.Index(ir, "\tADD\t")
	shlIdx := strings.Index(ir, "\tSHL\t")
	if addIdx == -1 || shlIdx == -1 || addIdx > shlIdx {
		t.Fatalf("expected ADD to be emitted before SHL (shift binds looser than +/-); got:\n%s", ir)
	}
}

// TestGenerate_BitAndPrecedenceTighterThanBitOr locks in §6's grouping of
// "&"/"&^" (priority 6) as tighter than "|"/"^" (priority 7): "1 | 2 & 3"
// must parse as "1 | (2 & 3)", so BAND must be emitted before BOR.
func TestGenerate_BitAndPrecedenceTighterThanBitOr(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let x = 1 | 2 & 3
	return 0
}
`)
	bandIdx := strings.Index(ir, "\tBAND\t")
	borIdx := strings.Index(ir, "\tBOR\t")
	if bandIdx == -1 || borIdx == -1 || bandIdx > borIdx {
		t.Fatalf("expected BAND to be emitted before BOR (& binds tighter than |); got:\n%s", ir)
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

// labelDefRe/labelRefRe/assertLabelsResolve check IR well-formedness at
// the label level: every "#Lx" a GOTO/IF/CASE... jumps to must actually
// be defined by a LABEL somewhere in the same IR. This is the kind of
// mistake seed_implementation_notes.md §1 warns is easy to make and easy
// to miss just by reading generated IR text.
var (
	labelDefRe = regexp.MustCompile(`(?m)^\tLABEL\t#(\w+)`)
	labelRefRe = regexp.MustCompile(`#(\w+)`)
)

func assertLabelsResolve(t *testing.T, ir string) {
	t.Helper()
	defined := map[string]bool{}
	for _, m := range labelDefRe.FindAllStringSubmatch(ir, -1) {
		defined[m[1]] = true
	}
	for _, m := range labelRefRe.FindAllStringSubmatch(ir, -1) {
		if !defined[m[1]] {
			t.Fatalf("label #%s is referenced but never defined by a LABEL; IR:\n%s", m[1], ir)
		}
	}
}

// TestGenerate_VarHoistingAcrossIfElifElse is the direct regression test
// for seed_implementation_notes.md §1: a `let` declared inside an elif/
// else body must still be hoisted above every IF/GOTO/LABEL in the
// function, or the generated Go fails with "goto jumps over variable
// declaration" the moment an earlier IF's jump skips past its clause.
func TestGenerate_VarHoistingAcrossIfElifElse(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let x = 1
	if x == 1 {
		let a = 10
		print(string(a))
	} elif x == 2 {
		let b = 20
		print(string(b))
	} else {
		let c = 30
		print(string(c))
	}
	return 0
}
`)
	mainBody := ir[:strings.Index(ir, "ENDFUNC")]
	lastVar := strings.LastIndex(mainBody, "\tVAR\t")
	firstControl := -1
	for _, instr := range []string{"\tIF\t", "\tGOTO\t", "\tLABEL\t"} {
		if i := strings.Index(mainBody, instr); i != -1 && (firstControl == -1 || i < firstControl) {
			firstControl = i
		}
	}
	if lastVar == -1 || firstControl == -1 || lastVar > firstControl {
		t.Fatalf("expected every VAR (including ones declared inside if/elif/else bodies) to precede every IF/GOTO/LABEL; got:\n%s", ir)
	}
	assertLabelsResolve(t, ir)
}

// TestGenerate_ControlFlowLabelsResolve is a broad well-formedness check
// across if/elif/else, while, switch (tagged and untagged), break, and
// continue together — every jump target must be defined.
func TestGenerate_ControlFlowLabelsResolve(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let i = 0
	while i < 10 {
		i++
		if i == 3 {
			continue
		}
		if i == 5 {
			break
		}
		switch i {
		case 1, 2:
			print("low")
		default:
			print("other")
		}
		switch {
		case i > 7:
			print("high")
		default:
			print("mid")
		}
	}
	return 0
}
`)
	assertLabelsResolve(t, ir)
}

// TestGenerate_BreakInSwitchDoesNotTargetEnclosingLoop locks in
// funcGen's separate break/continue stacks (cascade_spec.md §7: break
// exits the innermost loop *or* switch; continue always skips over a
// switch to reach the innermost loop). A switch pushes only a break
// target, never a continue target, so `continue` inside a switch must
// still resolve to the while's start label, not fail to compile.
func TestGenerate_ContinueInSwitchTargetsEnclosingLoop(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let i = 0
	while i < 5 {
		i++
		switch {
		case i == 2:
			continue
		}
	}
	return 0
}
`)
	assertLabelsResolve(t, ir)
	// The while loop's start label is the very first LABEL emitted.
	m := labelDefRe.FindStringSubmatch(ir)
	if m == nil {
		t.Fatalf("expected at least one LABEL in IR:\n%s", ir)
	}
	startLabel := m[1]
	if !strings.Contains(ir, "GOTO\t#"+startLabel+"\n") {
		t.Fatalf("expected continue (inside the switch) to GOTO the while's start label #%s; got:\n%s", startLabel, ir)
	}
}

func TestGenerate_NullCheck(t *testing.T) {
	// "is not none" reads the isset flag directly, with no extra
	// instruction.
	ir := generate(t, `
func main(): int {
	let x: int? = 1
	let ok = x is not none
	return 0
}
`)
	if !strings.Contains(ir, "SET\t%ok_2\t%x_1_isset\n") {
		t.Fatalf("expected 'is not none' to read the isset flag directly; got:\n%s", ir)
	}

	// "is none" negates it via NOT.
	ir = generate(t, `
func main(): int {
	let x: int? = 1
	let ok = x is none
	return 0
}
`)
	if !strings.Contains(ir, "\tNOT\t") {
		t.Fatalf("expected 'is none' to emit NOT over the isset flag; got:\n%s", ir)
	}
}

func TestGenerate_SwitchTaggedComparesWithEQ(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let day = 3
	switch day {
	case 1, 7:
		print("weekend")
	default:
		print("weekday")
	}
	return 0
}
`)
	if !strings.Contains(ir, "\tEQ\t") {
		t.Fatalf("expected a tagged switch to compare case values with EQ; got:\n%s", ir)
	}
}

func TestGenerate_SwitchUntaggedDoesNotCompareWithEQ(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let score = 85
	switch {
	case score >= 90:
		print("A")
	default:
		print("B")
	}
	return 0
}
`)
	if strings.Contains(ir, "\tEQ\t") {
		t.Fatalf("expected an untagged switch to use its case conditions directly, not EQ; got:\n%s", ir)
	}
	if !strings.Contains(ir, "\tGTE\t") {
		t.Fatalf("expected the '>=' case condition to emit GTE; got:\n%s", ir)
	}
}

func TestGenerate_CompoundAssignAndIncDec(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let x = 1
	x += 2
	x++
	let s = "a"
	s += "b"
	return 0
}
`)
	if !strings.Contains(ir, "ADD\t%x_1\t%x_1\t2\n") {
		t.Fatalf("expected 'x += 2' to emit ADD with the target reused as both operands; got:\n%s", ir)
	}
	if !strings.Contains(ir, "ADD\t%x_1\t%x_1\t1\n") {
		t.Fatalf("expected 'x++' to emit ADD x x 1; got:\n%s", ir)
	}
	if !strings.Contains(ir, "CONCAT\t%s_2\t%s_2\t\"b\"\n") {
		t.Fatalf("expected 's += \"b\"' on a string to emit CONCAT, not ADD; got:\n%s", ir)
	}
}

func TestGenerate_SLTYPEEmittedBeforeFUNC(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let xs: []int = [1, 2, 3]
	return 0
}
`)
	sltypeIdx := strings.Index(ir, "SLTYPE\t^intlist\t^int\n")
	funcIdx := strings.Index(ir, "FUNC\t!cascade_main")
	if sltypeIdx == -1 || funcIdx == -1 || sltypeIdx > funcIdx {
		t.Fatalf("expected SLTYPE to precede every FUNC block; got:\n%s", ir)
	}
}

func TestGenerate_ListLiteralUsesSLMAKEAndASET(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let xs: []int = [1, 2, 3]
	return 0
}
`)
	if !strings.Contains(ir, "SLMAKE\t%xs_1\t^intlist\t3\n") {
		t.Fatalf("expected a 3-element list literal to SLMAKE with size 3; got:\n%s", ir)
	}
	if !strings.Contains(ir, "ASET\t%xs_1\t0\t1\n") || !strings.Contains(ir, "ASET\t%xs_1\t1\t2\n") || !strings.Contains(ir, "ASET\t%xs_1\t2\t3\n") {
		t.Fatalf("expected each literal element to ASET at its own index; got:\n%s", ir)
	}
}

func TestGenerate_IndexReadAndWrite(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let xs = [1, 2, 3]
	let y = xs[1]
	xs[0] = 100
	return 0
}
`)
	if !strings.Contains(ir, "AGET\t%tmp_") || !strings.Contains(ir, "\t%xs_1\t1\n") {
		t.Fatalf("expected xs[1] to emit AGET; got:\n%s", ir)
	}
	if !strings.Contains(ir, "ASET\t%xs_1\t0\t100\n") {
		t.Fatalf("expected xs[0] = 100 to emit ASET directly (no reallocation); got:\n%s", ir)
	}
}

func TestGenerate_AppendReallocatesAndCopies(t *testing.T) {
	// append() must never emit a raw Go append() call — it always
	// SLMAKEs a fresh backing array and copies the old elements in, so
	// the original list is never mutated (see list.go's doc).
	ir := generate(t, `
func main(): int {
	let xs = [1, 2, 3]
	let ys = append(xs, 4)
	return 0
}
`)
	if strings.Contains(ir, "?append") {
		t.Fatalf("append() must not lower to Go's raw append(); got:\n%s", ir)
	}
	if !strings.Contains(ir, "CALL\t%tmp_") || !strings.Contains(ir, "?len\t%xs_1") {
		t.Fatalf("expected append() to first read len(xs); got:\n%s", ir)
	}
	if !strings.Contains(ir, "\tSLMAKE\t") {
		t.Fatalf("expected append() to SLMAKE a fresh backing array; got:\n%s", ir)
	}
	if !strings.Contains(ir, "\tAGET\t") {
		t.Fatalf("expected append() to copy old elements via AGET; got:\n%s", ir)
	}
	assertLabelsResolve(t, ir)
}

func TestGenerate_RangeBuildsListWithLoop(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let rs = range(1, 6)
	return 0
}
`)
	if !strings.Contains(ir, "\tSUB\t") {
		t.Fatalf("expected range() to compute size = to - from via SUB; got:\n%s", ir)
	}
	if !strings.Contains(ir, "\tSLMAKE\t") {
		t.Fatalf("expected range() to SLMAKE the result list; got:\n%s", ir)
	}
	assertLabelsResolve(t, ir)
}

func TestGenerate_LenCallsBareLen(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let xs = [1, 2, 3]
	let n = len(xs)
	return 0
}
`)
	if !strings.Contains(ir, "CALL\t%tmp_3\t:\t?len\t%xs_1\n") {
		t.Fatalf("expected len(xs) to emit a single ?len CALL; got:\n%s", ir)
	}
}

func TestGenerate_ForInLoop(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let xs = [1, 2, 3]
	let sum = 0
	for x in xs {
		if x == 2 {
			continue
		}
		if x == 3 {
			break
		}
		sum += x
	}
	return 0
}
`)
	if !strings.Contains(ir, "CALL\t%tmp_") || !strings.Contains(ir, "?len\t%xs_1") {
		t.Fatalf("expected for-in to compute len(xs) once up front; got:\n%s", ir)
	}
	if !strings.Contains(ir, "\tAGET\t") {
		t.Fatalf("expected for-in to AGET the current element each iteration; got:\n%s", ir)
	}
	assertLabelsResolve(t, ir)
}

func TestGenerate_ForInVarHoisted(t *testing.T) {
	// The for-in loop variable is declared inside the loop body
	// syntactically, but — like every other declaration — must still be
	// hoisted above the loop's own IF/GOTO/LABEL instructions.
	ir := generate(t, `
func main(): int {
	let xs = [1, 2, 3]
	for x in xs {
		print(string(x))
	}
	return 0
}
`)
	mainBody := ir[:strings.Index(ir, "ENDFUNC")]
	lastVar := strings.LastIndex(mainBody, "\tVAR\t")
	firstControl := -1
	for _, instr := range []string{"\tIF\t", "\tGOTO\t", "\tLABEL\t"} {
		if i := strings.Index(mainBody, instr); i != -1 && (firstControl == -1 || i < firstControl) {
			firstControl = i
		}
	}
	if lastVar == -1 || firstControl == -1 || lastVar > firstControl {
		t.Fatalf("expected every VAR (including the for-in loop variable) to precede every IF/GOTO/LABEL; got:\n%s", ir)
	}
}

func TestGenerate_EmptyListIsNil(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let xs: []int
	return 0
}
`)
	if !strings.Contains(ir, "SET\t%xs_1\tnil\n") {
		t.Fatalf("expected an uninitialized list declaration to SET nil; got:\n%s", ir)
	}
}

func TestGenerate_UserFunctionCallValue(t *testing.T) {
	ir := generate(t, `
func add(a: int, b: int): int {
	return a + b
}
func main(): int {
	let sum = add(3, 4)
	return 0
}
`)
	if !strings.Contains(ir, "FUNC\t!add\t^int\t^int\t:\t^int\n") {
		t.Fatalf("expected a FUNC declaration for add(); got:\n%s", ir)
	}
	if !strings.Contains(ir, "CALL\t%tmp_2\t:\t!add\t3\t4\n") {
		t.Fatalf("expected a single-result CALL to !add; got:\n%s", ir)
	}
}

func TestGenerate_VoidFunctionCallStmt(t *testing.T) {
	ir := generate(t, `
func log(message: string) {
	print(message)
}
func main(): int {
	log("hi")
	return 0
}
`)
	if !strings.Contains(ir, "FUNC\t!log\t^string\t:\n") {
		t.Fatalf("expected a FUNC declaration for log() with no result types; got:\n%s", ir)
	}
	if !strings.Contains(ir, "CALL\t:\t!log\t\"hi\"\n") {
		t.Fatalf("expected a result-less CALL to !log; got:\n%s", ir)
	}
}

func TestGenerate_MultiValueLetCall(t *testing.T) {
	ir := generate(t, `
func divmod(a: int, b: int): (int, int) {
	return a / b, a % b
}
func main(): int {
	let q, r = divmod(17, 5)
	let _, r2 = divmod(20, 6)
	return 0
}
`)
	if !strings.Contains(ir, "FUNC\t!divmod\t^int\t^int\t:\t^int\t^int\n") {
		t.Fatalf("expected a FUNC declaration for divmod() with two result types; got:\n%s", ir)
	}
	if !strings.Contains(ir, "CALL\t%q_1\t%r_2\t:\t!divmod\t17\t5\n") {
		t.Fatalf("expected a two-result CALL binding both names; got:\n%s", ir)
	}
	if !strings.Contains(ir, "CALL\t_\t%r2_") || !strings.Contains(ir, ":\t!divmod\t20\t6\n") {
		t.Fatalf("expected '_' to discard the first result; got:\n%s", ir)
	}
}

func TestGenerate_NullableParameterTwoSlotExpansion(t *testing.T) {
	// A nullable parameter must expand into two consecutive Go parameter
	// slots (value, then a ^bool isset flag) — there is no other way for
	// "is this none?" to cross a CALL boundary (see func.go's doc).
	ir := generate(t, `
func greet(name: string?): string {
	if name is none {
		return "hello stranger"
	}
	return "hello " + name
}
func main(): int {
	print(greet(none))
	print(greet("Cascade"))
	return 0
}
`)
	if !strings.Contains(ir, "FUNC\t!greet\t^string\t^bool\t:\t^string\n") {
		t.Fatalf("expected greet's nullable parameter to expand into ^string ^bool; got:\n%s", ir)
	}
	if !strings.Contains(ir, "!greet\t\"\"\tfalse\n") {
		t.Fatalf("expected greet(none) to pass (zero value, false); got:\n%s", ir)
	}
	if !strings.Contains(ir, "!greet\t\"Cascade\"\ttrue\n") {
		t.Fatalf("expected greet(\"Cascade\") to widen into (value, true); got:\n%s", ir)
	}
}

func TestGenerate_NullablePropagationThroughAssignment(t *testing.T) {
	// Regression test: assigning one nullable variable into another must
	// propagate its *current* isset flag, not hardcode "true" (see
	// genInit's and genNullableOperands's docs for the bug this fixes).
	ir := generate(t, `
func main(): int {
	let x: int? = none
	let y: int? = x
	return 0
}
`)
	if !strings.Contains(ir, "SET\t%y_2\t%x_1\n\tSET\t%y_2_isset\t%x_1_isset\n") {
		t.Fatalf("expected y's isset flag to be copied from x's own isset flag, not hardcoded; got:\n%s", ir)
	}
}

func TestGenerate_RecursiveFunctionCall(t *testing.T) {
	ir := generate(t, `
func factorial(n: int): int {
	if n <= 1 {
		return 1
	}
	return n * factorial(n - 1)
}
func main(): int {
	print(string(factorial(5)))
	return 0
}
`)
	if !strings.Contains(ir, "!factorial\t%tmp_") {
		t.Fatalf("expected factorial's own body to call itself via !factorial; got:\n%s", ir)
	}
	assertLabelsResolve(t, ir)
}
