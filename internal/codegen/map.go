// map.go compiles cascade_spec.md §2.2/§4.5 (map<K, V>) — see codegen.go's
// package doc.
package codegen

import (
	"github.com/amisonnet8/cascade/internal/ast"
)

// genMapLiteralInit compiles a map literal (cascade_spec.md §3, §4.5)
// into ref: MPMAKE allocates a fresh, empty map, then each pair is MSET
// in. Like genListLiteralInit, a map literal is only ever compiled
// through a known assignment target (genInit) — see ast.MapLit's doc —
// never as a general sub-expression (genValue has no case for it,
// mirroring ast.ListLit's identical, pre-existing scope).
func genMapLiteralInit(g *funcGen, ref varRef, lit *ast.MapLit) error {
	irType, err := typeToIR(g.types, ref.Type)
	if err != nil {
		return err
	}
	g.emit("\tMPMAKE\t%s\t%s\n", ref.ValOp, irType)
	for _, pair := range lit.Pairs {
		k, err := genValue(g, pair.Key)
		if err != nil {
			return err
		}
		v, err := genValue(g, pair.Value)
		if err != nil {
			return err
		}
		g.emit("\tMSET\t%s\t%s\t%s\n", ref.ValOp, k, v)
	}
	if ref.SetOp != "" {
		g.emit("\tSET\t%s\ttrue\n", ref.SetOp)
	}
	return nil
}

// genForInMapStmt compiles `for k, v in M { ... }` (cascade_spec.md §7).
// MPKEYS gets the map's keys as an ordinary Cascade list in one
// instruction (amivm_spec.md §4.16 — added after Cascade's Step 10
// originally worked around its absence with the cascadert runtime's
// Keys() helper; see CLAUDE.md's "確定した設計判断"), then this loops
// over that list exactly like genForInStmt, MGETting each key's value
// inside the loop body.
func genForInMapStmt(g *funcGen, stmt *ast.ForInStmt) error {
	mapOp, err := genValue(g, stmt.List)
	if err != nil {
		return err
	}
	keyIRType, err := typeToIR(g.types, stmt.ElemType)
	if err != nil {
		return err
	}
	valueIRType, err := typeToIR(g.types, stmt.ValueType)
	if err != nil {
		return err
	}
	keysListIRType, err := typeToIR(g.types, ast.Type{Elem: &stmt.ElemType})
	if err != nil {
		return err
	}

	keysOp := g.newTemp(keysListIRType)
	g.emit("\tMPKEYS\t%s\t%s\n", keysOp, mapOp)

	lenOp := g.newTemp("^int")
	g.emit("\tCALL\t%s\t:\t?len\t%s\n", lenOp, keysOp)
	idxOp := g.newTemp("^int")
	g.emit("\tSET\t%s\t0\n", idxOp)

	startLabel := g.newLabel()
	bodyLabel := g.newLabel()
	continueLabel := g.newLabel()
	endLabel := g.newLabel()

	g.emit("\tLABEL\t#%s\n", startLabel)
	cmp := g.newTemp("^bool")
	g.emit("\tLT\t%s\t%s\t%s\n", cmp, idxOp, lenOp)
	g.emit("\tIF\t%s\t#%s\n", cmp, bodyLabel)
	g.emit("\tGOTO\t#%s\n", endLabel)
	g.emit("\tLABEL\t#%s\n", bodyLabel)

	outer := g.scope
	g.scope = newScope(outer)
	keyRef := varRef{Type: stmt.ElemType, ValOp: "%" + g.freshName(stmt.VarName)}
	g.declareVar(keyRef.ValOp, keyIRType)
	g.scope.declare(stmt.VarName, keyRef)
	g.emit("\tAGET\t%s\t%s\t%s\n", keyRef.ValOp, keysOp, idxOp)

	valueRef := varRef{Type: stmt.ValueType, ValOp: "%" + g.freshName(stmt.ValueVarName)}
	g.declareVar(valueRef.ValOp, valueIRType)
	g.scope.declare(stmt.ValueVarName, valueRef)
	g.emit("\tMGET\t%s\t%s\t%s\n", valueRef.ValOp, mapOp, keyRef.ValOp)

	g.pushBreak(endLabel)
	g.pushContinue(continueLabel)
	var bodyErr error
	for _, s := range stmt.Body {
		if bodyErr = genStmt(g, s); bodyErr != nil {
			break
		}
	}
	g.popContinue()
	g.popBreak()
	g.scope = outer
	if bodyErr != nil {
		return bodyErr
	}

	g.emit("\tGOTO\t#%s\n", continueLabel)
	g.emit("\tLABEL\t#%s\n", continueLabel)
	g.emit("\tADD\t%s\t%s\t1\n", idxOp, idxOp)
	g.emit("\tGOTO\t#%s\n", startLabel)
	g.emit("\tLABEL\t#%s\n", endLabel)
	return nil
}

// genDeleteCall compiles delete(m, key) (cascade_spec.md §13) directly
// via Go's own builtin delete(), reachable through CALL the same way
// len()/append() already are (see amivm_spec.md/CLAUDE.md's "組み込み関
// 数(close/len/cap等)は専用命令を持たずCALLに統合されている").
func genDeleteCall(g *funcGen, call *ast.CallExpr) error {
	mapOp, err := genValue(g, call.Args[0])
	if err != nil {
		return err
	}
	keyOp, err := genValue(g, call.Args[1])
	if err != nil {
		return err
	}
	g.emit("\tCALL\t:\t?delete\t%s\t%s\n", mapOp, keyOp)
	return nil
}
