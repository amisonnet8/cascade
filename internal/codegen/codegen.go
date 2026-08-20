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
// position. AMIVM-IR's control flow is GOTO-based, not nested Go blocks
// (see genIfStmt/genWhileStmt/genSwitchStmt), so without this a forward
// jump — an `elif` chain skipping a later clause's body, a `break`
// jumping past the rest of a loop — trips Go's "goto jumps over variable
// declaration" rule the moment the skipped code declares anything
// (seed_implementation_notes.md §1). genBlock gives every if/while/
// switch-case body its own funcGen scope, even though every declaration
// still ends up as one flat hoisted VAR in the enclosing function — that
// scope is purely for name resolution (shadowing, cascade_spec.md §10),
// not for Go block structure, which doesn't exist here. See scope.go's
// varRef for how a nullable `T?` variable's companion "is this set?"
// flag is represented, and funcGen's breakStack/continueStack for why
// `break` (loop or switch) and `continue` (loop only, skipping over any
// enclosing switch) need separate target stacks.
//
// Only enough is implemented so far to compile Steps 1-7: `main` plus any
// number of other functions (§8.1, including multi-value returns via
// §8.5 — see func.go), whose bodies are `let`/`const`/multi-value `let`
// declarations, scalar/list-element/compound assignment, `print(...)`
// and other function calls, `return`, the full operator set (§6),
// control flow (if/elif/else, while, for-in, switch, break/continue),
// and lists (`[]T` — literals, indexing, append/range/len; see list.go).
// Later steps extend genStmt/genValue one feature at a time, the same
// way parser's grammar grows.
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
// text: a top-level SLTYPE for every list element type any function
// uses, then every function's own FUNC block (main's under
// mainInternalName — see func.go's genFuncDecl), then the generated
// `!main` wrapper that bridges to it.
func Generate(f *ast.File) (string, error) {
	var main *ast.FuncDecl
	sigs := map[string]funcSig{}
	for _, fn := range f.Funcs {
		params := make([]ast.Type, len(fn.Params))
		for i, p := range fn.Params {
			params[i] = p.Type
		}
		sigs[fn.Name] = funcSig{Params: params, Results: fn.Results}
		if fn.Name == "main" {
			main = fn
		}
	}
	if main == nil {
		return "", fmt.Errorf("codegen: no main function (run sema.Check first)")
	}

	slices := &sliceRegistry{used: map[string]bool{}}
	var funcsIR strings.Builder
	for _, fn := range f.Funcs {
		ir, err := genFuncDecl(fn, slices, sigs)
		if err != nil {
			return "", err
		}
		funcsIR.WriteString(ir)
	}

	var b strings.Builder
	for _, elemName := range slices.sorted() {
		elemIRType, err := scalarTypeToIR(ast.Type{Name: elemName})
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "SLTYPE\t%s\t%s\n", listTypeToken(elemName), elemIRType)
	}
	b.WriteString(funcsIR.String())

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
	decls         []varDecl
	b             strings.Builder
	scope         *scope
	slices        *sliceRegistry
	sigs          map[string]funcSig
	seq           int
	labelSeq      int
	breakStack    []string // break targets: pushed by both while and switch
	continueStack []string // continue targets: pushed by while only (switch isn't a loop)
}

func (g *funcGen) emit(format string, args ...any) {
	fmt.Fprintf(&g.b, format, args...)
}

// declareVar records a hoisted VAR for op.
func (g *funcGen) declareVar(op, irType string) {
	g.decls = append(g.decls, varDecl{Op: op, IRType: irType})
}

// newLabel mints a fresh label name, unique within this function. Go
// scopes labels per-function, so a plain counter needs no further
// qualification (unlike variable names — see freshName).
func (g *funcGen) newLabel() string {
	g.labelSeq++
	return fmt.Sprintf("L%d", g.labelSeq)
}

func (g *funcGen) pushBreak(label string)    { g.breakStack = append(g.breakStack, label) }
func (g *funcGen) popBreak()                 { g.breakStack = g.breakStack[:len(g.breakStack)-1] }
func (g *funcGen) pushContinue(label string) { g.continueStack = append(g.continueStack, label) }
func (g *funcGen) popContinue()              { g.continueStack = g.continueStack[:len(g.continueStack)-1] }

func (g *funcGen) currentBreak() (string, bool) {
	if len(g.breakStack) == 0 {
		return "", false
	}
	return g.breakStack[len(g.breakStack)-1], true
}

func (g *funcGen) currentContinue() (string, bool) {
	if len(g.continueStack) == 0 {
		return "", false
	}
	return g.continueStack[len(g.continueStack)-1], true
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

// genBlock compiles one `{ }` block's statements in a fresh child scope,
// so its declarations don't leak into the enclosing block and may shadow
// an outer variable (cascade_spec.md §10) — even though every declaration
// still ends up as one flat hoisted VAR in the enclosing function (see
// package doc). Used uniformly for every if/while/switch-case body.
func genBlock(g *funcGen, stmts []ast.Stmt) error {
	outer := g.scope
	g.scope = newScope(outer)
	defer func() { g.scope = outer }()
	for _, stmt := range stmts {
		if err := genStmt(g, stmt); err != nil {
			return err
		}
	}
	return nil
}

func genStmt(g *funcGen, stmt ast.Stmt) error {
	switch s := stmt.(type) {
	case *ast.LetDecl:
		return genLetDecl(g, s)
	case *ast.MultiLetDecl:
		return genMultiLetDecl(g, s)
	case *ast.AssignStmt:
		return genAssignStmt(g, s)
	case *ast.CompoundAssignStmt:
		return genCompoundAssignStmt(g, s)
	case *ast.IncDecStmt:
		return genIncDecStmt(g, s)
	case *ast.ExprStmt:
		return genExprStmt(g, s)
	case *ast.ReturnStmt:
		return genReturnStmt(g, s)
	case *ast.IfStmt:
		return genIfStmt(g, s)
	case *ast.WhileStmt:
		return genWhileStmt(g, s)
	case *ast.SwitchStmt:
		return genSwitchStmt(g, s)
	case *ast.ForInStmt:
		return genForInStmt(g, s)
	case *ast.BreakStmt:
		return genBreakStmt(g, s)
	case *ast.ContinueStmt:
		return genContinueStmt(g, s)
	default:
		return fmt.Errorf("codegen: unsupported statement %T", stmt)
	}
}

// genIfStmt compiles an if/elif/else chain to a sequence of conditional
// jumps followed by the bodies themselves (mirrors Seed's identical
// genIfStmt — see seed/internal/codegen/stmt.go). Each clause's condition
// is evaluated immediately before its own IF, so a taken jump skips every
// later condition's instructions entirely — giving the usual short-circuit
// "elif conditions run only if earlier ones were false" behavior for free.
//
//	<cond1 instrs>; IF cond1 body1
//	<cond2 instrs>; IF cond2 body2
//	...
//	GOTO else-or-end
//	LABEL body1; ...; GOTO end
//	LABEL body2; ...; GOTO end
//	LABEL else; ...; GOTO end   (only if there's an `else`)
//	LABEL end
func genIfStmt(g *funcGen, stmt *ast.IfStmt) error {
	endLabel := g.newLabel()
	bodyLabels := make([]string, len(stmt.Clauses))

	for i, clause := range stmt.Clauses {
		cond, err := genValue(g, clause.Cond)
		if err != nil {
			return err
		}
		bodyLabels[i] = g.newLabel()
		g.emit("\tIF\t%s\t#%s\n", cond, bodyLabels[i])
	}

	var elseLabel string
	if stmt.Else != nil {
		elseLabel = g.newLabel()
		g.emit("\tGOTO\t#%s\n", elseLabel)
	} else {
		g.emit("\tGOTO\t#%s\n", endLabel)
	}

	for i, clause := range stmt.Clauses {
		g.emit("\tLABEL\t#%s\n", bodyLabels[i])
		if err := genBlock(g, clause.Body); err != nil {
			return err
		}
		g.emit("\tGOTO\t#%s\n", endLabel)
	}

	if stmt.Else != nil {
		g.emit("\tLABEL\t#%s\n", elseLabel)
		if err := genBlock(g, stmt.Else); err != nil {
			return err
		}
		g.emit("\tGOTO\t#%s\n", endLabel)
	}

	g.emit("\tLABEL\t#%s\n", endLabel)
	return nil
}

// genWhileStmt compiles a while loop as: check the condition, jump into
// the body if true or out past the loop if false, and jump back to the
// check after the body runs (mirrors Seed's identical genWhileStmt).
//
//	LABEL start; <cond instrs>; IF cond body; GOTO end
//	LABEL body; ...; GOTO start
//	LABEL end
//
// `continue` targets start (re-check the condition) and `break` targets
// end.
func genWhileStmt(g *funcGen, stmt *ast.WhileStmt) error {
	startLabel := g.newLabel()
	bodyLabel := g.newLabel()
	endLabel := g.newLabel()

	g.emit("\tLABEL\t#%s\n", startLabel)
	cond, err := genValue(g, stmt.Cond)
	if err != nil {
		return err
	}
	g.emit("\tIF\t%s\t#%s\n", cond, bodyLabel)
	g.emit("\tGOTO\t#%s\n", endLabel)
	g.emit("\tLABEL\t#%s\n", bodyLabel)

	g.pushBreak(endLabel)
	g.pushContinue(startLabel)
	err = genBlock(g, stmt.Body)
	g.popContinue()
	g.popBreak()
	if err != nil {
		return err
	}

	g.emit("\tGOTO\t#%s\n", startLabel)
	g.emit("\tLABEL\t#%s\n", endLabel)
	return nil
}

// genSwitchStmt compiles a tagged or untagged switch (cascade_spec.md
// §7) as a sequence of conditional jumps, one IF per case value, followed
// by the case bodies themselves — the same "evaluate condition, jump to
// body, fall through to the next condition" shape as genIfStmt. The tag
// (if any) is evaluated exactly once, up front, and its resulting operand
// reused for every case's EQ comparison. break targets end; switch is not
// a loop, so it never touches the continue stack (see funcGen's doc).
func genSwitchStmt(g *funcGen, stmt *ast.SwitchStmt) error {
	var tagOp string
	if stmt.Tag != nil {
		v, err := genValue(g, stmt.Tag)
		if err != nil {
			return err
		}
		tagOp = v
	}

	endLabel := g.newLabel()
	caseLabels := make([]string, len(stmt.Cases))

	for i, c := range stmt.Cases {
		caseLabels[i] = g.newLabel()
		for _, val := range c.Values {
			v, err := genValue(g, val)
			if err != nil {
				return err
			}
			if stmt.Tag != nil {
				cmp := g.newTemp("^bool")
				g.emit("\tEQ\t%s\t%s\t%s\n", cmp, tagOp, v)
				g.emit("\tIF\t%s\t#%s\n", cmp, caseLabels[i])
			} else {
				g.emit("\tIF\t%s\t#%s\n", v, caseLabels[i])
			}
		}
	}

	var defaultLabel string
	if stmt.Default != nil {
		defaultLabel = g.newLabel()
		g.emit("\tGOTO\t#%s\n", defaultLabel)
	} else {
		g.emit("\tGOTO\t#%s\n", endLabel)
	}

	g.pushBreak(endLabel)
	for i, c := range stmt.Cases {
		g.emit("\tLABEL\t#%s\n", caseLabels[i])
		if err := genBlock(g, c.Body); err != nil {
			g.popBreak()
			return err
		}
		g.emit("\tGOTO\t#%s\n", endLabel)
	}
	if stmt.Default != nil {
		g.emit("\tLABEL\t#%s\n", defaultLabel)
		if err := genBlock(g, stmt.Default); err != nil {
			g.popBreak()
			return err
		}
		g.emit("\tGOTO\t#%s\n", endLabel)
	}
	g.popBreak()

	g.emit("\tLABEL\t#%s\n", endLabel)
	return nil
}

func genBreakStmt(g *funcGen, stmt *ast.BreakStmt) error {
	label, ok := g.currentBreak()
	if !ok {
		return fmt.Errorf("codegen: break outside of a loop or switch (sema bug)")
	}
	g.emit("\tGOTO\t#%s\n", label)
	return nil
}

func genContinueStmt(g *funcGen, stmt *ast.ContinueStmt) error {
	label, ok := g.currentContinue()
	if !ok {
		return fmt.Errorf("codegen: continue outside of a loop (sema bug)")
	}
	g.emit("\tGOTO\t#%s\n", label)
	return nil
}

// genCompoundAssignStmt emits `name op= value` in place (AMIVM-IR's
// arithmetic/bitwise instructions allow the same variable as both
// destination and source operand, e.g. `ADD x x y` -> Go's `x = x + y`;
// mirrors Seed's identical genCompoundAssignStmt).
func genCompoundAssignStmt(g *funcGen, stmt *ast.CompoundAssignStmt) error {
	ref, ok := g.scope.lookup(stmt.Name)
	if !ok {
		return fmt.Errorf("codegen: undefined name %q (sema bug)", stmt.Name)
	}
	v, err := genValue(g, stmt.Value)
	if err != nil {
		return err
	}
	switch {
	case stmt.Op == "+" && ref.Type.Name == "string":
		g.emit("\tCONCAT\t%s\t%s\t%s\n", ref.ValOp, ref.ValOp, v)
	case stmt.Op == "+":
		g.emit("\tADD\t%s\t%s\t%s\n", ref.ValOp, ref.ValOp, v)
	default:
		instr, ok := binaryOpInstr[stmt.Op]
		if !ok {
			return fmt.Errorf("codegen: unsupported compound assignment operator %q", stmt.Op)
		}
		g.emit("\t%s\t%s\t%s\t%s\n", instr, ref.ValOp, ref.ValOp, v)
	}
	if ref.SetOp != "" {
		g.emit("\tSET\t%s\ttrue\n", ref.SetOp)
	}
	return nil
}

func genIncDecStmt(g *funcGen, stmt *ast.IncDecStmt) error {
	ref, ok := g.scope.lookup(stmt.Name)
	if !ok {
		return fmt.Errorf("codegen: undefined name %q (sema bug)", stmt.Name)
	}
	instr := "ADD"
	if stmt.Op == "--" {
		instr = "SUB"
	}
	g.emit("\t%s\t%s\t%s\t1\n", instr, ref.ValOp, ref.ValOp)
	if ref.SetOp != "" {
		g.emit("\tSET\t%s\ttrue\n", ref.SetOp)
	}
	return nil
}

// genLetDecl compiles a `let`/`const` declaration (cascade_spec.md §4.2).
// decl.ResolvedType — filled in by sema.Check — is used directly rather
// than re-inferring it from decl.Init.
func genLetDecl(g *funcGen, decl *ast.LetDecl) error {
	typ := decl.ResolvedType
	irType, err := typeToIR(g.slices, typ)
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

// genAssignStmt compiles `name = value` or, when Index is set,
// `name[Index] = value` (cascade_spec.md §5), an ASET into the existing
// list — unlike a whole-list reassignment (genInit), this never
// reallocates, matching Go's own in-place slice-element-assignment
// semantics.
func genAssignStmt(g *funcGen, stmt *ast.AssignStmt) error {
	ref, ok := g.scope.lookup(stmt.Name)
	if !ok {
		return fmt.Errorf("codegen: undefined name %q (sema bug)", stmt.Name)
	}
	if stmt.Index != nil {
		idxOp, err := genValue(g, stmt.Index)
		if err != nil {
			return err
		}
		v, err := genValue(g, stmt.Value)
		if err != nil {
			return err
		}
		g.emit("\tASET\t%s\t%s\t%s\n", ref.ValOp, idxOp, v)
		return nil
	}
	return genInit(g, ref, stmt.Value)
}

// genInit emits the SET(s) (or, for a list literal, SLMAKE+ASET — see
// genListLiteralInit) that give ref its value: the zero value (and, if
// nullable, a false flag) when init is nil or the `none` literal, or
// init's own value otherwise. When ref is nullable, the isset flag comes
// from genNullableOperands rather than being hardcoded to "true" — init
// may itself be another nullable variable that currently holds none, and
// the target must end up none too (see genNullableOperands's doc for why
// this needs its own helper rather than a plain genValue call).
func genInit(g *funcGen, ref varRef, init ast.Expr) error {
	if init == nil {
		return genResetToZero(g, ref)
	}
	if _, isNone := init.(*ast.NoneLit); isNone {
		return genResetToZero(g, ref)
	}
	if lit, isList := init.(*ast.ListLit); isList {
		return genListLiteralInit(g, ref, lit)
	}
	if ref.SetOp != "" {
		val, isset, err := genNullableOperands(g, init, ref.Type)
		if err != nil {
			return err
		}
		g.emit("\tSET\t%s\t%s\n", ref.ValOp, val)
		g.emit("\tSET\t%s\t%s\n", ref.SetOp, isset)
		return nil
	}
	v, err := genValue(g, init)
	if err != nil {
		return err
	}
	g.emit("\tSET\t%s\t%s\n", ref.ValOp, v)
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

// genExprStmt compiles a call-expression statement: the `print` builtin
// (cascade_spec.md §13), or a call to any other function (§8.1) with its
// result(s) — zero, one, or many — simply discarded (AMIVM-IR's CALL
// omits its result operand(s) entirely for this, not an empty token —
// see genUserFuncCallStmt).
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
		sig, ok := g.sigs[call.Callee]
		if !ok {
			return fmt.Errorf("codegen: unsupported call to %q", call.Callee)
		}
		return genUserFuncCallStmt(g, call, sig)
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
	case *ast.NullCheckExpr:
		return genNullCheck(g, v)
	case *ast.IndexExpr:
		return genIndexRead(g, v)
	default:
		return "", fmt.Errorf("codegen: unsupported value expression %T", e)
	}
}

// genNullCheck compiles `X is none`/`X is not none` (cascade_spec.md §7)
// by reading X's own isset flag directly — sema.Check guarantees X is a
// plain identifier with a nullable type (see ast.NullCheckExpr's doc), so
// its varRef always has a SetOp (scope.go's varRef). "is not none" is the
// flag itself; "is none" is its negation. No new AMIVM instruction is
// needed, and narrowing (§2.3) itself is a sema-only concept — see
// CLAUDE.md's "確定した設計判断" — so there is nothing further to do here.
func genNullCheck(g *funcGen, e *ast.NullCheckExpr) (string, error) {
	id, ok := e.X.(*ast.Ident)
	if !ok {
		return "", fmt.Errorf("codegen: null-check target must be an identifier (sema bug)")
	}
	ref, ok := g.scope.lookup(id.Name)
	if !ok {
		return "", fmt.Errorf("codegen: undefined name %q (sema bug)", id.Name)
	}
	if ref.SetOp == "" {
		return "", fmt.Errorf("codegen: null-check on non-nullable %q (sema bug)", id.Name)
	}
	if e.Not {
		return ref.SetOp, nil
	}
	tmp := g.newTemp("^bool")
	g.emit("\tNOT\t%s\t%s\n", tmp, ref.SetOp)
	return tmp, nil
}

// genCallValue compiles a call expression used as a value: the
// `string()` conversion, the list builtins `len()`/`range()`/`append()`
// (cascade_spec.md §13, see list.go), or a call to any other
// single-value-returning function (§8.1, see func.go) — see genExprStmt
// for calls used as a bare statement (print) and genMultiLetDecl for a
// multi-value function call (§8.5).
func genCallValue(g *funcGen, call *ast.CallExpr) (string, error) {
	switch call.Callee {
	case "string":
		return genStringConversion(g, call)
	case "len":
		return genLenCall(g, call)
	case "range":
		return genRangeCall(g, call)
	case "append":
		return genAppendCall(g, call)
	default:
		sig, ok := g.sigs[call.Callee]
		if !ok {
			return "", fmt.Errorf("codegen: unsupported call to %q as a value", call.Callee)
		}
		return genUserFuncCallValue(g, call, sig)
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

// genUnary compiles a unary "!"/"-"/"~" expression (cascade_spec.md §6)
// into a fresh temp of ResultType (filled in by sema.Check). AMIVM-IR has
// no unary-minus instruction, so "-x" is emitted as "SUB tmp 0 x"
// (seed_implementation_notes.md §5.6's exact pattern for the same gap);
// "~x" (bitwise NOT) has its own instruction, BNOT, so it needs no such
// workaround.
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
	case "~":
		g.emit("\tBNOT\t%s\t%s\n", tmp, x)
	default:
		return "", fmt.Errorf("codegen: unsupported unary operator %q", e.Op)
	}
	return tmp, nil
}

// binaryOpInstr maps every ast.BinaryExpr.Op except "+" (see genBinary) to
// its AMIVM-IR instruction (cascade_spec.md §6, amivm_spec.md §4.3-4.7).
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
	"&":  "BAND",
	"|":  "BOR",
	"^":  "BXOR",
	"&^": "BCLEAR",
	"<<": "SHL",
	">>": "SHR",
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
// (cascade_spec.md §2.1, §2.2), which happens to coincide with Go's zero
// value in every case: a list's base value is the empty list `[]`
// (§2.2), and Go's nil slice already behaves as an empty one for every
// operation Cascade exposes (len, range-over, append) — `nil` is also
// what a nullable-and-unset variable of any type resets to, whether
// scalar or list, so this one token serves both cases identically.
func zeroValueLiteral(t ast.Type) (string, error) {
	if t.Elem != nil {
		return "nil", nil
	}
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
