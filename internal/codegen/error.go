// error.go compiles cascade_spec.md §8.6 (the built-in error type,
// error(), and postfix `expr?` propagation) — see codegen.go's package
// doc.
package codegen

import (
	"strings"

	"github.com/amisonnet8/cascade/internal/ast"
)

// errorStructDecl is the built-in `error` type's synthetic struct
// declaration (cascade_spec.md §2.2, §8.6) — mirrors sema's identical
// errorStructDecl (see CLAUDE.md's "意味検証の責任分担": codegen keeps its
// own copy rather than depending on sema internals, the same
// independence funcSig/mainInternalName already follow).
var errorStructDecl = &ast.StructDecl{
	Name:   "error",
	Fields: []ast.StructField{{Name: "message", Type: ast.Type{Name: "string"}}},
}

// genErrorCall compiles error(message) (cascade_spec.md §8.6, §13) into
// a fresh temp — mirrors genStructLit, just hardcoded for error's one
// "message" field.
func genErrorCall(g *funcGen, call *ast.CallExpr) (string, error) {
	msgOp, err := genValue(g, call.Args[0])
	if err != nil {
		return "", err
	}
	tmp := g.newTemp("^error")
	g.emit("\tFSET\t%s\t>message\t%s\n", tmp, msgOp)
	return tmp, nil
}

// genZeroValueToken returns an AMIVM-IR token holding t's Cascade base
// value, for use as a RET operand in genErrorProp's early return.
// Unlike zeroValueLiteral (a single literal token), a struct's base
// value has no such literal — see struct.go's genStructZeroReset — so
// this allocates a fresh temp and zero-resets it into that when needed,
// mirroring genResetToZero but returning a token rather than emitting
// into an existing ref.
func genZeroValueToken(g *funcGen, t ast.Type) (string, error) {
	if t.Ptr == nil && t.Elem == nil && t.Map == nil {
		if sd, ok := g.structs[t.Name]; ok {
			irType, err := typeToIR(g.types, t)
			if err != nil {
				return "", err
			}
			tmp := g.newTemp(irType)
			if err := genStructZeroReset(g, tmp, sd); err != nil {
				return "", err
			}
			return tmp, nil
		}
	}
	return zeroValueLiteral(t)
}

// genErrorProp compiles postfix `expr?` (cascade_spec.md §8.6): calls
// e.Call, requesting both its success and error? results (via
// emitCallWithResults's own nullable-result expansion — the error
// result's own isset flag doubles as "is this actually an error"), and
// if the error is set, immediately RETs from the enclosing function/
// closure with a zero value for every one of g.results except the last
// (see genZeroValueToken), plus the propagated error itself. Otherwise
// execution falls through and the whole expression evaluates to the
// call's own success value — sema.Check has already guaranteed that
// value's type is non-nullable (see checkErrorPropExpr's doc), so there
// is nothing further to propagate about it here.
func genErrorProp(g *funcGen, e *ast.ErrorPropExpr) (string, error) {
	calleeName, sig, args, err := resolveCall(g, e.Call)
	if err != nil {
		return "", err
	}
	refs, err := emitCallWithResults(g, calleeName, args, sig.Results)
	if err != nil {
		return "", err
	}
	errRef := refs[len(refs)-1]

	g.emit("\tIF\t%s\n", errRef.SetOp)

	var retVals []string
	for _, rt := range g.results[:len(g.results)-1] {
		zero, err := genZeroValueToken(g, rt)
		if err != nil {
			return "", err
		}
		retVals = append(retVals, zero)
		if needsIssetSlot(rt) {
			retVals = append(retVals, "false")
		}
	}
	retVals = append(retVals, errRef.ValOp, "true")
	g.emit("\tRET\t%s\n", strings.Join(retVals, "\t"))
	g.emit("\tENDIF\n")
	return refs[0].ValOp, nil
}
