// struct.go compiles cascade_spec.md §4.1 (structs), §4.4 (pointers), and
// §8.2 (receiver functions/method calls) — see codegen.go's package doc.
package codegen

import (
	"fmt"
	"strings"

	"github.com/amisonnet8/cascade/internal/ast"
)

// receiverStructName resolves a receiver's declared type to the struct
// name it names (cascade_spec.md §8.2: `Name` for a value receiver,
// `*Name` for a pointer receiver). Mirrors sema's identical
// receiverStructName check — codegen trusts sema already validated this
// (see CLAUDE.md's "意味検証の責任分担"), so there is no error case here.
func receiverStructName(t ast.Type) string {
	if t.Ptr != nil {
		return t.Ptr.Name
	}
	return t.Name
}

// genStructDecl compiles one struct declaration into its STTYPE/FIELD/
// ENDSTTYPE block (cascade_spec.md §4.1). Called directly from Generate,
// not per-function like the rest of codegen — a struct declaration is
// package-level, with nothing to hoist or scope-resolve.
func genStructDecl(slices *sliceRegistry, sd *ast.StructDecl) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "STTYPE\t^%s\n", sd.Name)
	for _, f := range sd.Fields {
		irType, err := typeToIR(slices, f.Type)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "FIELD\t>%s\t%s\n", f.Name, irType)
	}
	b.WriteString("ENDSTTYPE\n")
	return b.String(), nil
}

// genStructZeroReset zero-initializes valOp, a struct-typed variable
// (cascade_spec.md §4.2's "宣言のみ。ゼロ値で初期化される" rule extended to
// structs), field by field via FSET — AMIVM-IR has no "zero struct
// literal" SET token (see amivm_spec.md's STTYPE/FSET). A field that is
// itself another struct is zero-reset recursively into a fresh temp of
// its own type, then copied in with one FSET — Go structs are value
// types, so this correctly zeroes arbitrarily nested structs with no
// per-level special-casing (sema.Check rejects a nullable non-pointer
// field, so every field reaching here is either non-nullable or a
// natively-nil pointer — see Check's struct-field validation loop — so
// there is never an isset flag to worry about at the field level).
func genStructZeroReset(g *funcGen, valOp string, sd *ast.StructDecl) error {
	for _, f := range sd.Fields {
		if fsd, ok := g.structs[f.Type.Name]; ok && f.Type.Ptr == nil && f.Type.Elem == nil {
			fieldIRType, err := scalarTypeToIR(f.Type)
			if err != nil {
				return err
			}
			tmp := g.newTemp(fieldIRType)
			if err := genStructZeroReset(g, tmp, fsd); err != nil {
				return err
			}
			g.emit("\tFSET\t%s\t>%s\t%s\n", valOp, f.Name, tmp)
			continue
		}
		zero, err := zeroValueLiteral(f.Type)
		if err != nil {
			return err
		}
		g.emit("\tFSET\t%s\t>%s\t%s\n", valOp, f.Name, zero)
	}
	return nil
}

// genStructLit compiles a struct literal (cascade_spec.md §4.1) into a
// fresh temp, FSET field by field. sema.Check has already verified every
// declared field is given exactly once (see ast.StructLit's doc), so
// there's no need to zero-reset first.
func genStructLit(g *funcGen, lit *ast.StructLit) (string, error) {
	irType, err := scalarTypeToIR(ast.Type{Name: lit.TypeName})
	if err != nil {
		return "", err
	}
	tmp := g.newTemp(irType)
	for _, fi := range lit.Fields {
		v, err := genValue(g, fi.Value)
		if err != nil {
			return "", err
		}
		g.emit("\tFSET\t%s\t>%s\t%s\n", tmp, fi.Name, v)
	}
	return tmp, nil
}

// genFieldRead compiles `x.field` used as a value (cascade_spec.md §4.1,
// §8.2) into a fresh temp via FGET — works identically whether x is a
// struct value or a pointer to one (Go's own auto-deref on field access;
// see ast.FieldExpr's doc, no special-casing needed here).
func genFieldRead(g *funcGen, e *ast.FieldExpr) (string, error) {
	xOp, err := genValue(g, e.X)
	if err != nil {
		return "", err
	}
	irType, err := typeToIR(g.slices, e.ResultType)
	if err != nil {
		return "", err
	}
	tmp := g.newTemp(irType)
	g.emit("\tFGET\t%s\t%s\t>%s\n", tmp, xOp, e.Field)
	return tmp, nil
}

// genAddrOf compiles unary `&x` (cascade_spec.md §6) into a fresh pointer
// temp via ADDR. sema's isAddressable restricts X to a plain identifier —
// amivm's ADDR only accepts a bare variable as its operand (the "variable"
// operand category, amivm_spec.md §6 — $N/&N/%xxx/@xxx only, never a
// field-path or index expression), so there is nothing more general to
// support here.
func genAddrOf(g *funcGen, e *ast.UnaryExpr) (string, error) {
	id, ok := e.X.(*ast.Ident)
	if !ok {
		return "", fmt.Errorf("codegen: '&' operand must be a plain variable (sema bug)")
	}
	ref, ok := g.scope.lookup(id.Name)
	if !ok {
		return "", fmt.Errorf("codegen: undefined name %q (sema bug)", id.Name)
	}
	irType, err := typeToIR(g.slices, e.ResultType)
	if err != nil {
		return "", err
	}
	tmp := g.newTemp(irType)
	g.emit("\tADDR\t%s\t%s\n", tmp, ref.ValOp)
	return tmp, nil
}

// genDeref compiles unary `*p` used as a value (cascade_spec.md §6) into
// a fresh temp via PGET. The dereference-as-assignment-target form (`*p =
// v`) is DerefAssignStmt, compiled separately via PSET (see
// genDerefAssignStmt below) — genUnary only ever reaches this function
// for `*p` read as a value.
func genDeref(g *funcGen, e *ast.UnaryExpr) (string, error) {
	x, err := genValue(g, e.X)
	if err != nil {
		return "", err
	}
	irType, err := typeToIR(g.slices, e.ResultType)
	if err != nil {
		return "", err
	}
	tmp := g.newTemp(irType)
	g.emit("\tPGET\t%s\t%s\n", tmp, x)
	return tmp, nil
}

// genDerefAssignStmt compiles `*Ptr = Value` (cascade_spec.md §5) via
// PSET.
func genDerefAssignStmt(g *funcGen, stmt *ast.DerefAssignStmt) error {
	ptrOp, err := genValue(g, stmt.Ptr)
	if err != nil {
		return err
	}
	v, err := genValue(g, stmt.Value)
	if err != nil {
		return err
	}
	g.emit("\tPSET\t%s\t%s\n", ptrOp, v)
	return nil
}

// genReceiverOperand compiles a method call's receiver expression into
// the bare-variable operand token the CALL itself needs as its first
// argument, applying whichever automatic address-of/dereference
// sema.Check determined is necessary (cascade_spec.md §8.2 — see
// ast.CallExpr's RecvNeedsAddr/RecvNeedsDeref doc). At most one of the
// two is ever set.
func genReceiverOperand(g *funcGen, call *ast.CallExpr) (string, error) {
	recvOp, err := genValue(g, call.Receiver)
	if err != nil {
		return "", err
	}
	if call.RecvNeedsAddr {
		tmp := g.newTemp("^*" + call.RecvStruct)
		g.emit("\tADDR\t%s\t%s\n", tmp, recvOp)
		return tmp, nil
	}
	if call.RecvNeedsDeref {
		tmp := g.newTemp("^" + call.RecvStruct)
		g.emit("\tPGET\t%s\t%s\n", tmp, recvOp)
		return tmp, nil
	}
	return recvOp, nil
}

// genMethodCallArgs compiles a method call's full CALL argument list: the
// (possibly address-of'd/dereferenced) receiver operand first, then the
// ordinary arguments — mirrors genFuncDecl's doc on why a receiver
// function's receiver is just its first plain parameter.
func genMethodCallArgs(g *funcGen, call *ast.CallExpr, sig funcSig) ([]string, error) {
	recvOp, err := genReceiverOperand(g, call)
	if err != nil {
		return nil, err
	}
	args, err := genCallArgs(g, call, sig)
	if err != nil {
		return nil, err
	}
	return append([]string{recvOp}, args...), nil
}

// methodAmivmName is the amivm-level name a method call resolves to
// (cascade_spec.md §8.2), matching genFuncDecl's identical
// StructName_Method naming for the method's own FUNC declaration.
func methodAmivmName(call *ast.CallExpr) string {
	return call.RecvStruct + "_" + call.Callee
}

// genMethodCallValue compiles a method call used as a value
// (cascade_spec.md §8.2) into a fresh temp. sema.Check guarantees
// len(sig.Results) == 1 here — see checkCallExprValue's receiver branch.
func genMethodCallValue(g *funcGen, call *ast.CallExpr) (string, error) {
	sig := g.methods[call.RecvStruct][call.Callee]
	args, err := genMethodCallArgs(g, call, sig)
	if err != nil {
		return "", err
	}
	resultIRType, err := typeToIR(g.slices, sig.Results[0])
	if err != nil {
		return "", err
	}
	tmp := g.newTemp(resultIRType)
	emitCall(g, []string{tmp}, "!"+methodAmivmName(call), args)
	return tmp, nil
}

// genMethodCallStmt compiles a method call used as a bare statement
// (cascade_spec.md §8.2), discarding every result regardless of how many
// there are — mirrors genUserFuncCallStmt.
func genMethodCallStmt(g *funcGen, call *ast.CallExpr) error {
	sig := g.methods[call.RecvStruct][call.Callee]
	args, err := genMethodCallArgs(g, call, sig)
	if err != nil {
		return err
	}
	emitCall(g, nil, "!"+methodAmivmName(call), args)
	return nil
}
