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
// `main`/`seed_main` split (see seed/CLAUDE.md "確定した設計判断") — record
// this decision in CLAUDE.md's own "確定した設計判断" once this package
// lands.
//
// Only enough is implemented so far to compile Step 1's bootstrap program:
// a single `main` function whose body is a `print(...)` call followed by
// `return <int literal>`. Later steps extend genStmt/genValue one feature
// at a time, the same way parser's grammar grows.
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

	var b strings.Builder
	fmt.Fprintf(&b, "FUNC\t!%s\t:\t^int\n", mainInternalName)
	g := &funcGen{}
	for _, stmt := range main.Body {
		if err := genStmt(g, stmt); err != nil {
			return "", err
		}
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

// funcGen accumulates the AMIVM-IR body of a single function being
// compiled. It grows scope/temp-variable/label state in later steps, the
// same way Seed's funcGen did (see seed/internal/codegen/codegen.go).
type funcGen struct {
	b strings.Builder
}

func (g *funcGen) emit(format string, args ...any) {
	fmt.Fprintf(&g.b, format, args...)
}

func genStmt(g *funcGen, stmt ast.Stmt) error {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		return genExprStmt(g, s)
	case *ast.ReturnStmt:
		return genReturnStmt(g, s)
	default:
		return fmt.Errorf("codegen: unsupported statement %T", stmt)
	}
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
		v, err := genValue(call.Args[0])
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
		v, err := genValue(r)
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

// genValue compiles e into an inline AMIVM-IR value token. Only literals
// are supported so far — identifiers (variable reads) and nested calls
// used as values arrive once Step 2 introduces variables.
func genValue(e ast.Expr) (string, error) {
	switch v := e.(type) {
	case *ast.StringLit:
		return strconv.Quote(v.Value), nil
	case *ast.IntLit:
		return strconv.FormatInt(v.Value, 10), nil
	default:
		return "", fmt.Errorf("codegen: unsupported value expression %T", e)
	}
}
