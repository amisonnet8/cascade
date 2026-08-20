// package_level.go compiles cascade_spec.md §11.3's top-level
// `let`/`const` declarations — see codegen.go's package doc.
package codegen

import (
	"fmt"
	"strings"

	"github.com/amisonnet8/cascade/internal/ast"
)

// genTopLevelLets compiles every top-level `let`/`const` into GVAR
// declarations (cascade_spec.md §2's own module-level equivalent of a
// local `let`'s VAR) plus a synthesized `!cascade_init` FUNC — called by
// the generated `!main` wrapper before `!cascade_main` — that runs each
// one's own initializer in declaration order, reusing genInit exactly as
// a local `let` does (genInit only ever reads/writes through whatever
// ValOp/SetOp token a varRef carries, so it needs no changes at all to
// target a `@name` GVAR instead of a `%name` hoisted local). Returns the
// GVAR declaration lines, the !cascade_init FUNC's own IR text (empty
// when there are no top-level lets at all, so nothing is emitted for the
// common single-package-with-none case), and the package-level scope
// (every let's own varRef) that genFuncDecl/genStageDecl chain every
// ordinary function/stage body's own scope from — making a top-level let
// visible everywhere in the package, exactly like a struct/func/stage
// name already is via sigs/structs/stages, without a separate lookup
// path.
func genTopLevelLets(lets []*ast.LetDecl, types *typeRegistry, structs map[string]*ast.StructDecl) (gvarIR, initFuncIR string, packageScope *scope, err error) {
	packageScope = newScope(nil)
	if len(lets) == 0 {
		return "", "", packageScope, nil
	}

	g := &funcGen{scope: packageScope, types: types, structs: structs}

	var gvars strings.Builder
	for _, ld := range lets {
		typ := ld.ResolvedType
		irType, err := typeToIR(types, typ)
		if err != nil {
			return "", "", nil, err
		}
		ref := varRef{Type: typ, ValOp: "@" + ld.Name}
		fmt.Fprintf(&gvars, "GVAR\t%s\t%s\n", ref.ValOp, irType)
		if needsIssetSlot(typ) {
			ref.SetOp = "@" + ld.Name + "_isset"
			fmt.Fprintf(&gvars, "GVAR\t%s\t^bool\n", ref.SetOp)
		}
		g.scope.declare(ld.Name, ref)
		if err := genInit(g, ref, ld.Init); err != nil {
			return "", "", nil, err
		}
	}

	var initFunc strings.Builder
	initFunc.WriteString("FUNC\t!cascade_init\t:\n")
	for _, d := range g.decls {
		fmt.Fprintf(&initFunc, "\tVAR\t%s\t%s\n", d.Op, d.IRType)
	}
	initFunc.WriteString(g.b.String())
	initFunc.WriteString("ENDFUNC\n")

	return gvars.String(), initFunc.String(), packageScope, nil
}
