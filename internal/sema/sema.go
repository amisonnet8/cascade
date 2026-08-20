// Package sema performs semantic analysis on a Cascade ast.File, since
// amivm delegates all type/scope checking to go/types and does not check
// anything itself (see CLAUDE.md "意味検証の責任分担"). Only the checks
// Step 1 needs — a single well-formed `main` — are implemented so far;
// later steps add type checking, scope resolution, nullable-type
// narrowing, and pipeline type-connection checks.
package sema

import (
	"fmt"

	"github.com/amisonnet8/cascade/internal/ast"
)

// mainInternalName is the amivm-level name codegen emits the user's `main`
// under (see codegen.go's package doc for why `main` itself can't be used
// directly). Must match codegen's mainInternalName constant.
const mainInternalName = "cascade_main"

// Check validates f. It is intentionally narrow so far: cascade_spec.md
// §12 requires exactly one `main` function, taking no parameters and
// returning a single non-nullable int.
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
	return nil
}
