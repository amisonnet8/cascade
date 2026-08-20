// Package sema performs semantic analysis on a Cascade ast.File, since
// amivm delegates all type/scope checking to go/types and does not check
// anything itself (see CLAUDE.md "意味検証の責任分担"). Only the checks
// Steps 1-7 need are implemented so far: a single well-formed `main` plus
// any number of other functions (§8.1) — including multi-value returns
// (§8.5) — scope-resolved `let`/`const` declarations and scalar/list-
// element/compound assignment, nullable-type (`T?`) compatibility and
// narrowing (§2.3, §4.2, §5, §7), the full operator set (§6), control
// flow (if/elif/else, while, for-in, switch, break/continue), and lists
// (`[]T` — literals, indexing, append/range/len). Later steps add
// structs, closures, and everything past that.
//
// A function whose non-void body doesn't obviously return on every path
// isn't checked here (no control-flow reachability analysis is
// implemented yet) — such a mistake surfaces as amivm's less friendly
// "missing return" error instead. Nullable return types (`func f(): T?`)
// are rejected outright for now; see checkFuncSig.
package sema

import (
	"fmt"

	"github.com/amisonnet8/cascade/internal/ast"
)

// mainInternalName is the amivm-level name codegen emits the user's `main`
// under (see codegen.go's package doc for why `main` itself can't be used
// directly). Must match codegen's mainInternalName constant.
const mainInternalName = "cascade_main"

// funcSig is one function's signature (cascade_spec.md §8.1): its
// parameter and result types, used to validate every call to it. The
// whole table is built before any function body is checked (see Check),
// so forward references and (mutual) recursion both work regardless of
// declaration order.
type funcSig struct {
	Params  []ast.Type
	Results []ast.Type
}

// checker carries state shared across every function body being checked
// — currently just the signature table. This is the "小さな構造体
// (checker)" seed_implementation_notes.md §7 recommends introducing once
// program-wide (not just per-call-site) shared information is needed.
// Scope, by contrast, changes with every nested block, so it stays an
// explicit parameter throughout rather than becoming a checker field.
type checker struct {
	sigs map[string]funcSig
}

// reservedBuiltinNames are plain identifiers (not lexer keywords, unlike
// print/string/int/float/bool are not, but see below) that sema/codegen
// dispatch on directly by name (cascade_spec.md §13). A user-defined
// function reusing one of these names would be silently unreachable —
// shadowed by the builtin's own dispatch, which always checks first — so
// it's rejected outright instead. "string"/"int"/"float"/"bool" need no
// entry here: they're lexer keywords, so the parser already can't parse
// them as a function name in the first place (see parser's
// parsePrimaryAtom), and likewise for "send"/"chan"/"collect"/"map"/
// "error".
var reservedBuiltinNames = map[string]bool{
	"print":  true,
	"len":    true,
	"range":  true,
	"append": true,
}

// Check validates f. cascade_spec.md §12 requires exactly one `main`
// function, taking no parameters and returning a single non-nullable int;
// every function's signature (including main's) is collected into one
// table first, and then every function's body — main's and every other
// one's — is checked against it.
func Check(f *ast.File) error {
	c := &checker{sigs: map[string]funcSig{}}

	var main *ast.FuncDecl
	for _, fn := range f.Funcs {
		if fn.Name == mainInternalName {
			return fmt.Errorf("line %d: %q is a reserved name and cannot be used as a function name", fn.Line, mainInternalName)
		}
		if reservedBuiltinNames[fn.Name] {
			return fmt.Errorf("line %d: %q is a builtin function name and cannot be redefined", fn.Line, fn.Name)
		}
		if _, exists := c.sigs[fn.Name]; exists {
			return fmt.Errorf("line %d: duplicate function %q", fn.Line, fn.Name)
		}
		if err := checkFuncSig(fn); err != nil {
			return err
		}
		params := make([]ast.Type, len(fn.Params))
		for i, p := range fn.Params {
			params[i] = p.Type
		}
		c.sigs[fn.Name] = funcSig{Params: params, Results: fn.Results}
		if fn.Name == "main" {
			main = fn
		}
	}
	if main == nil {
		return fmt.Errorf("no 'main' function found")
	}
	if len(main.Params) != 0 {
		return fmt.Errorf("line %d: 'main' must take no parameters (cascade_spec.md §12)", main.Line)
	}
	if len(main.Results) != 1 || main.Results[0].Name != "int" || main.Results[0].Nullable {
		return fmt.Errorf("line %d: 'main' must return int (cascade_spec.md §12)", main.Line)
	}

	for _, fn := range f.Funcs {
		if err := c.checkFuncBody(fn); err != nil {
			return err
		}
	}
	return nil
}

// checkFuncSig validates fn's declared signature on its own, before any
// body is checked. Nullable return types would need a function's result
// to carry a companion isset flag across the CALL/RET boundary — the
// same 2-slot expansion codegen already does for a nullable *parameter*
// (see codegen's genCallArgs) — but nothing through Step 7 demonstrates
// it, and it's exactly what Step 11's `(T, error?)` convention needs, so
// it's deliberately deferred there rather than half-built now.
func checkFuncSig(fn *ast.FuncDecl) error {
	for _, r := range fn.Results {
		if r.Nullable {
			return fmt.Errorf("line %d: function %q: nullable return types are not supported yet", fn.Line, fn.Name)
		}
	}
	return nil
}

// checkFuncBody checks fn's body in a fresh scope pre-populated with its
// parameters (cascade_spec.md §8.1) — §10's scoping rules apply to
// parameters the same as any other declaration, including rejecting two
// parameters with the same name.
func (c *checker) checkFuncBody(fn *ast.FuncDecl) error {
	sc := newScope(nil)
	for _, p := range fn.Params {
		if !sc.declareLocal(p.Name, varInfo{Type: p.Type}) {
			return fmt.Errorf("line %d: duplicate parameter name %q", p.Line, p.Name)
		}
	}
	for _, stmt := range fn.Body {
		if err := c.checkStmt(sc, stmt, fn.Results, 0, 0); err != nil {
			return err
		}
	}
	return nil
}

// checkStmt dispatches to one statement's own check function. want is the
// enclosing function's declared return types, needed by ReturnStmt.
// loopDepth counts enclosing while loops (continue requires loopDepth >
// 0); breakDepth counts enclosing while loops *and* switches (break
// requires breakDepth > 0) — cascade_spec.md §7: break exits the
// innermost loop or switch, but continue always skips over an enclosing
// switch to reach the innermost loop, since "switch自体はループではない".
func (c *checker) checkStmt(sc *scope, stmt ast.Stmt, want []ast.Type, loopDepth, breakDepth int) error {
	switch s := stmt.(type) {
	case *ast.LetDecl:
		return c.checkLetDecl(sc, s)
	case *ast.MultiLetDecl:
		return c.checkMultiLetDecl(sc, s)
	case *ast.AssignStmt:
		return c.checkAssignStmt(sc, s)
	case *ast.CompoundAssignStmt:
		return c.checkCompoundAssignStmt(sc, s)
	case *ast.IncDecStmt:
		return c.checkIncDecStmt(sc, s)
	case *ast.ExprStmt:
		return c.checkExprStmt(sc, s)
	case *ast.ReturnStmt:
		return c.checkReturnStmt(sc, s, want)
	case *ast.IfStmt:
		return c.checkIfStmt(sc, s, want, loopDepth, breakDepth)
	case *ast.WhileStmt:
		return c.checkWhileStmt(sc, s, want, loopDepth, breakDepth)
	case *ast.SwitchStmt:
		return c.checkSwitchStmt(sc, s, want, loopDepth, breakDepth)
	case *ast.ForInStmt:
		return c.checkForInStmt(sc, s, want, loopDepth, breakDepth)
	case *ast.BreakStmt:
		if breakDepth == 0 {
			return fmt.Errorf("line %d: break outside of a loop or switch", s.Line)
		}
		return nil
	case *ast.ContinueStmt:
		if loopDepth == 0 {
			return fmt.Errorf("line %d: continue outside of a loop", s.Line)
		}
		return nil
	default:
		return fmt.Errorf("line %d: unsupported statement %T", ast.StmtLine(stmt), stmt)
	}
}

// checkBlock checks stmts in a fresh child scope of sc (cascade_spec.md
// §10: every `{ }` block gets its own scope).
func (c *checker) checkBlock(sc *scope, stmts []ast.Stmt, want []ast.Type, loopDepth, breakDepth int) error {
	return c.checkStmtsIn(newScope(sc), stmts, want, loopDepth, breakDepth)
}

// checkStmtsIn checks stmts directly in sc, with no new scope pushed —
// used where the caller already created (and possibly narrowed, see
// checkIfStmt) the scope stmts should run in.
func (c *checker) checkStmtsIn(sc *scope, stmts []ast.Stmt, want []ast.Type, loopDepth, breakDepth int) error {
	for _, stmt := range stmts {
		if err := c.checkStmt(sc, stmt, want, loopDepth, breakDepth); err != nil {
			return err
		}
	}
	return nil
}

// checkIfStmt checks an if/elif/else chain (cascade_spec.md §7): every
// condition must be a non-nullable bool, and each clause/else body is
// checked in its own scope with §2.3's null-narrowing applied where it
// can be determined statically from the clause's own condition (see
// narrowedVarInfo) — and, for the single-clause `if x is none { ... }`
// shape whose body always exits, propagated into sc itself for the rest
// of the enclosing block (cascade_spec.md §2.3's own example).
func (c *checker) checkIfStmt(sc *scope, stmt *ast.IfStmt, want []ast.Type, loopDepth, breakDepth int) error {
	for _, clause := range stmt.Clauses {
		ct, err := c.exprType(sc, clause.Cond)
		if err != nil {
			return err
		}
		if ct.Name != "bool" || ct.Nullable {
			return fmt.Errorf("line %d: if condition must be bool, got %s", ast.ExprLine(clause.Cond), typeString(ct))
		}

		inner := newScope(sc)
		if name, info, ok := narrowedVarInfo(sc, clause.Cond, true); ok {
			inner.shadow(name, info)
		}
		if err := c.checkStmtsIn(inner, clause.Body, want, loopDepth, breakDepth); err != nil {
			return err
		}
	}

	if stmt.Else != nil {
		inner := newScope(sc)
		if len(stmt.Clauses) == 1 {
			if name, info, ok := narrowedVarInfo(sc, stmt.Clauses[0].Cond, false); ok {
				inner.shadow(name, info)
			}
		}
		if err := c.checkStmtsIn(inner, stmt.Else, want, loopDepth, breakDepth); err != nil {
			return err
		}
	}

	if len(stmt.Clauses) == 1 && stmt.Else == nil {
		if name, info, ok := narrowedVarInfo(sc, stmt.Clauses[0].Cond, false); ok && endsInUnconditionalExit(stmt.Clauses[0].Body) {
			sc.shadow(name, info)
		}
	}

	return nil
}

// narrowedVarInfo returns the varInfo X would have if narrowed to
// non-null, when cond is exactly `X is none`/`X is not none`
// (cascade_spec.md §2.3, §7) on a plain identifier already declared (and
// nullable) in sc — wantNot selects which of the two (true for "is not
// none", false for "is none") cond must be for narrowing to apply.
func narrowedVarInfo(sc *scope, cond ast.Expr, wantNot bool) (name string, info varInfo, ok bool) {
	nc, isCheck := cond.(*ast.NullCheckExpr)
	if !isCheck || nc.Not != wantNot {
		return "", varInfo{}, false
	}
	id, isIdent := nc.X.(*ast.Ident)
	if !isIdent {
		return "", varInfo{}, false
	}
	orig, found := sc.lookup(id.Name)
	if !found || !orig.Type.Nullable {
		return "", varInfo{}, false
	}
	narrowed := orig
	narrowed.Type = ast.Type{Name: orig.Type.Name, Nullable: false}
	return id.Name, narrowed, true
}

// endsInUnconditionalExit reports whether body's last statement always
// leaves it (cascade_spec.md §2.3's narrowing condition: "`return`/
// `break`/`continue`で抜ける形になっている場合"). This is a syntactic
// check on the last statement only, not full control-flow analysis —
// matching the spec's own single-statement example (`if input is none {
// return }`).
func endsInUnconditionalExit(body []ast.Stmt) bool {
	if len(body) == 0 {
		return false
	}
	switch body[len(body)-1].(type) {
	case *ast.ReturnStmt, *ast.BreakStmt, *ast.ContinueStmt:
		return true
	default:
		return false
	}
}

func (c *checker) checkWhileStmt(sc *scope, stmt *ast.WhileStmt, want []ast.Type, loopDepth, breakDepth int) error {
	ct, err := c.exprType(sc, stmt.Cond)
	if err != nil {
		return err
	}
	if ct.Name != "bool" || ct.Nullable {
		return fmt.Errorf("line %d: while condition must be bool, got %s", ast.ExprLine(stmt.Cond), typeString(ct))
	}
	return c.checkBlock(sc, stmt.Body, want, loopDepth+1, breakDepth+1)
}

// checkSwitchStmt checks a tagged or untagged switch (cascade_spec.md
// §7). Tagged: each case value must match the tag's type exactly (no
// implicit conversion, same rule as every other comparison). Untagged:
// each case value is itself a bool condition. Every case/default body is
// checked in its own scope with breakDepth+1 (switch is not a loop, so
// loopDepth is unchanged — see checkStmt's doc).
func (c *checker) checkSwitchStmt(sc *scope, stmt *ast.SwitchStmt, want []ast.Type, loopDepth, breakDepth int) error {
	var tagType ast.Type
	if stmt.Tag != nil {
		t, err := c.exprType(sc, stmt.Tag)
		if err != nil {
			return err
		}
		if t.Nullable {
			return fmt.Errorf("line %d: switch tag must be non-nullable, got %s", ast.ExprLine(stmt.Tag), typeString(t))
		}
		tagType = t
	}

	for _, cs := range stmt.Cases {
		for _, v := range cs.Values {
			vt, err := c.exprType(sc, v)
			if err != nil {
				return err
			}
			if stmt.Tag != nil {
				if vt.Nullable || vt.Name != tagType.Name {
					return fmt.Errorf("line %d: case value type %s does not match switch tag type %s", ast.ExprLine(v), typeString(vt), typeString(tagType))
				}
			} else if vt.Nullable || vt.Name != "bool" {
				return fmt.Errorf("line %d: case condition must be bool, got %s", ast.ExprLine(v), typeString(vt))
			}
		}
		if err := c.checkBlock(sc, cs.Body, want, loopDepth, breakDepth+1); err != nil {
			return err
		}
	}

	if stmt.Default != nil {
		if err := c.checkBlock(sc, stmt.Default, want, loopDepth, breakDepth+1); err != nil {
			return err
		}
	}
	return nil
}

// checkForInStmt checks `for x in List { ... }` (cascade_spec.md §7):
// List must be a non-nullable list, and the loop variable x is declared
// with its element type in a fresh scope wrapping the body (loopDepth+1,
// breakDepth+1, same as while — see checkStmt's doc).
func (c *checker) checkForInStmt(sc *scope, stmt *ast.ForInStmt, want []ast.Type, loopDepth, breakDepth int) error {
	t, err := c.exprType(sc, stmt.List)
	if err != nil {
		return err
	}
	if t.Nullable || t.Elem == nil {
		return fmt.Errorf("line %d: for-in requires a list, got %s", ast.ExprLine(stmt.List), typeString(t))
	}
	stmt.ElemType = *t.Elem

	inner := newScope(sc)
	inner.declareLocal(stmt.VarName, varInfo{Type: stmt.ElemType})
	return c.checkStmtsIn(inner, stmt.Body, want, loopDepth+1, breakDepth+1)
}

// checkCompoundAssignStmt validates `name op= value` (cascade_spec.md
// §5) by reusing binaryResultType as if it were `name = name op value` —
// the same rules apply (e.g. `+=` on a string concatenates, `%=` requires
// int), so there is no separate rule set to maintain.
func (c *checker) checkCompoundAssignStmt(sc *scope, stmt *ast.CompoundAssignStmt) error {
	info, ok := sc.lookup(stmt.Name)
	if !ok {
		return fmt.Errorf("line %d: undefined name %q", stmt.Line, stmt.Name)
	}
	if info.Const {
		return fmt.Errorf("line %d: cannot assign to %q (declared const)", stmt.Line, stmt.Name)
	}
	vt, err := c.exprType(sc, stmt.Value)
	if err != nil {
		return err
	}
	rt, err := binaryResultType(stmt.Op, info.Type, vt, stmt.Line)
	if err != nil {
		return err
	}
	if rt.Name != info.Type.Name {
		return fmt.Errorf("line %d: cannot assign %s to %s", stmt.Line, typeString(rt), typeString(info.Type))
	}
	return nil
}

// checkIncDecStmt validates `name++`/`name--` (cascade_spec.md §5): name
// must be a non-nullable, non-const int or float.
func (c *checker) checkIncDecStmt(sc *scope, stmt *ast.IncDecStmt) error {
	info, ok := sc.lookup(stmt.Name)
	if !ok {
		return fmt.Errorf("line %d: undefined name %q", stmt.Line, stmt.Name)
	}
	if info.Const {
		return fmt.Errorf("line %d: cannot assign to %q (declared const)", stmt.Line, stmt.Name)
	}
	if info.Type.Nullable || (info.Type.Name != "int" && info.Type.Name != "float") {
		return fmt.Errorf("line %d: %s requires a non-nullable int or float, got %s", stmt.Line, stmt.Op, typeString(info.Type))
	}
	return nil
}

// checkLetDecl validates a `let`/`const` declaration (cascade_spec.md
// §4.2) and records its resolved type both in sc (for later statements to
// look up) and on the AST node itself (ResolvedType, so codegen doesn't
// have to re-infer it — see LetDecl's doc comment).
func (c *checker) checkLetDecl(sc *scope, decl *ast.LetDecl) error {
	if decl.Const && decl.Init == nil {
		return fmt.Errorf("line %d: 'const %s' requires an initializer", decl.Line, decl.Name)
	}

	typ := decl.Type
	if !typeGiven(typ) {
		// `let x = Init`: the type is inferred entirely from Init, which
		// rules out `let x = none` (§2.3's whole point is that `T?` must
		// be written explicitly) — exprType already rejects a bare
		// NoneLit for exactly this reason.
		if decl.Init == nil {
			return fmt.Errorf("line %d: 'let %s' needs either a type or an initializer", decl.Line, decl.Name)
		}
		t, err := c.exprType(sc, decl.Init)
		if err != nil {
			return err
		}
		typ = t
	} else if decl.Init != nil {
		if err := c.checkAssignable(sc, typ, decl.Init); err != nil {
			return err
		}
	}
	decl.ResolvedType = typ

	if !sc.declareLocal(decl.Name, varInfo{Type: typ, Const: decl.Const}) {
		return fmt.Errorf("line %d: %q is already declared in this scope (cascade_spec.md §10)", decl.Line, decl.Name)
	}
	return nil
}

// checkMultiLetDecl validates a multi-value `let`/`const` declaration
// (cascade_spec.md §5, §8.5): Init's callee must be a known function
// (sema has no way to know a value-typed expression's "arity" beyond a
// direct call — the parser already enforces this shape, see
// parseMultiLetDecl) returning exactly as many values as there are
// Names. "_" entries are validated like any other but declare nothing
// (cascade_spec.md §5).
func (c *checker) checkMultiLetDecl(sc *scope, decl *ast.MultiLetDecl) error {
	sig, ok := c.sigs[decl.Init.Callee]
	if !ok {
		return fmt.Errorf("line %d: undefined function %q", decl.Init.Line, decl.Init.Callee)
	}
	if err := c.checkCallArgs(sc, decl.Init, sig); err != nil {
		return err
	}
	if len(sig.Results) != len(decl.Names) {
		return fmt.Errorf("line %d: %s() returns %d value(s), but %d name(s) given", decl.Init.Line, decl.Init.Callee, len(sig.Results), len(decl.Names))
	}
	decl.ResolvedTypes = sig.Results

	for i, name := range decl.Names {
		if name == "_" {
			continue
		}
		if !sc.declareLocal(name, varInfo{Type: sig.Results[i], Const: decl.Const}) {
			return fmt.Errorf("line %d: %q is already declared in this scope (cascade_spec.md §10)", decl.Line, name)
		}
	}
	return nil
}

// checkAssignStmt validates `name = value` or, when Index is set,
// `name[Index] = value` (cascade_spec.md §5). Index assignment mutates
// element content rather than rebinding the variable itself, so it's
// allowed even on a `const`-declared list (only `name = ...` itself is
// forbidden there).
func (c *checker) checkAssignStmt(sc *scope, stmt *ast.AssignStmt) error {
	info, ok := sc.lookup(stmt.Name)
	if !ok {
		return fmt.Errorf("line %d: undefined name %q", stmt.Line, stmt.Name)
	}
	if stmt.Index != nil {
		if info.Type.Nullable || info.Type.Elem == nil {
			return fmt.Errorf("line %d: cannot index into %s", stmt.Line, typeString(info.Type))
		}
		it, err := c.exprType(sc, stmt.Index)
		if err != nil {
			return err
		}
		if it.Nullable || it.Name != "int" {
			return fmt.Errorf("line %d: list index must be int, got %s", ast.ExprLine(stmt.Index), typeString(it))
		}
		return c.checkAssignable(sc, *info.Type.Elem, stmt.Value)
	}
	if info.Const {
		return fmt.Errorf("line %d: cannot assign to %q (declared const)", stmt.Line, stmt.Name)
	}
	return c.checkAssignable(sc, info.Type, stmt.Value)
}

// checkExprStmt validates a call-expression statement: the `print`
// builtin (cascade_spec.md §13), or a call to any known function
// (cascade_spec.md §8.1) — a statement discards whatever it returns
// (zero, one, or many values), so unlike checkCallExprValue there is no
// result-count restriction here.
func (c *checker) checkExprStmt(sc *scope, stmt *ast.ExprStmt) error {
	call, ok := stmt.X.(*ast.CallExpr)
	if !ok {
		return fmt.Errorf("line %d: expected a call expression", ast.ExprLine(stmt.X))
	}
	switch call.Callee {
	case "print":
		if len(call.Args) != 1 {
			return fmt.Errorf("line %d: print expects exactly 1 argument, got %d", call.Line, len(call.Args))
		}
		t, err := c.exprType(sc, call.Args[0])
		if err != nil {
			return err
		}
		if t.Name != "string" || t.Nullable {
			return fmt.Errorf("line %d: print expects string, got %s", call.Line, typeString(t))
		}
		return nil
	default:
		sig, ok := c.sigs[call.Callee]
		if !ok {
			return fmt.Errorf("line %d: unsupported call to %q", call.Line, call.Callee)
		}
		return c.checkCallArgs(sc, call, sig)
	}
}

func (c *checker) checkReturnStmt(sc *scope, stmt *ast.ReturnStmt, want []ast.Type) error {
	if len(stmt.Results) != len(want) {
		return fmt.Errorf("line %d: expected %d return value(s), got %d", stmt.Line, len(want), len(stmt.Results))
	}
	for i, r := range stmt.Results {
		if err := c.checkAssignable(sc, want[i], r); err != nil {
			return err
		}
	}
	return nil
}

// checkAssignable validates that value may be assigned/initialized into a
// variable of type target (cascade_spec.md §2.3, §5, §3): `none` is only
// valid against a nullable target; a list literal is checked recursively
// against target's element type (see checkListLiteralAgainst) rather than
// through the general exprType path, since an empty `[]` (and, less
// obviously, a non-empty one) has no type of its own without a target —
// mirroring how NoneLit is handled; otherwise value's own type must have
// target's exact shape (ignoring nullability — Cascade never does
// implicit conversion, and §6's note on `+` makes the same point for
// operators), and a nullable value may only widen into a nullable target,
// never narrow implicitly into a non-nullable one (narrowing requires an
// explicit `is not none` check, §2.3). This same function doubles as
// argument-type checking for a function call (see checkCallArgs) — a
// parameter is assigned into exactly the same way a `let`/`return` target
// is.
func (c *checker) checkAssignable(sc *scope, target ast.Type, value ast.Expr) error {
	if _, isNone := value.(*ast.NoneLit); isNone {
		if !target.Nullable {
			return fmt.Errorf("line %d: cannot assign 'none' to non-nullable type %s", ast.ExprLine(value), typeString(target))
		}
		return nil
	}
	if lit, isList := value.(*ast.ListLit); isList {
		return c.checkListLiteralAgainst(sc, lit, target)
	}
	vt, err := c.exprType(sc, value)
	if err != nil {
		return err
	}
	if vt.Nullable && !target.Nullable {
		return fmt.Errorf("line %d: cannot assign %s to non-nullable %s (narrow with 'is not none' first)", ast.ExprLine(value), typeString(vt), typeString(target))
	}
	if !typeShapeEqual(vt, target) {
		return fmt.Errorf("line %d: cannot assign %s to %s", ast.ExprLine(value), typeString(vt), typeString(target))
	}
	return nil
}

// checkCallArgs validates call's arguments against sig's parameter types
// (cascade_spec.md §8.1): the argument count must match, and each
// argument must be assignable to its corresponding parameter type (see
// checkAssignable — a call argument is checked exactly like any other
// assignment, including nullable widening).
func (c *checker) checkCallArgs(sc *scope, call *ast.CallExpr, sig funcSig) error {
	if len(call.Args) != len(sig.Params) {
		return fmt.Errorf("line %d: %s() expects %d argument(s), got %d", call.Line, call.Callee, len(sig.Params), len(call.Args))
	}
	for i, arg := range call.Args {
		if err := c.checkAssignable(sc, sig.Params[i], arg); err != nil {
			return err
		}
	}
	return nil
}

// checkListLiteralAgainst validates a list literal's elements against
// target's element type (cascade_spec.md §3, §4.3). target must itself be
// a list type — an empty `[]` is valid here (the loop simply doesn't
// run), unlike through exprType's context-free inference.
func (c *checker) checkListLiteralAgainst(sc *scope, lit *ast.ListLit, target ast.Type) error {
	if target.Elem == nil {
		return fmt.Errorf("line %d: cannot assign a list literal to non-list type %s", lit.Line, typeString(target))
	}
	for _, e := range lit.Elems {
		if err := c.checkAssignable(sc, *target.Elem, e); err != nil {
			return err
		}
	}
	return nil
}

// typeShapeEqual reports whether a and b are the same type, ignoring
// Nullable (checkAssignable already handles nullability separately: a
// non-nullable value may widen into a nullable target, but never the
// reverse).
func typeShapeEqual(a, b ast.Type) bool {
	if (a.Elem == nil) != (b.Elem == nil) {
		return false
	}
	if a.Elem != nil {
		return typeShapeEqual(*a.Elem, *b.Elem)
	}
	return a.Name == b.Name
}

// typeGiven reports whether t is an explicitly written type (`let x: T`)
// as opposed to the zero Type `let x = Init` leaves for inference — a
// list type has an empty Name, so checking Name alone isn't enough once
// list types exist.
func typeGiven(t ast.Type) bool {
	return t.Name != "" || t.Elem != nil
}

// exprType infers e's type. NoneLit has no type of its own (see its doc
// comment) and always fails here — callers that accept `none`
// (checkAssignable) special-case it before calling exprType.
func (c *checker) exprType(sc *scope, e ast.Expr) (ast.Type, error) {
	switch v := e.(type) {
	case *ast.StringLit:
		return ast.Type{Name: "string"}, nil
	case *ast.IntLit:
		return ast.Type{Name: "int"}, nil
	case *ast.FloatLit:
		return ast.Type{Name: "float"}, nil
	case *ast.BoolLit:
		return ast.Type{Name: "bool"}, nil
	case *ast.NoneLit:
		return ast.Type{}, fmt.Errorf("line %d: cannot infer a type from 'none' alone; give the variable an explicit nullable type (e.g. 'T?')", v.Line)
	case *ast.ListLit:
		return c.inferListLitType(sc, v)
	case *ast.IndexExpr:
		xt, err := c.exprType(sc, v.X)
		if err != nil {
			return ast.Type{}, err
		}
		if xt.Nullable || xt.Elem == nil {
			return ast.Type{}, fmt.Errorf("line %d: cannot index into %s", v.Line, typeString(xt))
		}
		it, err := c.exprType(sc, v.Index)
		if err != nil {
			return ast.Type{}, err
		}
		if it.Nullable || it.Name != "int" {
			return ast.Type{}, fmt.Errorf("line %d: list index must be int, got %s", ast.ExprLine(v.Index), typeString(it))
		}
		v.ResultType = *xt.Elem
		return *xt.Elem, nil
	case *ast.Ident:
		info, ok := sc.lookup(v.Name)
		if !ok {
			return ast.Type{}, fmt.Errorf("line %d: undefined name %q", v.Line, v.Name)
		}
		return info.Type, nil
	case *ast.CallExpr:
		return c.checkCallExprValue(sc, v)
	case *ast.UnaryExpr:
		xt, err := c.exprType(sc, v.X)
		if err != nil {
			return ast.Type{}, err
		}
		rt, err := unaryResultType(v.Op, xt, v.Line)
		if err != nil {
			return ast.Type{}, err
		}
		v.ResultType = rt
		return rt, nil
	case *ast.BinaryExpr:
		xt, err := c.exprType(sc, v.X)
		if err != nil {
			return ast.Type{}, err
		}
		yt, err := c.exprType(sc, v.Y)
		if err != nil {
			return ast.Type{}, err
		}
		rt, err := binaryResultType(v.Op, xt, yt, v.Line)
		if err != nil {
			return ast.Type{}, err
		}
		v.ResultType = rt
		return rt, nil
	case *ast.NullCheckExpr:
		xt, err := c.exprType(sc, v.X)
		if err != nil {
			return ast.Type{}, err
		}
		if !xt.Nullable {
			desc := "is none"
			if v.Not {
				desc = "is not none"
			}
			return ast.Type{}, fmt.Errorf("line %d: '%s' requires a nullable type, got %s", v.Line, desc, typeString(xt))
		}
		return ast.Type{Name: "bool"}, nil
	default:
		return ast.Type{}, fmt.Errorf("line %d: cannot determine the type of this expression yet", ast.ExprLine(e))
	}
}

// inferListLitType infers a list literal's type with no target context
// (cascade_spec.md §3), by inferring its element type from Elems[0] and
// then checking every other element matches it. An empty `[]` has
// nothing to infer from and is always an error here — callers with a
// target type instead go through checkListLiteralAgainst, which allows
// an empty literal.
func (c *checker) inferListLitType(sc *scope, lit *ast.ListLit) (ast.Type, error) {
	if len(lit.Elems) == 0 {
		return ast.Type{}, fmt.Errorf("line %d: cannot infer a type from an empty list literal; give it an explicit []T type", lit.Line)
	}
	elemType, err := c.exprType(sc, lit.Elems[0])
	if err != nil {
		return ast.Type{}, err
	}
	target := ast.Type{Elem: &elemType}
	if err := c.checkListLiteralAgainst(sc, lit, target); err != nil {
		return ast.Type{}, err
	}
	return target, nil
}

// checkCallExprValue validates a call expression used as a value (as
// opposed to a bare statement — see checkExprStmt): the `string()`
// conversion, the list builtins `len()`/`range()`/`append()`
// (cascade_spec.md §13), or a call to any known single-value-returning
// function (§8.1) — a multi-value function (§8.5) can't produce one
// value, so it's rejected here with a pointer to the multi-value `let`
// form instead. The rest of §13's builtins (filter/map/reduce, which
// need closures) land in the steps that need them.
func (c *checker) checkCallExprValue(sc *scope, call *ast.CallExpr) (ast.Type, error) {
	switch call.Callee {
	case "string":
		if len(call.Args) != 1 {
			return ast.Type{}, fmt.Errorf("line %d: string() expects exactly 1 argument, got %d", call.Line, len(call.Args))
		}
		t, err := c.exprType(sc, call.Args[0])
		if err != nil {
			return ast.Type{}, err
		}
		if t.Nullable || (t.Name != "int" && t.Name != "float" && t.Name != "bool") {
			return ast.Type{}, fmt.Errorf("line %d: string() does not support %s", call.Line, typeString(t))
		}
		call.ArgType = t
		return ast.Type{Name: "string"}, nil
	case "len":
		if len(call.Args) != 1 {
			return ast.Type{}, fmt.Errorf("line %d: len() expects exactly 1 argument, got %d", call.Line, len(call.Args))
		}
		t, err := c.exprType(sc, call.Args[0])
		if err != nil {
			return ast.Type{}, err
		}
		if t.Nullable || (t.Name != "string" && t.Elem == nil) {
			return ast.Type{}, fmt.Errorf("line %d: len() does not support %s", call.Line, typeString(t))
		}
		return ast.Type{Name: "int"}, nil
	case "range":
		if len(call.Args) != 2 {
			return ast.Type{}, fmt.Errorf("line %d: range() expects exactly 2 arguments, got %d", call.Line, len(call.Args))
		}
		for _, a := range call.Args {
			t, err := c.exprType(sc, a)
			if err != nil {
				return ast.Type{}, err
			}
			if t.Nullable || t.Name != "int" {
				return ast.Type{}, fmt.Errorf("line %d: range() requires int arguments, got %s", ast.ExprLine(a), typeString(t))
			}
		}
		elem := ast.Type{Name: "int"}
		return ast.Type{Elem: &elem}, nil
	case "append":
		if len(call.Args) != 2 {
			return ast.Type{}, fmt.Errorf("line %d: append() expects exactly 2 arguments, got %d", call.Line, len(call.Args))
		}
		lt, err := c.exprType(sc, call.Args[0])
		if err != nil {
			return ast.Type{}, err
		}
		if lt.Nullable || lt.Elem == nil {
			return ast.Type{}, fmt.Errorf("line %d: append() expects a list as its first argument, got %s", ast.ExprLine(call.Args[0]), typeString(lt))
		}
		if err := c.checkAssignable(sc, *lt.Elem, call.Args[1]); err != nil {
			return ast.Type{}, err
		}
		call.ArgType = lt
		return lt, nil
	default:
		sig, ok := c.sigs[call.Callee]
		if !ok {
			return ast.Type{}, fmt.Errorf("line %d: %q cannot be used as a value", call.Line, call.Callee)
		}
		if err := c.checkCallArgs(sc, call, sig); err != nil {
			return ast.Type{}, err
		}
		if len(sig.Results) == 0 {
			return ast.Type{}, fmt.Errorf("line %d: %s() returns no value", call.Line, call.Callee)
		}
		if len(sig.Results) > 1 {
			return ast.Type{}, fmt.Errorf("line %d: %s() returns multiple values; use a multi-value let (e.g. 'let a, b = %s(...)') to receive them", call.Line, call.Callee, call.Callee)
		}
		return sig.Results[0], nil
	}
}

// unaryResultType validates op's operand type and returns its result type
// (cascade_spec.md §6): "!" needs bool, "-"/"~" need int (or float for
// "-"), and either way the operand must be non-nullable (narrow with `is
// not none` first — deferred to Step 5, see CLAUDE.md's "確定した設計判断").
func unaryResultType(op string, xt ast.Type, line int) (ast.Type, error) {
	if xt.Nullable {
		return ast.Type{}, fmt.Errorf("line %d: operator %q needs a non-nullable operand, got %s", line, op, typeString(xt))
	}
	switch op {
	case "!":
		if xt.Name != "bool" {
			return ast.Type{}, fmt.Errorf("line %d: unary ! requires bool, got %s", line, typeString(xt))
		}
		return xt, nil
	case "-":
		if xt.Name != "int" && xt.Name != "float" {
			return ast.Type{}, fmt.Errorf("line %d: unary - requires int or float, got %s", line, typeString(xt))
		}
		return xt, nil
	case "~":
		if xt.Name != "int" {
			return ast.Type{}, fmt.Errorf("line %d: unary ~ requires int, got %s", line, typeString(xt))
		}
		return xt, nil
	default:
		return ast.Type{}, fmt.Errorf("line %d: unsupported unary operator %q", line, op)
	}
}

// binaryResultType validates op's operand types and returns its result
// type (cascade_spec.md §6). Cascade never does implicit conversion, so
// both operands must already share the exact same type; "+" is the one
// operator whose AMIVM-IR instruction depends on that shared type (ADD for
// int/float, CONCAT for string — codegen reads ResultType.Name to tell
// them apart, mirroring Seed's identical `+` dispatch).
func binaryResultType(op string, xt, yt ast.Type, line int) (ast.Type, error) {
	if xt.Nullable || yt.Nullable {
		return ast.Type{}, fmt.Errorf("line %d: operator %q needs non-nullable operands (narrow with 'is not none' first)", line, op)
	}
	if !typeShapeEqual(xt, yt) {
		return ast.Type{}, fmt.Errorf("line %d: operator %q: mismatched operand types %s and %s", line, op, typeString(xt), typeString(yt))
	}

	switch op {
	case "+":
		switch xt.Name {
		case "int", "float", "string":
			return xt, nil
		default:
			return ast.Type{}, fmt.Errorf("line %d: operator + does not support %s", line, typeString(xt))
		}
	case "-", "*", "/":
		if xt.Name != "int" && xt.Name != "float" {
			return ast.Type{}, fmt.Errorf("line %d: operator %q requires int or float operands, got %s", line, op, typeString(xt))
		}
		return xt, nil
	case "%":
		if xt.Name != "int" {
			return ast.Type{}, fmt.Errorf("line %d: operator %% requires int operands, got %s", line, typeString(xt))
		}
		return xt, nil
	case "&", "|", "^", "&^", "<<", ">>":
		if xt.Name != "int" {
			return ast.Type{}, fmt.Errorf("line %d: operator %q requires int operands, got %s", line, op, typeString(xt))
		}
		return xt, nil
	case "<", "<=", ">", ">=":
		if xt.Name != "int" && xt.Name != "float" && xt.Name != "string" {
			return ast.Type{}, fmt.Errorf("line %d: operator %q requires int, float, or string operands, got %s", line, op, typeString(xt))
		}
		return ast.Type{Name: "bool"}, nil
	case "==", "!=":
		return ast.Type{Name: "bool"}, nil
	case "&&", "||":
		if xt.Name != "bool" {
			return ast.Type{}, fmt.Errorf("line %d: operator %q requires bool operands, got %s", line, op, typeString(xt))
		}
		return ast.Type{Name: "bool"}, nil
	default:
		return ast.Type{}, fmt.Errorf("line %d: unsupported operator %q", line, op)
	}
}

// typeString formats t for error messages, e.g. "int", "string?", or
// "[]int" (the "?" always applies to the outermost type — see ast.Type's
// doc — so it's appended after any "[]" recursion, never before it).
func typeString(t ast.Type) string {
	base := t.Name
	if t.Elem != nil {
		base = "[]" + typeString(*t.Elem)
	}
	if t.Nullable {
		return base + "?"
	}
	return base
}
