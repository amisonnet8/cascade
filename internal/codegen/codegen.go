// Package codegen translates a sema-checked Cascade ast.File into
// AMIVM-IR.
//
// Cascade's `main` cannot be emitted as amivm's `!main` directly: Go
// requires literal `func main()` to take no arguments and return nothing,
// but Cascade's entry point has signature `func main(): int`
// (cascade_spec.md §12). So the user's main is emitted as an ordinary
// function (mainInternalName), and a small generated `!main` wrapper
// bridges the two: it calls the renamed function and turns its returned
// int into a process exit code via os.Exit. This mirrors Seed's identical
// `main`/`seed_main` split (see CLAUDE.md's "確定した設計判断").
//
// Every declared variable's `VAR` is hoisted to the top of its function,
// with only the `SET` that actually assigns a value left at its original
// position — even though Steps 1-2 have no branches yet to trip Go's
// "goto jumps over variable declaration" rule, this is the exact shape
// Step 5 (control flow) requires (seed_implementation_notes.md §1), and
// getting funcGen's shape right once now avoids reworking every
// declaration site later. See scope.go's varRef for how a nullable `T?`
// variable's companion "is this set?" flag is represented.
//
// Only enough is implemented so far to compile Steps 1-3: a single `main`
// function whose body is `let`/`const` declarations, scalar assignment, a
// `print(...)` call, `return`, and arithmetic/comparison/logical operator
// expressions (cascade_spec.md §6). Later steps extend genStmt/genValue
// one feature at a time, the same way parser's grammar grows.
package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/amisonnet8/cascade/internal/ast"
)

// mainInternalName is the amivm-level name the user's `main` is emitted
// under. Must match sema's mainInternalName constant.
const mainInternalName = "cascade_main"

// Generate translates f (already validated by sema.Check) into AMIVM-IR
// text.
func Generate(f *ast.File) (string, error) {
	var main *ast.FuncDecl
	for _, fn := range f.Funcs {
		if fn.Name == "main" {
			main = fn
		}
	}
	if main == nil {
		return "", fmt.Errorf("codegen: no main function (run sema.Check first)")
	}

	g := &funcGen{scope: newScope(nil)}
	for _, stmt := range main.Body {
		if err := genStmt(g, stmt); err != nil {
			return "", err
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "FUNC\t!%s\t:\t^int\n", mainInternalName)
	for _, d := range g.decls {
		fmt.Fprintf(&b, "\tVAR\t%s\t%s\n", d.Op, d.IRType)
	}
	b.WriteString(g.b.String())
	b.WriteString("ENDFUNC\n")

	b.WriteString("FUNC\t!main\t:\n")
	b.WriteString("\tVAR\t%exitcode\t^int\n")
	fmt.Fprintf(&b, "\tCALL\t%%exitcode\t:\t!%s\n", mainInternalName)
	b.WriteString("\tCALL\t:\t?os.Exit\t%exitcode\n")
	b.WriteString("\tRET\n")
	b.WriteString("ENDFUNC\n")

	return b.String(), nil
}

// varDecl is one hoisted VAR line, emitted at the top of the function
// (see package doc).
type varDecl struct {
	Op     string
	IRType string
}

// funcGen accumulates the AMIVM-IR body of a single function being
// compiled, alongside the scope/name-mangling state needed to resolve
// Cascade variable references. decls and b are kept separate — and only
// combined by Generate once generation finishes — so that every VAR ends
// up hoisted before any control flow (see package doc).
type funcGen struct {
	decls []varDecl
	b     strings.Builder
	scope *scope
	seq   int
}

func (g *funcGen) emit(format string, args ...any) {
	fmt.Fprintf(&g.b, format, args...)
}

// declareVar records a hoisted VAR for op.
func (g *funcGen) declareVar(op, irType string) {
	g.decls = append(g.decls, varDecl{Op: op, IRType: irType})
}

// newTemp declares a fresh hoisted variable of the given AMIVM-IR type to
// hold an intermediate result (e.g. one operator's worth of a larger
// expression) and returns its operand.
func (g *funcGen) newTemp(irType string) string {
	op := "%" + g.freshName("tmp")
	g.declareVar(op, irType)
	return op
}

// freshName mints an internal variable name for a `let`/`const` declared
// as base, unique within this function. A numeric suffix is added
// unconditionally (not just on collision) so that once Step 5 adds nested
// blocks, a shadowing inner declaration of the same user-level name never
// collides with the outer one in the flat Go namespace every hoisted VAR
// shares (seed_implementation_notes.md §2).
func (g *funcGen) freshName(base string) string {
	g.seq++
	return fmt.Sprintf("%s_%d", base, g.seq)
}

func genStmt(g *funcGen, stmt ast.Stmt) error {
	switch s := stmt.(type) {
	case *ast.LetDecl:
		return genLetDecl(g, s)
	case *ast.AssignStmt:
		return genAssignStmt(g, s)
	case *ast.ExprStmt:
		return genExprStmt(g, s)
	case *ast.ReturnStmt:
		return genReturnStmt(g, s)
	default:
		return fmt.Errorf("codegen: unsupported statement %T", stmt)
	}
}

// genLetDecl compiles a `let`/`const` declaration (cascade_spec.md §4.2).
// decl.ResolvedType — filled in by sema.Check — is used directly rather
// than re-inferring it from decl.Init.
func genLetDecl(g *funcGen, decl *ast.LetDecl) error {
	typ := decl.ResolvedType
	irType, err := scalarTypeToIR(typ)
	if err != nil {
		return err
	}

	name := g.freshName(decl.Name)
	ref := varRef{Type: typ, ValOp: "%" + name}
	g.declareVar(ref.ValOp, irType)
	if typ.Nullable {
		ref.SetOp = "%" + name + "_isset"
		g.declareVar(ref.SetOp, "^bool")
	}
	g.scope.declare(decl.Name, ref)

	return genInit(g, ref, decl.Init)
}

func genAssignStmt(g *funcGen, stmt *ast.AssignStmt) error {
	ref, ok := g.scope.lookup(stmt.Name)
	if !ok {
		return fmt.Errorf("codegen: undefined name %q (sema bug)", stmt.Name)
	}
	return genInit(g, ref, stmt.Value)
}

// genInit emits the SET(s) that give ref its value: the zero value (and,
// if nullable, a false flag) when init is nil or the `none` literal, or
// init's own value (and a true flag) otherwise.
func genInit(g *funcGen, ref varRef, init ast.Expr) error {
	if init == nil {
		return genResetToZero(g, ref)
	}
	if _, isNone := init.(*ast.NoneLit); isNone {
		return genResetToZero(g, ref)
	}
	v, err := genValue(g, init)
	if err != nil {
		return err
	}
	g.emit("\tSET\t%s\t%s\n", ref.ValOp, v)
	if ref.SetOp != "" {
		g.emit("\tSET\t%s\ttrue\n", ref.SetOp)
	}
	return nil
}

func genResetToZero(g *funcGen, ref varRef) error {
	zero, err := zeroValueLiteral(ref.Type)
	if err != nil {
		return err
	}
	g.emit("\tSET\t%s\t%s\n", ref.ValOp, zero)
	if ref.SetOp != "" {
		g.emit("\tSET\t%s\tfalse\n", ref.SetOp)
	}
	return nil
}

// genExprStmt compiles a call-expression statement. Only the `print`
// builtin (cascade_spec.md §13) is wired up so far; user-defined function
// calls and the remaining builtins land in later steps.
func genExprStmt(g *funcGen, stmt *ast.ExprStmt) error {
	call, ok := stmt.X.(*ast.CallExpr)
	if !ok {
		return fmt.Errorf("codegen: unsupported expression statement %T", stmt.X)
	}
	switch call.Callee {
	case "print":
		if len(call.Args) != 1 {
			return fmt.Errorf("codegen: print expects exactly 1 argument, got %d", len(call.Args))
		}
		v, err := genValue(g, call.Args[0])
		if err != nil {
			return err
		}
		g.emit("\tCALL\t:\t?fmt.Println\t%s\n", v)
		return nil
	default:
		return fmt.Errorf("codegen: unsupported call to %q", call.Callee)
	}
}

// genReturnStmt compiles a `return` statement, joining every result into
// one RET instruction (AMIVM-IR's RET natively takes multiple operands, so
// cascade_spec.md §8.5's multi-value return needs no special-casing here).
func genReturnStmt(g *funcGen, stmt *ast.ReturnStmt) error {
	values := make([]string, len(stmt.Results))
	for i, r := range stmt.Results {
		v, err := genValue(g, r)
		if err != nil {
			return err
		}
		values[i] = v
	}
	if len(values) == 0 {
		g.emit("\tRET\n")
		return nil
	}
	g.emit("\tRET\t%s\n", strings.Join(values, "\t"))
	return nil
}

// genValue compiles e into an AMIVM-IR value token: a literal token
// inline, or a declared variable's ValOp for a plain read (correct with no
// SetOp check at all — see scope.go's varRef doc). `none` has no value
// token of its own; callers that accept it (genInit) special-case it
// before reaching here.
func genValue(g *funcGen, e ast.Expr) (string, error) {
	switch v := e.(type) {
	case *ast.StringLit:
		return strconv.Quote(v.Value), nil
	case *ast.IntLit:
		return strconv.FormatInt(v.Value, 10), nil
	case *ast.FloatLit:
		return strconv.FormatFloat(v.Value, 'g', -1, 64), nil
	case *ast.BoolLit:
		return strconv.FormatBool(v.Value), nil
	case *ast.Ident:
		ref, ok := g.scope.lookup(v.Name)
		if !ok {
			return "", fmt.Errorf("codegen: undefined name %q (sema bug)", v.Name)
		}
		return ref.ValOp, nil
	case *ast.UnaryExpr:
		return genUnary(g, v)
	case *ast.BinaryExpr:
		return genBinary(g, v)
	case *ast.CallExpr:
		return genCallValue(g, v)
	default:
		return "", fmt.Errorf("codegen: unsupported value expression %T", e)
	}
}

// genCallValue compiles a call expression used as a value. Only the
// `string()` builtin conversion (cascade_spec.md §13) is wired up so far
// — see genExprStmt for calls used as a bare statement (print).
func genCallValue(g *funcGen, call *ast.CallExpr) (string, error) {
	switch call.Callee {
	case "string":
		return genStringConversion(g, call)
	default:
		return "", fmt.Errorf("codegen: unsupported call to %q as a value", call.Callee)
	}
}

// genStringConversion compiles string(x) using call.ArgType (filled in by
// sema.Check) to pick the right strconv function. Go's own string(v)
// conversion is a rune conversion for numeric types — string(65) is "A",
// not "65" — so strconv is required here, not a plain CALL-as-cast
// (seed_implementation_notes.md §3).
func genStringConversion(g *funcGen, call *ast.CallExpr) (string, error) {
	x, err := genValue(g, call.Args[0])
	if err != nil {
		return "", err
	}
	tmp := g.newTemp("^string")
	switch call.ArgType.Name {
	case "int":
		g.emit("\tCALL\t%s\t:\t?strconv.Itoa\t%s\n", tmp, x)
	case "float":
		g.emit("\tCALL\t%s\t:\t?strconv.FormatFloat\t%s\t'g'\t-1\t64\n", tmp, x)
	case "bool":
		g.emit("\tCALL\t%s\t:\t?strconv.FormatBool\t%s\n", tmp, x)
	default:
		return "", fmt.Errorf("codegen: string() does not support %s (sema bug)", call.ArgType.Name)
	}
	return tmp, nil
}

// genUnary compiles a unary "!"/"-" expression (cascade_spec.md §6) into a
// fresh temp of ResultType (filled in by sema.Check). AMIVM-IR has no
// unary-minus instruction, so "-x" is emitted as "SUB tmp 0 x"
// (seed_implementation_notes.md §5.6's exact pattern for the same gap).
func genUnary(g *funcGen, e *ast.UnaryExpr) (string, error) {
	x, err := genValue(g, e.X)
	if err != nil {
		return "", err
	}
	irType, err := scalarTypeToIR(e.ResultType)
	if err != nil {
		return "", err
	}
	tmp := g.newTemp(irType)
	switch e.Op {
	case "!":
		g.emit("\tNOT\t%s\t%s\n", tmp, x)
	case "-":
		g.emit("\tSUB\t%s\t0\t%s\n", tmp, x)
	default:
		return "", fmt.Errorf("codegen: unsupported unary operator %q", e.Op)
	}
	return tmp, nil
}

// binaryOpInstr maps every ast.BinaryExpr.Op except "+" (see genBinary) to
// its AMIVM-IR instruction (cascade_spec.md §6, amivm_spec.md §4.3/4.6/4.7).
var binaryOpInstr = map[string]string{
	"-":  "SUB",
	"*":  "MUL",
	"/":  "DIV",
	"%":  "MOD",
	"==": "EQ",
	"!=": "NEQ",
	"<":  "LT",
	"<=": "LTE",
	">":  "GT",
	">=": "GTE",
	"&&": "AND",
	"||": "OR",
}

// genBinary compiles a binary expression (cascade_spec.md §6) into a fresh
// temp of ResultType (filled in by sema.Check). "+" is special-cased: its
// AMIVM-IR instruction depends on the (shared, sema-checked) operand type
// — ADD for int/float, CONCAT for string — the same dispatch Seed's
// codegen uses (seed/CLAUDE.md "確定した設計判断").
func genBinary(g *funcGen, e *ast.BinaryExpr) (string, error) {
	x, err := genValue(g, e.X)
	if err != nil {
		return "", err
	}
	y, err := genValue(g, e.Y)
	if err != nil {
		return "", err
	}
	irType, err := scalarTypeToIR(e.ResultType)
	if err != nil {
		return "", err
	}
	tmp := g.newTemp(irType)

	if e.Op == "+" {
		if e.ResultType.Name == "string" {
			g.emit("\tCONCAT\t%s\t%s\t%s\n", tmp, x, y)
		} else {
			g.emit("\tADD\t%s\t%s\t%s\n", tmp, x, y)
		}
		return tmp, nil
	}
	instr, ok := binaryOpInstr[e.Op]
	if !ok {
		return "", fmt.Errorf("codegen: unsupported binary operator %q", e.Op)
	}
	g.emit("\t%s\t%s\t%s\t%s\n", instr, tmp, x, y)
	return tmp, nil
}

// scalarTypeToIR maps a Cascade scalar type to its AMIVM-IR type token.
// The nullable flag plays no part in the token itself — a `T?`'s value
// variable has exactly T's IR type; its extra "is this set?" flag is a
// second, separate ^bool variable (see scope.go's varRef).
func scalarTypeToIR(t ast.Type) (string, error) {
	switch t.Name {
	case "int":
		return "^int", nil
	case "float":
		return "^float64", nil
	case "string":
		return "^string", nil
	case "bool":
		return "^bool", nil
	default:
		return "", fmt.Errorf("codegen: unknown type %q", t.Name)
	}
}

// zeroValueLiteral is the AMIVM-IR value token for t's Cascade base value
// (cascade_spec.md §2.1), which happens to coincide with Go's zero value
// in every case.
func zeroValueLiteral(t ast.Type) (string, error) {
	switch t.Name {
	case "int":
		return "0", nil
	case "float":
		return "0", nil
	case "string":
		return strconv.Quote(""), nil
	case "bool":
		return "false", nil
	default:
		return "", fmt.Errorf("codegen: unknown type %q", t.Name)
	}
}
