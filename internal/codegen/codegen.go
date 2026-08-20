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
// Only enough is implemented so far to compile Steps 1-8: `main` plus any
// number of other functions (§8.1, including multi-value returns via
// §8.5 — see func.go), whose bodies are `let`/`const`/multi-value `let`
// declarations, scalar/list-element/struct-field/compound assignment,
// `print(...)` and other function/method calls, `return`, the full
// operator set (§6, including `&`/`*`), control flow (if/elif/else,
// while, for-in, switch, break/continue), lists (`[]T` — literals,
// indexing, append/range/len; see list.go), and structs/pointers/
// receiver functions (§4.1, §4.4, §8.2 — struct literals, field read/
// write, pointer address-of/dereference, and method calls; see
// struct.go). Later steps extend genStmt/genValue one feature at a time,
// the same way parser's grammar grows.
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
// text: a top-level STTYPE for every struct declaration (must precede
// everything else, since a SLTYPE/FNTYPE/MPTYPE or FUNC parameter/local
// may reference a struct type — see genStructDecl), then a top-level
// SLTYPE for every list element type any function uses, then a top-level
// FNTYPE for every distinct function-type shape any function uses
// (list.go's typeRegistry.useFuncType — emitted after SLTYPE since a
// function type may itself take/return a list, e.g. `func([]int): int`),
// then a top-level MPTYPE for every distinct map key/value shape any
// function uses (useMapType — emitted last among the TYPE declarations,
// since a map's value type may itself be a struct/list/func), then every
// function's own FUNC block (main's under mainInternalName, a receiver
// function's under its qualified StructName_Method name, a closure
// literal's own CLOS block nested inline inside whichever FUNC contains
// it — see func.go's genFuncDecl and struct.go/closure.go), then the
// generated `!main` wrapper that bridges to it. Every FUNC's own body is
// compiled first, into a separate buffer, before this final assembly —
// SLTYPE/FNTYPE/MPTYPE registration happens as a side effect of that
// (typeToIR calls), so by the time this function's own loops run, every
// type any function body needed is already known.
func Generate(f *ast.File) (string, error) {
	structs := map[string]*ast.StructDecl{}
	for _, sd := range f.Structs {
		structs[sd.Name] = sd
	}

	var main *ast.FuncDecl
	sigs := map[string]funcSig{}
	methods := map[string]map[string]funcSig{}
	for _, fn := range f.Funcs {
		params := make([]ast.Type, len(fn.Params))
		for i, p := range fn.Params {
			params[i] = p.Type
		}
		if fn.Receiver != nil {
			structName := receiverStructName(fn.Receiver.Type)
			if methods[structName] == nil {
				methods[structName] = map[string]funcSig{}
			}
			methods[structName][fn.Name] = funcSig{Params: params, Results: fn.Results}
			continue
		}
		sigs[fn.Name] = funcSig{Params: params, Results: fn.Results}
		if fn.Name == "main" {
			main = fn
		}
	}
	if main == nil {
		return "", fmt.Errorf("codegen: no main function (run sema.Check first)")
	}

	types := &typeRegistry{used: map[string]bool{}}

	var structsIR strings.Builder
	for _, sd := range f.Structs {
		ir, err := genStructDecl(types, sd)
		if err != nil {
			return "", err
		}
		structsIR.WriteString(ir)
	}

	var funcsIR strings.Builder
	for _, fn := range f.Funcs {
		ir, err := genFuncDecl(fn, types, structs, sigs, methods)
		if err != nil {
			return "", err
		}
		funcsIR.WriteString(ir)
	}

	var b strings.Builder
	b.WriteString(structsIR.String())
	for _, elemName := range types.sorted() {
		elemIRType, err := scalarTypeToIR(ast.Type{Name: elemName})
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "SLTYPE\t%s\t%s\n", listTypeToken(elemName), elemIRType)
	}
	for _, decl := range types.funcTypeDecls() {
		b.WriteString(decl)
		b.WriteString("\n")
	}
	for _, decl := range types.mapTypeDecls() {
		b.WriteString(decl)
		b.WriteString("\n")
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
	types         *typeRegistry
	structs       map[string]*ast.StructDecl // struct name -> declaration, for struct.go's genStructZeroReset
	sigs          map[string]funcSig
	methods       map[string]map[string]funcSig // struct name -> method name -> sig, for struct.go's method-call codegen
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
	case *ast.DerefAssignStmt:
		return genDerefAssignStmt(g, s)
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
	irType, err := typeToIR(g.types, typ)
	if err != nil {
		return err
	}

	name := g.freshName(decl.Name)
	ref := varRef{Type: typ, ValOp: "%" + name}
	g.declareVar(ref.ValOp, irType)
	if needsIssetSlot(typ) {
		ref.SetOp = "%" + name + "_isset"
		g.declareVar(ref.SetOp, "^bool")
	}
	g.scope.declare(decl.Name, ref)

	return genInit(g, ref, decl.Init)
}

// genAssignStmt compiles `name = value`; `name[Index] = value` when Index
// is set — an ASET into the existing list (unlike a whole-list
// reassignment via genInit, this never reallocates, matching Go's own
// in-place slice-element-assignment semantics) or, when ref holds a map,
// an MSET instead (cascade_spec.md §5's `counts["a"] = 1` — creates the
// key if absent, overwrites it if present, exactly matching Go's own
// `m[k] = v`, so no special-casing beyond picking the right instruction
// is needed); or `name.Field = value` when Field is set, an FSET
// (cascade_spec.md §5) — works identically whether ref holds a struct
// value or a pointer to one, see ast.FieldExpr's doc.
func genAssignStmt(g *funcGen, stmt *ast.AssignStmt) error {
	ref, ok := g.scope.lookup(stmt.Name)
	if !ok {
		return fmt.Errorf("codegen: undefined name %q (sema bug)", stmt.Name)
	}
	if stmt.Index != nil {
		keyOrIdxOp, err := genValue(g, stmt.Index)
		if err != nil {
			return err
		}
		v, err := genValue(g, stmt.Value)
		if err != nil {
			return err
		}
		if ref.Type.Map != nil {
			g.emit("\tMSET\t%s\t%s\t%s\n", ref.ValOp, keyOrIdxOp, v)
		} else {
			g.emit("\tASET\t%s\t%s\t%s\n", ref.ValOp, keyOrIdxOp, v)
		}
		return nil
	}
	if stmt.Field != "" {
		v, err := genValue(g, stmt.Value)
		if err != nil {
			return err
		}
		g.emit("\tFSET\t%s\t>%s\t%s\n", ref.ValOp, stmt.Field, v)
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
	if lit, isMap := init.(*ast.MapLit); isMap {
		return genMapLiteralInit(g, ref, lit)
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
	if ref.Type.Ptr == nil && ref.Type.Elem == nil {
		if sd, ok := g.structs[ref.Type.Name]; ok {
			if err := genStructZeroReset(g, ref.ValOp, sd); err != nil {
				return err
			}
			if ref.SetOp != "" {
				g.emit("\tSET\t%s\tfalse\n", ref.SetOp)
			}
			return nil
		}
	}
	// A *non-nullable* map's base value is a real, writable empty map
	// (§2.2's `{}`), unlike a nullable map's none/unset state (still
	// `nil`, via the ordinary zeroValueLiteral path below — see its doc)
	// — writing into a nil Go map panics, so this needs its own MPMAKE
	// rather than a plain SET nil.
	if ref.Type.Map != nil && !ref.Type.Nullable {
		irType, err := typeToIR(g.types, ref.Type)
		if err != nil {
			return err
		}
		g.emit("\tMPMAKE\t%s\t%s\n", ref.ValOp, irType)
		return nil
	}
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
	if call.Receiver != nil {
		return genMethodCallStmt(g, call)
	}
	if call.IsClosureVar {
		return genClosureCallStmt(g, call)
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
	case "delete":
		return genDeleteCall(g, call)
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
	case *ast.StructLit:
		return genStructLit(g, v)
	case *ast.FieldExpr:
		return genFieldRead(g, v)
	case *ast.ClosureLit:
		return genClosureLit(g, v)
	default:
		return "", fmt.Errorf("codegen: unsupported value expression %T", e)
	}
}

// genNullCheck compiles `X is none`/`X is not none` (cascade_spec.md §7).
// sema.Check guarantees X is a plain identifier with a nullable type (see
// ast.NullCheckExpr's doc). A pointer variable has no isset flag at all —
// its nullability is native Go nil (see ast.Type's doc) — so it's
// compared directly against nil via EQ/NEQ instead; every other nullable
// type reads its own isset flag directly (scope.go's varRef): "is not
// none" is the flag itself, "is none" is its negation. Narrowing (§2.3)
// itself is a sema-only concept — see CLAUDE.md's "確定した設計判断" — so
// there is nothing further to do here.
func genNullCheck(g *funcGen, e *ast.NullCheckExpr) (string, error) {
	id, ok := e.X.(*ast.Ident)
	if !ok {
		return "", fmt.Errorf("codegen: null-check target must be an identifier (sema bug)")
	}
	ref, ok := g.scope.lookup(id.Name)
	if !ok {
		return "", fmt.Errorf("codegen: undefined name %q (sema bug)", id.Name)
	}
	if ref.Type.Ptr != nil {
		instr := "EQ"
		if e.Not {
			instr = "NEQ"
		}
		tmp := g.newTemp("^bool")
		g.emit("\t%s\t%s\t%s\tnil\n", instr, tmp, ref.ValOp)
		return tmp, nil
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
	if call.Receiver != nil {
		return genMethodCallValue(g, call)
	}
	if call.IsClosureVar {
		return genClosureCallValue(g, call)
	}
	switch call.Callee {
	case "string":
		return genStringConversion(g, call)
	case "len":
		return genLenCall(g, call)
	case "range":
		return genRangeCall(g, call)
	case "append":
		return genAppendCall(g, call)
	case "filter":
		return genFilterCall(g, call)
	case "map":
		return genMapCall(g, call)
	case "reduce":
		return genReduceCall(g, call)
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

// genUnary compiles a unary expression (cascade_spec.md §6). "&"
// (address-of) and "*" (dereference) don't fit the generic scalar-
// operator shape below at all — see struct.go's genAddrOf/genDeref — so
// they're dispatched separately. For the rest, AMIVM-IR has no unary-
// minus instruction, so "-x" is emitted as "SUB tmp 0 x"
// (seed_implementation_notes.md §5.6's exact pattern for the same gap);
// "~x" (bitwise NOT) has its own instruction, BNOT, so it needs no such
// workaround.
func genUnary(g *funcGen, e *ast.UnaryExpr) (string, error) {
	switch e.Op {
	case "&":
		return genAddrOf(g, e)
	case "*":
		return genDeref(g, e)
	}
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

// needsIssetSlot reports whether t needs a companion isset flag
// (cascade_spec.md §2.3) alongside its value — every nullable type
// except a pointer or a function type, both of which represent their
// nullability via Go's native nil instead (see ast.Type's doc and
// CLAUDE.md's "確定した設計判断").
func needsIssetSlot(t ast.Type) bool {
	return t.Nullable && t.Ptr == nil && t.Func == nil
}

// baseTypeName maps t — a scalar or struct type, never a list or a
// pointer — to the bare name portion of its AMIVM-IR type token, with no
// leading `^`. Shared by scalarTypeToIR and typeToIR's pointer-type case
// (list.go): amivm's pointer type token is a single `^*Name` token, not a
// nested `^*(^Name)` (verified against amivm/test_ir/08_pointer.ir), so
// the bare name is needed on its own to build that token. A struct type
// name (cascade_spec.md §4.1) isn't one of the four builtins below, but
// sema.Check has already verified it names a declared struct before
// codegen ever sees it (see CLAUDE.md's "意味検証の責任分担"), so codegen
// trusts it rather than re-validating.
func baseTypeName(t ast.Type) (string, error) {
	switch t.Name {
	case "int":
		return "int", nil
	case "float":
		return "float64", nil
	case "string":
		return "string", nil
	case "bool":
		return "bool", nil
	case "":
		return "", fmt.Errorf("codegen: type has no name (compound type used where a scalar/struct type was expected)")
	default:
		return t.Name, nil
	}
}

// scalarTypeToIR maps a Cascade scalar or struct type to its AMIVM-IR
// type token. The nullable flag plays no part in the token itself — a
// `T?`'s value variable has exactly T's IR type; its extra "is this set?"
// flag, when it has one at all (see needsIssetSlot), is a second,
// separate ^bool variable (see scope.go's varRef).
func scalarTypeToIR(t ast.Type) (string, error) {
	name, err := baseTypeName(t)
	if err != nil {
		return "", err
	}
	return "^" + name, nil
}

// zeroValueLiteral is the AMIVM-IR value token for t's Cascade base value
// (cascade_spec.md §2.1, §2.2, §4.4, §8.3), which happens to coincide
// with Go's zero value in every case: a list's base value is the empty
// list `[]` (§2.2), and Go's nil slice already behaves as an empty one
// for every operation Cascade exposes (len, range-over, append); a
// pointer's and a function value's base value are both always `none`
// (§2.2), each represented as Go's native nil (see ast.Type's doc)
// rather than an isset flag — `nil` is also what a nullable-and-unset
// variable of any other type resets to, so this one token serves these
// cases identically. A *non-nullable* map's own base value is `{}` (an
// actually writable empty map, §2.2) — writing into a nil Go map panics,
// unlike appending to a nil slice, so that case is handled by
// genResetToZero's own MPMAKE branch instead of reaching this function
// at all; `nil` here only ever fires for a *nullable* map's none/unset
// state (see genResetToZero's doc for why that's still safe — briefly,
// nothing can legally write into a nullable map without narrowing to
// non-nullable first, and by the time that's possible the value slot has
// already been through a real MPMAKE). A struct's own base value (every
// field at its own base value) has no single literal token either — see
// struct.go's genStructZeroReset, also dispatched to by genResetToZero
// instead of calling this function.
func zeroValueLiteral(t ast.Type) (string, error) {
	if t.Elem != nil || t.Ptr != nil || t.Func != nil || t.Map != nil {
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
