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
// the label level, in both directions: every "#Lx" a GOTO/IF/CASE...
// jumps to must actually be defined by a LABEL somewhere in the same IR,
// and (just as importantly, since amivm's IF/LOOP migration) every
// defined LABEL must actually be referenced by at least one GOTO —
// go/types rejects an unreferenced Go label the same way it rejects an
// unused variable, exactly the class of bug this caught in
// genSwitchStmt's own end-of-switch label before its unconditional
// fallthrough GOTO was added (a switch whose body never calls `break`
// left that label defined but never jumped to). This is the kind of
// mistake seed_implementation_notes.md §1/§2.2 warns is easy to make and
// easy to miss just by reading generated IR text.
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
	// Non-defining references only: strip every LABEL-definition line
	// first, so a label's own `LABEL #Lx` line doesn't count as a
	// reference to itself (every defined label would otherwise trivially
	// look "referenced", defeating the point of the check below).
	nonLabelLines := labelDefRe.ReplaceAllString(ir, "")
	referenced := map[string]bool{}
	for _, m := range labelRefRe.FindAllStringSubmatch(nonLabelLines, -1) {
		referenced[m[1]] = true
		if !defined[m[1]] {
			t.Fatalf("label #%s is referenced but never defined by a LABEL; IR:\n%s", m[1], ir)
		}
	}
	for label := range defined {
		if !referenced[label] {
			t.Fatalf("label #%s is defined by a LABEL but never referenced by anything (go/types would reject this as an unused label); IR:\n%s", label, ir)
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
	// switch never pushes its own continueTarget (cascade_spec.md §7:
	// "switch自体はループではない"), so `continue` inside a switch body
	// finds the enclosing while's continueTarget on the stack — which for
	// a while loop is native (see genWhileStmt), meaning this compiles to
	// a plain CONTINUE that Go's own scoping rules carry straight past
	// the switch's nested IF/ELSE blocks to the LOOP, with no LABEL/GOTO
	// needed at all.
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
	if !strings.Contains(ir, "\tIF\t%tmp_4\n\tCONTINUE\n\tENDIF\n") {
		t.Fatalf("expected continue (inside the switch) to compile to a native CONTINUE directly inside the switch's own IF, with no GOTO of its own; got:\n%s", ir)
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

func TestGenerate_StructDeclEmitsSTTYPE(t *testing.T) {
	ir := generate(t, `
struct Point {
	x: float
	y: float
}
func main(): int {
	let p = Point{x: 1.0, y: 2.0}
	return 0
}
`)
	if !strings.Contains(ir, "STTYPE\t^Point\nFIELD\t>x\t^float64\nFIELD\t>y\t^float64\nENDSTTYPE\n") {
		t.Fatalf("expected a STTYPE/FIELD/ENDSTTYPE block for Point; got:\n%s", ir)
	}
	if !strings.Contains(ir, "STTYPE") || strings.Index(ir, "STTYPE") > strings.Index(ir, "FUNC") {
		t.Fatalf("expected STTYPE to precede every FUNC block; got:\n%s", ir)
	}
}

func TestGenerate_StructLiteralUsesFSET(t *testing.T) {
	ir := generate(t, `
struct Point {
	x: float
	y: float
}
func main(): int {
	let p = Point{x: 1.0, y: 2.0}
	return 0
}
`)
	if !strings.Contains(ir, "FSET\t%tmp_2\t>x\t1\n\tFSET\t%tmp_2\t>y\t2\n") {
		t.Fatalf("expected the struct literal to FSET each field; got:\n%s", ir)
	}
}

func TestGenerate_FieldReadAndWrite(t *testing.T) {
	ir := generate(t, `
struct Point {
	x: float
	y: float
}
func main(): int {
	let p = Point{x: 1.0, y: 2.0}
	p.x = 5.0
	let x = p.x
	return 0
}
`)
	if !strings.Contains(ir, "FSET\t%p_1\t>x\t5\n") {
		t.Fatalf("expected p.x = 5.0 to compile to an FSET; got:\n%s", ir)
	}
	if !strings.Contains(ir, "FGET\t%tmp_4\t%p_1\t>x\n") {
		t.Fatalf("expected p.x read to compile to an FGET; got:\n%s", ir)
	}
}

func TestGenerate_StructZeroResetIsRecursive(t *testing.T) {
	// A bare `let ln: Line` (no initializer) must zero-reset every field,
	// including a nested struct field, via FSET — there is no "zero
	// struct literal" SET token (see struct.go's genStructZeroReset).
	ir := generate(t, `
struct Point {
	x: float
	y: float
}
struct Line {
	start: Point
	end: Point
}
func main(): int {
	let ln: Line
	return 0
}
`)
	if !strings.Contains(ir, "FSET\t%tmp_2\t>x\t0\n\tFSET\t%tmp_2\t>y\t0\n\tFSET\t%ln_1\t>start\t%tmp_2\n") {
		t.Fatalf("expected ln.start's own fields to be zero-reset into a temp and copied in via FSET; got:\n%s", ir)
	}
}

func TestGenerate_PointerAddrDerefAndPset(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let v: int = 100
	let p: *int = &v
	let copy: int = *p
	*p = 200
	return 0
}
`)
	if !strings.Contains(ir, "ADDR\t%tmp_3\t%v_1\n\tSET\t%p_2\t%tmp_3\n") {
		t.Fatalf("expected &v to compile to ADDR into a temp, then SET into p; got:\n%s", ir)
	}
	if !strings.Contains(ir, "PGET\t%tmp_5\t%p_2\n") {
		t.Fatalf("expected *p read to compile to PGET; got:\n%s", ir)
	}
	if !strings.Contains(ir, "PSET\t%p_2\t200\n") {
		t.Fatalf("expected *p = 200 to compile to PSET; got:\n%s", ir)
	}
	if !strings.Contains(ir, "VAR\t%p_2\t^*int\n") {
		t.Fatalf("expected p's declared type to be ^*int; got:\n%s", ir)
	}
}

func TestGenerate_AddrOfFieldAndIndexUsePointOperand(t *testing.T) {
	// Regression test: amivm's ADDR gained an optional "point" operand
	// (a field name or index — amivm_spec.md §4.2/§5) after Cascade's
	// isAddressable originally restricted `&` to a bare variable only
	// (see isAddressable's doc). genAddrOfOperand must resolve the base
	// variable directly and fold the field/index into ADDR's point —
	// never genValue the field/index into a copy first (that would ADDR
	// the copy, not the original storage; see genAddrOfOperand's doc).
	ir := generate(t, `
struct Point {
	x: int
	y: int
}
func main(): int {
	let pt: Point = Point{x: 1, y: 2}
	let px: *int = &pt.x

	let xs: []int = [10, 20, 30]
	let idx: int = 1
	let pe: *int = &xs[idx]
	return 0
}
`)
	if !strings.Contains(ir, "ADDR\t%tmp_4\t%pt_1\t>x\n") {
		t.Fatalf("expected &pt.x to compile to ADDR with a >x field point operand; got:\n%s", ir)
	}
	if !strings.Contains(ir, "ADDR\t%tmp_8\t%xs_5\t%idx_6\n") {
		t.Fatalf("expected &xs[idx] to compile to ADDR with idx as its index point operand; got:\n%s", ir)
	}
	if strings.Contains(ir, "FGET") || strings.Contains(ir, "AGET") {
		t.Fatalf("must not read the field/index into a copy before ADDRing it; got:\n%s", ir)
	}
}

func TestGenerate_MethodCallAutoAddrOnFieldAndIndex(t *testing.T) {
	// Companion to TestGenerate_AddrOfFieldAndIndexUsePointOperand: the
	// same widened addressability applies to a pointer-receiver method's
	// implicit auto-address-of (cascade_spec.md §8.2, CallExpr.
	// RecvNeedsAddr) — genReceiverOperand must route through
	// genAddrOfOperand directly on the receiver expression, not genValue
	// a copy and ADDR that. Used in value position (a `let`'s
	// initializer) rather than as its own bare statement, since
	// parseFieldOrMethodStmt's statement-position shortcut only supports
	// a single dotted level (b.pt.bump() as its own statement is an
	// unrelated, pre-existing parser limitation — see its doc) while
	// general expression parsing (parseSelectorSuffix) has no such limit.
	ir := generate(t, `
struct Point {
	x: int
}
func (p: *Point) getX(): int {
	return p.x
}
struct Box {
	pt: Point
}
func main(): int {
	let b: Box = Box{pt: Point{x: 1}}
	let v: int = b.pt.getX()
	return 0
}
`)
	if !strings.Contains(ir, "ADDR\t%tmp_5\t%b_1\t>pt\n") {
		t.Fatalf("expected b.pt.getX()'s auto-address-of to compile to ADDR with a >pt field point operand (directly off %%b_1, not a copy of b.pt); got:\n%s", ir)
	}
}

func TestGenerate_PointerNullCheckUsesEQNilNotIssetFlag(t *testing.T) {
	// A pointer has no isset flag of its own (its nullability is native Go
	// nil — see ast.Type's doc) — `is none`/`is not none` on one must
	// compile to EQ/NEQ against the literal nil instead (see genNullCheck).
	ir := generate(t, `
func main(): int {
	let p: *int = none
	if p is none {
		return 1
	}
	if p is not none {
		return 2
	}
	return 0
}
`)
	if !strings.Contains(ir, "EQ\t%tmp_2\t%p_1\tnil\n") {
		t.Fatalf("expected 'p is none' to compile to EQ against nil; got:\n%s", ir)
	}
	if !strings.Contains(ir, "NEQ\t%tmp_3\t%p_1\tnil\n") {
		t.Fatalf("expected 'p is not none' to compile to NEQ against nil; got:\n%s", ir)
	}
	if strings.Contains(ir, "_isset") {
		t.Fatalf("a pointer variable must never get a companion isset flag; got:\n%s", ir)
	}
}

func TestGenerate_MethodCallValueReceiver(t *testing.T) {
	ir := generate(t, `
struct Point {
	x: float
	y: float
}
func (p: Point) sum(): float {
	return p.x + p.y
}
func main(): int {
	let pt = Point{x: 3.0, y: 4.0}
	let s = pt.sum()
	return 0
}
`)
	if !strings.Contains(ir, "FUNC\t!Point_sum\t^Point\t:\t^float64\n") {
		t.Fatalf("expected the method's own FUNC to be named !Point_sum with the receiver as its first param; got:\n%s", ir)
	}
	if !strings.Contains(ir, "!Point_sum\t%pt_1\n") {
		t.Fatalf("expected the call site to pass pt directly (no ADDR/PGET needed for a matching value receiver); got:\n%s", ir)
	}
}

func TestGenerate_MethodCallAutoAddrAndDeref(t *testing.T) {
	// A value receiver's method called on a pointer variable must PGET
	// first (auto-deref); a pointer receiver's method called on a plain
	// variable must ADDR first (auto-address-of) — cascade_spec.md §8.2.
	ir := generate(t, `
struct Point {
	x: float
	y: float
}
func (p: Point) sum(): float {
	return p.x + p.y
}
func (p: *Point) scale(factor: float) {
	p.x = p.x * factor
}
func main(): int {
	let pt = Point{x: 3.0, y: 4.0}
	pt.scale(2.0)

	let ptr: *Point = &pt
	let s = ptr.sum()
	return 0
}
`)
	if !strings.Contains(ir, "ADDR\t%tmp_3\t%pt_1\n\tCALL\t:\t!Point_scale\t%tmp_3\t2\n") {
		t.Fatalf("expected pt.scale(2.0) to auto-ADDR pt before the call; got:\n%s", ir)
	}
	if !strings.Contains(ir, "PGET\t%tmp_") || !strings.Contains(ir, "!Point_sum\t%tmp_") {
		t.Fatalf("expected ptr.sum() to auto-PGET ptr before the call; got:\n%s", ir)
	}
}

func TestGenerate_ClosureLitEmitsFNTYPEAndCLOS(t *testing.T) {
	ir := generate(t, `
func makeAdder(base: int): func(int): int {
	return func(n: int): int {
		return base + n
	}
}
func main(): int {
	let add5 = makeAdder(5)
	print(string(add5(10)))
	return 0
}
`)
	if !strings.Contains(ir, "FNTYPE\t^FuncType1\t^int\t:\t^int\n") {
		t.Fatalf("expected an FNTYPE deftype for func(int): int; got:\n%s", ir)
	}
	if !strings.Contains(ir, "CLOS\t%tmp_1\t^int\t:\t^int\n") {
		t.Fatalf("expected the closure literal to compile to a CLOS block; got:\n%s", ir)
	}
	if !strings.Contains(ir, "ADD\t%tmp_1\t$1\t&1-1\n") {
		t.Fatalf("expected the closure body to reference its own param via &1-1 (depth 1, param 1) and the captured outer param via $1; got:\n%s", ir)
	}
	if !strings.Contains(ir, "ENDCLOS\n") {
		t.Fatalf("expected the CLOS block to be closed with ENDCLOS; got:\n%s", ir)
	}
}

func TestGenerate_ClosureCapturesAndMutatesOuterVariable(t *testing.T) {
	// Regression test for real Go closure capture (cascade_spec.md §8.3):
	// a captured variable must be referenced by the *same* token inside
	// the CLOS body as outside it, with no copying — see genClosureLit's
	// doc, mirroring amivm/test_ir/15_closure.ir's %count example.
	ir := generate(t, `
func makeCounter(): func(): int {
	let count = 0
	return func(): int {
		count += 1
		return count
	}
}
func main(): int {
	let counter = makeCounter()
	print(string(counter()))
	return 0
}
`)
	if !strings.Contains(ir, "VAR\t%count_1\t^int\n") {
		t.Fatalf("expected count to be hoisted in makeCounter's own outer scope; got:\n%s", ir)
	}
	if !strings.Contains(ir, "ADD\t%count_1\t%count_1\t1\n") {
		t.Fatalf("expected the closure body to mutate the captured %%count_1 directly, not a copy; got:\n%s", ir)
	}
}

func TestGenerate_NestedClosureLitUsesQualifiedParamTokens(t *testing.T) {
	// Regression test: amivm's CLOS gained nesting support (a closure
	// literal may now itself contain another — see genClosureLitInto's
	// doc), with each nesting depth's own parameters addressed via a
	// fully-qualified &depth-N token rather than the old flat &N. This
	// checks the classic curry(a)(b) = a + b shape: the outer CLOS
	// (depth 1) must declare its own parameter as &1-1, and the inner
	// CLOS (depth 2, nested inside the outer's own body) must reference
	// both its own parameter (&2-1) and the captured outer one (&1-1) —
	// never a bare &1/&2, which would be ambiguous once nesting exists.
	ir := generate(t, `
func curry(): func(int): func(int): int {
	return func(a: int): func(int): int {
		return func(b: int): int {
			return a + b
		}
	}
}
func main(): int {
	return 0
}
`)
	if !strings.Contains(ir, "CLOS\t%tmp_1\t^int\t:\t^FuncType1\n") {
		t.Fatalf("expected the outer (depth-1) closure's own CLOS header; got:\n%s", ir)
	}
	if !strings.Contains(ir, "CLOS\t%tmp_1\t^int\t:\t^int\n") {
		t.Fatalf("expected the inner (depth-2) closure's own CLOS header; got:\n%s", ir)
	}
	if !strings.Contains(ir, "ADD\t%tmp_1\t&1-1\t&2-1\n") {
		t.Fatalf("expected the innermost body to add the captured outer param (&1-1) and its own param (&2-1); got:\n%s", ir)
	}
	if strings.Contains(ir, "&1\t") || strings.Contains(ir, "\t&1\n") || strings.Contains(ir, "&2\t") || strings.Contains(ir, "\t&2\n") {
		t.Fatalf("must never emit a bare (unqualified) &N closure-parameter token now that CLOS can nest; got:\n%s", ir)
	}
}

func TestGenerate_ClosureLitAssignedToLocalSkipsTempCopy(t *testing.T) {
	// Regression test: amivm's CLOS target category was widened from
	// "local" (%xxx only) to "shallow" ($N/%xxx/@xxx — amivm_spec.md
	// §4.17/§5), so `let f = func(...) {...}` (or a plain reassignment)
	// can CLOS straight into the destination variable instead of a fresh
	// temp followed by a redundant SET — see genClosureLitInto's doc.
	ir := generate(t, `
func main(): int {
	let f: func(int): int = func(x: int): int {
		return x + 1
	}
	print(string(f(4)))
	return 0
}
`)
	if !strings.Contains(ir, "CLOS\t%f_1\t^int\t:\t^int\n") {
		t.Fatalf("expected the closure literal to CLOS directly into %%f_1; got:\n%s", ir)
	}
	if strings.Contains(ir, "SET\t%f_1\t%tmp") {
		t.Fatalf("must not copy the closure through a temp before assigning it to f; got:\n%s", ir)
	}
}

func TestGenerate_ClosureLitAssignedToGlobalSkipsTempCopy(t *testing.T) {
	// Companion to the local-variable case above: a top-level `let`
	// closure initializer (compiled inside cascade_init — cascade_spec.md
	// §11.3) CLOSes directly into its @xxx global too, since "shallow"
	// includes @xxx alongside $N/%xxx.
	ir := generate(t, `
let doubler: func(int): int = func(x: int): int {
	return x * 2
}
func main(): int {
	print(string(doubler(21)))
	return 0
}
`)
	if !strings.Contains(ir, "CLOS\t@doubler\t^int\t:\t^int\n") {
		t.Fatalf("expected the closure literal to CLOS directly into @doubler; got:\n%s", ir)
	}
	if strings.Contains(ir, "SET\t@doubler\t%tmp") {
		t.Fatalf("must not copy the closure through a temp before assigning it to the global; got:\n%s", ir)
	}
}

func TestGenerate_ClosureCallThroughGlobalUsesAtTokenDirectly(t *testing.T) {
	// Regression test: amivm's callname category was widened to accept
	// @xxx directly (amivm_spec.md §5), so calling a closure held in a
	// global variable no longer needs a copy-to-temp first — see
	// closureCallTarget's doc.
	ir := generate(t, `
let doubler: func(int): int = func(x: int): int {
	return x * 2
}
func main(): int {
	print(string(doubler(21)))
	return 0
}
`)
	if !strings.Contains(ir, "CALL\t%tmp_1\t:\t@doubler\t21\n") {
		t.Fatalf("expected the call to use @doubler directly as its callname; got:\n%s", ir)
	}
	if strings.Contains(ir, "SET\t%tmp_1\t@doubler\n") {
		t.Fatalf("must not copy the global closure into a temp before calling through it; got:\n%s", ir)
	}
}

func TestGenerate_ClosureCallThroughParameterUsesDollarTokenDirectly(t *testing.T) {
	// Regression test: amivm's CALL callname category accepts
	// %xxx/@xxx/!xxx/?xxx/$N/&N (the $N/&N addition came after Cascade's
	// Step 9 originally worked around their absence by copying into a
	// local temp first — see closureCallTarget's doc), so calling a
	// closure held in a plain function parameter no longer needs a copy.
	ir := generate(t, `
func applyTwice(f: func(int): int, x: int): int {
	return f(f(x))
}
func main(): int {
	return 0
}
`)
	if strings.Contains(ir, "SET\t%tmp_1\t$1\n") {
		t.Fatalf("closure parameter should be called through $1 directly, without a copy-to-temp; got:\n%s", ir)
	}
	if !strings.Contains(ir, "CALL\t%tmp_1\t:\t$1\t") {
		t.Fatalf("expected the closure call to use the $1 parameter token directly as callname; got:\n%s", ir)
	}
}

func TestGenerate_FilterMapReduce(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let numbers: []int = [1, 2, 3, 4, 5, 6]
	let evens = filter(numbers, func(n: int): bool {
		return n % 2 == 0
	})
	let doubled = map(numbers, func(n: int): int {
		return n * 2
	})
	let total = reduce(numbers, 0, func(acc: int, n: int): int {
		return acc + n
	})
	print(string(len(evens)) + string(len(doubled)) + string(total))
	return 0
}
`)
	if !strings.Contains(ir, "\tSLICE\t") {
		t.Fatalf("expected filter() to trim its over-allocated result via SLICE; got:\n%s", ir)
	}
	if !strings.Contains(ir, "\tSLMAKE\t") {
		t.Fatalf("expected map()/filter() to SLMAKE their result lists; got:\n%s", ir)
	}
	assertLabelsResolve(t, ir)
}

func TestGenerate_PointerEqualityUsesEQ(t *testing.T) {
	ir := generate(t, `
struct Point {
	x: int
}
func main(): int {
	let p: Point = Point{x: 1}
	let a: *Point = &p
	let b: *Point = &p
	let same = a == b
	print(string(same))
	return 0
}
`)
	if !strings.Contains(ir, "\tEQ\t") {
		t.Fatalf("expected pointer equality to compile to EQ; got:\n%s", ir)
	}
}

func TestGenerate_MapLiteralUsesMPMAKEAndMSET(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let counts: map<string, int> = {"a": 1, "b": 2}
	return 0
}
`)
	if !strings.Contains(ir, "MPTYPE\t^MapType1\t^string\t^int\n") {
		t.Fatalf("expected an MPTYPE deftype for map<string, int>; got:\n%s", ir)
	}
	if !strings.Contains(ir, "MPMAKE\t%counts_1\t^MapType1\n") {
		t.Fatalf("expected the map literal to MPMAKE its target; got:\n%s", ir)
	}
	if !strings.Contains(ir, "MSET\t%counts_1\t\"a\"\t1\n\tMSET\t%counts_1\t\"b\"\t2\n") {
		t.Fatalf("expected each pair to compile to an MSET; got:\n%s", ir)
	}
}

func TestGenerate_MapIndexReadUsesCommaOkMGET(t *testing.T) {
	// Regression test: assigning m[k] into a nullable-typed variable must
	// use MGET's comma-ok form (both value and ok operands from one
	// instruction), not a value-only MGET plus a hardcoded "true" isset
	// flag — see genNullableOperands's IndexExpr case.
	ir := generate(t, `
func main(): int {
	let counts: map<string, int> = {"a": 1}
	let v = counts["a"]
	return 0
}
`)
	if !strings.Contains(ir, "MGET\t%tmp_3\t%tmp_4\t%counts_1\t\"a\"\n\tSET\t%v_2\t%tmp_3\n\tSET\t%v_2_isset\t%tmp_4\n") {
		t.Fatalf("expected a comma-ok MGET feeding both v's value and isset directly; got:\n%s", ir)
	}
}

func TestGenerate_MapIndexAssignUsesMSET(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let counts: map<string, int> = {"a": 1}
	counts["b"] = 2
	return 0
}
`)
	if !strings.Contains(ir, "MSET\t%counts_1\t\"b\"\t2\n") {
		t.Fatalf("expected counts[\"b\"] = 2 to compile to MSET; got:\n%s", ir)
	}
}

func TestGenerate_NonNullableEmptyMapUsesMPMAKENotNil(t *testing.T) {
	// Regression test: a non-nullable map with no initializer must
	// allocate a real, writable empty map via MPMAKE — writing into a nil
	// Go map panics, unlike appending to a nil slice — see
	// genResetToZero's doc.
	ir := generate(t, `
func main(): int {
	let empty: map<string, int>
	empty["x"] = 1
	return 0
}
`)
	if !strings.Contains(ir, "MPMAKE\t%empty_1\t^MapType1\n") {
		t.Fatalf("expected a bare 'let empty: map<string, int>' to MPMAKE, not SET nil; got:\n%s", ir)
	}
	if strings.Contains(ir, "SET\t%empty_1\tnil") {
		t.Fatalf("a non-nullable empty map must never be SET to nil; got:\n%s", ir)
	}
}

func TestGenerate_DeleteCallsBuiltinDelete(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let counts: map<string, int> = {"a": 1}
	delete(counts, "a")
	return 0
}
`)
	if !strings.Contains(ir, "CALL\t:\t?delete\t%counts_1\t\"a\"\n") {
		t.Fatalf("expected delete(counts, \"a\") to compile to a CALL against Go's builtin delete; got:\n%s", ir)
	}
}

func TestGenerate_TwoVariableForInUsesMPKEYS(t *testing.T) {
	ir := generate(t, `
func main(): int {
	let counts: map<string, int> = {"a": 1, "b": 2}
	let total = 0
	for k, v in counts {
		total += v
		print(k)
	}
	return 0
}
`)
	if !strings.Contains(ir, "MPKEYS\t%tmp_3\t%counts_1\n") {
		t.Fatalf("expected the two-variable for-in to call MPKEYS once; got:\n%s", ir)
	}
	if !strings.Contains(ir, "\tMGET\t%v_") {
		t.Fatalf("expected each iteration to MGET the value for its key; got:\n%s", ir)
	}
	assertLabelsResolve(t, ir)
}

func TestGenerate_ErrorStructEmitsSTTYPE(t *testing.T) {
	ir := generate(t, `
func main(): int {
	return 0
}
`)
	if !strings.Contains(ir, "STTYPE\t^error\nFIELD\t>message\t^string\nENDSTTYPE\n") {
		t.Fatalf("expected the built-in error type to always emit an STTYPE block; got:\n%s", ir)
	}
}

func TestGenerate_ErrorCallUsesFSET(t *testing.T) {
	ir := generate(t, `
func f(): (int, error?) {
	return 0, error("boom")
}
func main(): int {
	return 0
}
`)
	if !strings.Contains(ir, "FSET\t%tmp_1\t>message\t\"boom\"\n") {
		t.Fatalf("expected error(\"boom\") to compile to an FSET of the message field; got:\n%s", ir)
	}
}

func TestGenerate_NullableResultExpandsRETAndFUNC(t *testing.T) {
	// Regression test for the core Step 11 mechanism: a nullable RESULT
	// type needs the same two-slot (value, isset) expansion a nullable
	// PARAMETER already gets, applied to both FUNC's own declared result
	// list and every RET inside its body.
	ir := generate(t, `
func divide(a: int, b: int): (int, error?) {
	if b == 0 {
		return 0, error("division by zero")
	}
	return a / b, none
}
func main(): int {
	return 0
}
`)
	if !strings.Contains(ir, "FUNC\t!divide\t^int\t^int\t:\t^int\t^error\t^bool\n") {
		t.Fatalf("expected divide's FUNC declaration to expand its error? result into (^error, ^bool); got:\n%s", ir)
	}
	if !strings.Contains(ir, "RET\t0\t%tmp_2\ttrue\n") {
		t.Fatalf("expected 'return 0, error(...)' to RET the error value plus an explicit true isset; got:\n%s", ir)
	}
	if !strings.Contains(ir, "RET\t%tmp_3\t%tmp_4\tfalse\n") {
		t.Fatalf("expected 'return a / b, none' to RET the zero error value plus an explicit false isset; got:\n%s", ir)
	}
}

func TestGenerate_MultiLetNullableResultGetsIssetFlag(t *testing.T) {
	ir := generate(t, `
func divide(a: int, b: int): (int, error?) {
	if b == 0 {
		return 0, error("division by zero")
	}
	return a / b, none
}
func main(): int {
	let q, err = divide(10, 2)
	if err is not none {
		print(err.message)
	}
	return 0
}
`)
	if !strings.Contains(ir, "VAR\t%err_2\t^error\n\tVAR\t%err_2_isset\t^bool\n") {
		t.Fatalf("expected the multi-let's error result to get a companion isset VAR; got:\n%s", ir)
	}
	if !strings.Contains(ir, "CALL\t%q_1\t%err_2\t%err_2_isset\t:\t!divide\t10\t2\n") {
		t.Fatalf("expected the CALL to request all three result operands (value, error, isset); got:\n%s", ir)
	}
}

func TestGenerate_ErrorPropagationEarlyReturns(t *testing.T) {
	ir := generate(t, `
func divide(a: int, b: int): (int, error?) {
	if b == 0 {
		return 0, error("division by zero")
	}
	return a / b, none
}
func loadAndDouble(a: int, b: int): (int, error?) {
	let result = divide(a, b)?
	return result * 2, none
}
func main(): int {
	return 0
}
`)
	if !strings.Contains(ir, "CALL\t%tmp_2\t%tmp_3\t%tmp_4\t:\t!divide\t$1\t$2\n") {
		t.Fatalf("expected 'divide(a, b)?' to request all three of divide's result operands; got:\n%s", ir)
	}
	if !strings.Contains(ir, "IF\t%tmp_4\n") {
		t.Fatalf("expected the propagation check to branch on divide's own isset flag; got:\n%s", ir)
	}
	if !strings.Contains(ir, "RET\t0\t%tmp_3\ttrue\n") {
		t.Fatalf("expected the early return to propagate divide's own error value with true; got:\n%s", ir)
	}
	assertLabelsResolve(t, ir)
}

func TestGenerate_BareStatementErrorPropagation(t *testing.T) {
	ir := generate(t, `
func saveResult(n: int): (int, error?) {
	return n, none
}
func process(n: int): (int, error?) {
	saveResult(n)?
	return n * 10, none
}
func main(): int {
	return 0
}
`)
	if !strings.Contains(ir, "CALL\t%tmp_1\t%tmp_2\t%tmp_3\t:\t!saveResult\t$1\n") {
		t.Fatalf("expected the bare 'saveResult(n)?' statement to still call and check the error; got:\n%s", ir)
	}
	assertLabelsResolve(t, ir)
}

func TestGenerate_SingleNullableResultUsesCommaOkPattern(t *testing.T) {
	ir := generate(t, `
func mustBePositive(n: int): error? {
	if n < 0 {
		return error("must be positive")
	}
	return none
}
func main(): int {
	let e = mustBePositive(-5)
	if e is not none {
		print(e.message)
	}
	return 0
}
`)
	if !strings.Contains(ir, "FUNC\t!mustBePositive\t^int\t:\t^error\t^bool\n") {
		t.Fatalf("expected a single error? result to expand FUNC's declared result list too; got:\n%s", ir)
	}
	if !strings.Contains(ir, "CALL\t%tmp_") || !strings.Contains(ir, "!mustBePositive") {
		t.Fatalf("expected mustBePositive to be called with both result operands; got:\n%s", ir)
	}
	assertLabelsResolve(t, ir)
}

func TestGenerate_ChanTypeEmitsCHTYPE(t *testing.T) {
	ir := generate(t, `
source numbers(output: chan<int>) {
	send(output, 1)
}
sink printAll(input: chan<int>) {
	for n in input {
		print(string(n))
	}
}
func main(): int {
	numbers |> printAll
	return 0
}
`)
	if !strings.Contains(ir, "CHTYPE\t^ChanType1\t^int\n") {
		t.Fatalf("expected chan<int> to emit a top-level CHTYPE; got:\n%s", ir)
	}
}

func TestGenerate_SendCompilesToCHSEND(t *testing.T) {
	ir := generate(t, `
source numbers(output: chan<int>) {
	send(output, 42)
}
sink printAll(input: chan<int>) {
	for n in input {
		print(string(n))
	}
}
func main(): int {
	numbers |> printAll
	return 0
}
`)
	// Inside a stage body, send() compiles to a SEL between CASESEND (the
	// real send) and a CASERECV on the hidden abort channel — a plain
	// CHSEND would block forever if downstream ever aborts and nobody's
	// left to receive (see pipeline.go's genSendCall).
	if !strings.Contains(ir, "SEL\n\tCASESEND\t$1\t42\n") {
		t.Fatalf("expected send(output, 42) inside 'numbers' to compile to a CASESEND $1 42 inside a SEL; got:\n%s", ir)
	}
}

func TestGenerate_SourceAndStageDeferCloseTheirOutput(t *testing.T) {
	ir := generate(t, `
source numbers(output: chan<int>) {
	send(output, 1)
}
stage double(input: chan<int>, output: chan<int>) {
	for n in input {
		send(output, n * 2)
	}
}
sink printAll(input: chan<int>) {
	for n in input {
		print(string(n))
	}
}
func main(): int {
	numbers |> double |> printAll
	return 0
}
`)
	// Every source/stage/sink also gets two hidden trailing parameters
	// (the abort-broadcast and close-once-guard channels, §9.4 — see
	// genStageDecl), so FUNC's own declared parameter list is longer than
	// just the user-written ones, but $1/$2 still refer to the same
	// user-declared (output, in this case) parameters as before.
	if !strings.Contains(ir, "FUNC\t!numbers\t^ChanType1\t^ChanType2\t^ChanType2\t:\n\tDEFER\t?close\t$1\n") {
		t.Fatalf("expected 'numbers' (a source) to DEFER-close its own single (output) parameter as the first body instruction; got:\n%s", ir)
	}
	if !strings.Contains(ir, "FUNC\t!double\t^ChanType1\t^ChanType1\t^ChanType2\t^ChanType2\t:\n") || !strings.Contains(ir, "DEFER\t?close\t$2\n") {
		t.Fatalf("expected 'double' (a stage) to DEFER-close its second (output) parameter; got:\n%s", ir)
	}
	if strings.Contains(ir, "FUNC\t!printAll") && strings.Contains(ir[strings.Index(ir, "FUNC\t!printAll"):], "DEFER") {
		t.Fatalf("a sink has no output channel and must not emit any DEFER ?close; got:\n%s", ir)
	}
}

func TestGenerate_ForInChannelUsesCHRECVCommaOk(t *testing.T) {
	ir := generate(t, `
source numbers(output: chan<int>) {
	send(output, 1)
}
sink printAll(input: chan<int>) {
	for n in input {
		print(string(n))
	}
}
func main(): int {
	numbers |> printAll
	return 0
}
`)
	printAllIR := ir[strings.Index(ir, "FUNC\t!printAll"):]
	// Inside a stage body, the receive is wrapped in a SEL alongside a
	// CASERECV on the hidden abort channel (see pipeline.go's
	// genForInChannelStmt) rather than a plain CHRECV.
	if !strings.Contains(printAllIR, "SEL\n\tCASERECV\t%n_1\t%tmp_2\t$1\n") {
		t.Fatalf("expected 'for n in input' to compile to a comma-ok CASERECV inside a SEL; got:\n%s", printAllIR)
	}
	if !strings.Contains(printAllIR, "NOT\t%tmp_3\t%tmp_2\n\tIF\t%tmp_3\n\tBREAK\n\tENDIF\n") {
		t.Fatalf("expected the CASERECV's ok flag (negated) to drive the loop's BREAK; got:\n%s", printAllIR)
	}
	if strings.Contains(printAllIR, "AGET") {
		t.Fatalf("a channel for-in must not use AGET (that's the list form); got:\n%s", printAllIR)
	}
	assertLabelsResolve(t, ir)
}

func TestGenerate_PipelineStmtSpawnsAllButTheSinkAndCallsSinkSynchronously(t *testing.T) {
	ir := generate(t, `
source numbers(output: chan<int>) {
	send(output, 1)
}
stage double(input: chan<int>, output: chan<int>) {
	for n in input {
		send(output, n * 2)
	}
}
sink printAll(input: chan<int>) {
	for n in input {
		print(string(n))
	}
}
func main(): int {
	numbers |> double |> printAll
	return 0
}
`)
	mainIR := ir[:strings.Index(ir, "ENDFUNC")]
	if !strings.Contains(mainIR, "CHMAKE\t%tmp_1\t^ChanType1\t0\n") || !strings.Contains(mainIR, "CHMAKE\t%tmp_2\t^ChanType1\t0\n") {
		t.Fatalf("expected one CHMAKE per inter-stage channel; got:\n%s", mainIR)
	}
	// Every SPAWN/CALL to a source/stage/sink also carries the shared
	// abort-broadcast and close-once-guard channels (§9.4) as two
	// trailing arguments beyond the user-declared input/output ones —
	// see genFreshAbortAndGuardChans/genPipelineStmt.
	if !strings.Contains(mainIR, "SPAWN\t!numbers\t%tmp_1\t%tmp_3\t%tmp_4\n") {
		t.Fatalf("expected the source to be SPAWNed against the first channel plus the abort/guard channels; got:\n%s", mainIR)
	}
	if !strings.Contains(mainIR, "SPAWN\t!double\t%tmp_1\t%tmp_2\t%tmp_3\t%tmp_4\n") {
		t.Fatalf("expected the middle stage to be SPAWNed between both channels plus the abort/guard channels; got:\n%s", mainIR)
	}
	if !strings.Contains(mainIR, "CALL\t:\t!printAll\t%tmp_2\t%tmp_3\t%tmp_4\n") {
		t.Fatalf("expected the sink to be called synchronously (not SPAWNed), blocking until the pipeline drains; got:\n%s", mainIR)
	}
	if strings.Contains(mainIR, "SPAWN\t!printAll") {
		t.Fatalf("the sink must never be SPAWNed — the statement's own synchronous completion depends on calling it directly; got:\n%s", mainIR)
	}
}

func TestGenerate_CollectSpawnsCollectorAndCHRECVsCommaOk(t *testing.T) {
	ir := generate(t, `
source numbers(output: chan<int>) {
	send(output, 1)
}
func main(): int {
	let results: []int? = numbers |> collect
	if results is none {
		return 1
	}
	return 0
}
`)
	if !strings.Contains(ir, "SPAWN\t!__collect_1\t") {
		t.Fatalf("expected collect to spawn a synthesized collector goroutine; got:\n%s", ir)
	}
	// The collector's own result channel is received from with a single
	// comma-ok CHRECV — ok=false (channel closed with nothing sent) is
	// what signals an aborted pipeline as `none` (cascade_spec.md §9.3/
	// §9.4); ok=true carries the real collected list.
	collectorIR := ir[strings.Index(ir, "FUNC\t!__collect_1"):]
	if !strings.Contains(collectorIR, "SEL\n\tCASERECV\t%v\t%ok\t$1\n") {
		t.Fatalf("expected the collector to SEL-receive its input channel with comma-ok; got:\n%s", collectorIR)
	}
	if !strings.Contains(collectorIR, "CALL\t%result\t:\t?append\t%result\t%v\n") {
		t.Fatalf("expected the collector to accumulate via raw append(); got:\n%s", collectorIR)
	}
	if !strings.Contains(collectorIR, "CHSEND\t$2\t%result\n") {
		t.Fatalf("expected the collector to send its finished list out over its result channel; got:\n%s", collectorIR)
	}
	if !strings.Contains(collectorIR, "DEFER\t?close\t$2\n") {
		t.Fatalf("expected the collector to DEFER-close its result channel (the abort-signals-none mechanism); got:\n%s", collectorIR)
	}
	assertLabelsResolve(t, ir)
}

func TestGenerate_AbortPrintsSignalsAndReturns(t *testing.T) {
	ir := generate(t, `
source numbers(output: chan<int>) {
	abort("boom")
}
func main(): int {
	let results: []int? = numbers |> collect
	if results is none {
		return 1
	}
	return 0
}
`)
	numbersStart := strings.Index(ir, "FUNC\t!numbers")
	numbersIR := ir[numbersStart : numbersStart+strings.Index(ir[numbersStart:], "ENDFUNC")]
	if !strings.Contains(numbersIR, "CONCAT\t%tmp_") || !strings.Contains(numbersIR, `"abort: "`) {
		t.Fatalf("expected abort() to prefix and print its message; got:\n%s", numbersIR)
	}
	if !strings.Contains(numbersIR, "CALL\t:\t?fmt.Println\t") {
		t.Fatalf("expected abort() to print via fmt.Println; got:\n%s", numbersIR)
	}
	// The close-once guard: a non-blocking SEL (CASESEND with DEFAULT) on
	// the guard channel, only the winner of which actually closes the
	// shared abort-broadcast channel — see genAbortCall's doc for why a
	// plain send can't safely broadcast to multiple concurrently-selecting
	// stages, only a close can.
	if !strings.Contains(numbersIR, "SEL\n\tCASESEND\t") || !strings.Contains(numbersIR, "\tDEFAULT\n") {
		t.Fatalf("expected abort() to use a non-blocking SEL+DEFAULT guard before closing; got:\n%s", numbersIR)
	}
	if !strings.Contains(numbersIR, "CALL\t:\t?close\t") {
		t.Fatalf("expected abort()'s winning path to close the abort-broadcast channel; got:\n%s", numbersIR)
	}
	if !strings.Contains(numbersIR, "\tRET\n") {
		t.Fatalf("expected abort() to unconditionally return from the enclosing stage; got:\n%s", numbersIR)
	}
	assertLabelsResolve(t, ir)
}

func TestGenerate_MergeSpawnsBothSourcesAndFanIn(t *testing.T) {
	ir := generate(t, `
source numsA(output: chan<int>) {
	send(output, 1)
}
source numsB(output: chan<int>) {
	send(output, 2)
}
func main(): int {
	let combined: chan<int> = merge(numsA, numsB)
	for v in combined {
		print(string(v))
	}
	return 0
}
`)
	mainIR := ir[:strings.Index(ir, "ENDFUNC")]
	if !strings.Contains(mainIR, "SPAWN\t!numsA\t") || !strings.Contains(mainIR, "SPAWN\t!numsB\t") {
		t.Fatalf("expected merge() to spawn both named sources; got:\n%s", mainIR)
	}
	if !strings.Contains(mainIR, "SPAWN\t!__merge_1\t") {
		t.Fatalf("expected merge() to spawn a synthesized fan-in goroutine; got:\n%s", mainIR)
	}
	mergeIR := ir[strings.Index(ir, "FUNC\t!__merge_1"):]
	// The nil-channel idiom: once an input is observed closed, its own
	// channel variable is set to nil so its SEL case can never fire
	// again, without needing to restructure the SEL itself (see
	// genMergeFunc's doc) — and both going nil is what ends the loop.
	if !strings.Contains(mergeIR, "EQ\t%doneA\t%chA\tnil\n") || !strings.Contains(mergeIR, "EQ\t%doneB\t%chB\tnil\n") {
		t.Fatalf("expected the fan-in to detect each input going nil; got:\n%s", mergeIR)
	}
	if !strings.Contains(mergeIR, "SET\t%chA\tnil\n") || !strings.Contains(mergeIR, "SET\t%chB\tnil\n") {
		t.Fatalf("expected the fan-in to nil out a closed input's own channel variable; got:\n%s", mergeIR)
	}
	if !strings.Contains(mergeIR, "SEL\n\tCASERECV\t%v\t%ok\t%chA\n") || !strings.Contains(mergeIR, "\tCASERECV\t%v\t%ok\t%chB\n") {
		t.Fatalf("expected the fan-in to SEL-receive from both inputs; got:\n%s", mergeIR)
	}
	assertLabelsResolve(t, ir)
}

func TestGenerate_TopLevelLetCompilesToGVARAndInitFunc(t *testing.T) {
	ir := generate(t, `
let counter: int = 10

func main(): int {
	print(string(counter))
	return bump()
}

func bump(): int {
	return counter
}
`)
	if !strings.Contains(ir, "GVAR\t@counter\t^int\n") {
		t.Fatalf("expected a top-level let to compile to a GVAR; got:\n%s", ir)
	}
	if !strings.Contains(ir, "FUNC\t!cascade_init\t:\n") || !strings.Contains(ir, "SET\t@counter\t10\n") {
		t.Fatalf("expected !cascade_init to hold the global's own initializer; got:\n%s", ir)
	}
	if !strings.Contains(ir, "FUNC\t!main\t:\n\tVAR\t%exitcode\t^int\n\tCALL\t:\t!cascade_init\n") {
		t.Fatalf("expected the generated !main wrapper to call !cascade_init before !cascade_main; got:\n%s", ir)
	}
	bumpIR := ir[strings.Index(ir, "FUNC\t!bump"):]
	if !strings.Contains(bumpIR, "RET\t@counter\n") {
		t.Fatalf("expected bump() to read the global directly via @counter, without redeclaring it; got:\n%s", bumpIR)
	}
}

func TestGenerate_NullableTopLevelLetGetsIssetGVAR(t *testing.T) {
	ir := generate(t, `
let shared: int? = none

func main(): int {
	if shared is not none {
		print(string(shared))
	}
	return 0
}
`)
	if !strings.Contains(ir, "GVAR\t@shared\t^int\n") || !strings.Contains(ir, "GVAR\t@shared_isset\t^bool\n") {
		t.Fatalf("expected a nullable top-level let to get a companion isset GVAR; got:\n%s", ir)
	}
	if !strings.Contains(ir, "SET\t@shared\t0\n\tSET\t@shared_isset\tfalse\n") {
		t.Fatalf("expected the 'none' initializer to reset the value and isset flag; got:\n%s", ir)
	}
}

func TestGenerate_NoTopLevelLetsEmitsNoInitFunc(t *testing.T) {
	ir := generate(t, `
func main(): int {
	return 0
}
`)
	if strings.Contains(ir, "cascade_init") {
		t.Fatalf("expected no !cascade_init at all when there are no top-level lets; got:\n%s", ir)
	}
}
