// Package sema performs semantic analysis on a Cascade ast.File, since
// amivm delegates all type/scope checking to go/types and does not check
// anything itself (see CLAUDE.md "意味検証の責任分担"). Only the checks
// Steps 1-2 need are implemented so far: a single well-formed `main`,
// scope-resolved `let`/`const` declarations and scalar assignment, and
// nullable-type (`T?`) compatibility (cascade_spec.md §2.3, §4.2, §5).
// Later steps add operators, control flow, and everything past that.
package sema

import (
	"fmt"

	"github.com/amisonnet8/cascade/internal/ast"
)

// mainInternalName is the amivm-level name codegen emits the user's `main`
// under (see codegen.go's package doc for why `main` itself can't be used
// directly). Must match codegen's mainInternalName constant.
const mainInternalName = "cascade_main"

// Check validates f. cascade_spec.md §12 requires exactly one `main`
// function, taking no parameters and returning a single non-nullable int;
// its body is then checked statement by statement in a fresh top-level
// scope.
func Check(f *ast.File) error {
	var main *ast.FuncDecl
	for _, fn := range f.Funcs {
		if fn.Name == mainInternalName {
			return fmt.Errorf("line %d: %q is a reserved name and cannot be used as a function name", fn.Line, mainInternalName)
		}
		if fn.Name == "main" {
			if main != nil {
				return fmt.Errorf("line %d: duplicate 'main' function (first declared on line %d)", fn.Line, main.Line)
			}
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

	sc := newScope(nil)
	for _, stmt := range main.Body {
		if err := checkStmt(sc, stmt, main.Results); err != nil {
			return err
		}
	}
	return nil
}

// checkStmt dispatches to one statement's own check function. want is the
// enclosing function's declared return types, needed by ReturnStmt.
func checkStmt(sc *scope, stmt ast.Stmt, want []ast.Type) error {
	switch s := stmt.(type) {
	case *ast.LetDecl:
		return checkLetDecl(sc, s)
	case *ast.AssignStmt:
		return checkAssignStmt(sc, s)
	case *ast.ExprStmt:
		return checkExprStmt(sc, s)
	case *ast.ReturnStmt:
		return checkReturnStmt(sc, s, want)
	default:
		return fmt.Errorf("line %d: unsupported statement %T", ast.StmtLine(stmt), stmt)
	}
}

// checkLetDecl validates a `let`/`const` declaration (cascade_spec.md
// §4.2) and records its resolved type both in sc (for later statements to
// look up) and on the AST node itself (ResolvedType, so codegen doesn't
// have to re-infer it — see LetDecl's doc comment).
func checkLetDecl(sc *scope, decl *ast.LetDecl) error {
	if decl.Const && decl.Init == nil {
		return fmt.Errorf("line %d: 'const %s' requires an initializer", decl.Line, decl.Name)
	}

	typ := decl.Type
	if typ.Name == "" {
		// `let x = Init`: the type is inferred entirely from Init, which
		// rules out `let x = none` (§2.3's whole point is that `T?` must
		// be written explicitly) — exprType already rejects a bare
		// NoneLit for exactly this reason.
		if decl.Init == nil {
			return fmt.Errorf("line %d: 'let %s' needs either a type or an initializer", decl.Line, decl.Name)
		}
		t, err := exprType(sc, decl.Init)
		if err != nil {
			return err
		}
		typ = t
	} else if decl.Init != nil {
		if err := checkAssignable(sc, typ, decl.Init); err != nil {
			return err
		}
	}
	decl.ResolvedType = typ

	if !sc.declareLocal(decl.Name, varInfo{Type: typ, Const: decl.Const}) {
		return fmt.Errorf("line %d: %q is already declared in this scope (cascade_spec.md §10)", decl.Line, decl.Name)
	}
	return nil
}

func checkAssignStmt(sc *scope, stmt *ast.AssignStmt) error {
	info, ok := sc.lookup(stmt.Name)
	if !ok {
		return fmt.Errorf("line %d: undefined name %q", stmt.Line, stmt.Name)
	}
	if info.Const {
		return fmt.Errorf("line %d: cannot assign to %q (declared const)", stmt.Line, stmt.Name)
	}
	return checkAssignable(sc, info.Type, stmt.Value)
}

// checkExprStmt validates a call-expression statement. Only the `print`
// builtin (cascade_spec.md §13) is wired up so far.
func checkExprStmt(sc *scope, stmt *ast.ExprStmt) error {
	call, ok := stmt.X.(*ast.CallExpr)
	if !ok {
		return fmt.Errorf("line %d: expected a call expression", ast.ExprLine(stmt.X))
	}
	switch call.Callee {
	case "print":
		if len(call.Args) != 1 {
			return fmt.Errorf("line %d: print expects exactly 1 argument, got %d", call.Line, len(call.Args))
		}
		t, err := exprType(sc, call.Args[0])
		if err != nil {
			return err
		}
		if t.Name != "string" || t.Nullable {
			return fmt.Errorf("line %d: print expects string, got %s", call.Line, typeString(t))
		}
		return nil
	default:
		return fmt.Errorf("line %d: unsupported call to %q", call.Line, call.Callee)
	}
}

func checkReturnStmt(sc *scope, stmt *ast.ReturnStmt, want []ast.Type) error {
	if len(stmt.Results) != len(want) {
		return fmt.Errorf("line %d: expected %d return value(s), got %d", stmt.Line, len(want), len(stmt.Results))
	}
	for i, r := range stmt.Results {
		if err := checkAssignable(sc, want[i], r); err != nil {
			return err
		}
	}
	return nil
}

// checkAssignable validates that value may be assigned/initialized into a
// variable of type target (cascade_spec.md §2.3, §5): `none` is only valid
// against a nullable target, and otherwise value's own type must match
// target's scalar name exactly (Cascade never does implicit conversion —
// §6's note on `+` makes the same point for operators).
func checkAssignable(sc *scope, target ast.Type, value ast.Expr) error {
	if _, isNone := value.(*ast.NoneLit); isNone {
		if !target.Nullable {
			return fmt.Errorf("line %d: cannot assign 'none' to non-nullable type %s", ast.ExprLine(value), target.Name)
		}
		return nil
	}
	vt, err := exprType(sc, value)
	if err != nil {
		return err
	}
	if vt.Name != target.Name {
		return fmt.Errorf("line %d: cannot assign %s to %s", ast.ExprLine(value), typeString(vt), typeString(target))
	}
	return nil
}

// exprType infers e's type. NoneLit has no type of its own (see its doc
// comment) and always fails here — callers that accept `none` (checkAssignable)
// special-case it before calling exprType.
func exprType(sc *scope, e ast.Expr) (ast.Type, error) {
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
	case *ast.Ident:
		info, ok := sc.lookup(v.Name)
		if !ok {
			return ast.Type{}, fmt.Errorf("line %d: undefined name %q", v.Line, v.Name)
		}
		return info.Type, nil
	default:
		return ast.Type{}, fmt.Errorf("line %d: cannot determine the type of this expression yet", ast.ExprLine(e))
	}
}

// typeString formats t for error messages, e.g. "int" or "string?".
func typeString(t ast.Type) string {
	if t.Nullable {
		return t.Name + "?"
	}
	return t.Name
}
