// closure.go compiles cascade_spec.md §8.3 (closures) and §8.4
// (filter/map/reduce) — see codegen.go's package doc.
package codegen

import (
	"fmt"
	"strings"

	"github.com/amisonnet8/cascade/internal/ast"
)

// genClosureLit compiles a closure literal (cascade_spec.md §8.3) into a
// fresh temp, via a nested CLOS...ENDCLOS block emitted inline within the
// enclosing function's own body (amivm requires CLOS's target to already
// be VAR-declared — see amivm_spec.md's CLOS — which newTemp's own
// declareVar call satisfies).
//
// The closure body compiles through its own, completely independent
// funcGen — fresh decls/seq/labelSeq/breakStack/continueStack, since a
// CLOS is its own nested Go func literal with its own local scope and
// its own goto/VAR-hoisting problem (seed_implementation_notes.md §1) —
// except its *scope* is a child of the enclosing scope at the point the
// literal appears, exactly mirroring real Go closure capture: a captured
// identifier resolves to the *same* underlying %xxx/$N token used
// outside, with no copying or special capture instruction needed (see
// amivm/test_ir/15_closure.ir, where `%count`/`%base` are referenced
// directly, unchanged, inside a CLOS body). A closure parameter is a
// consecutive `&N` slot exactly like a FUNC parameter's `$N` (including
// the same two-slot nullable expansion — see needsIssetSlot).
//
// amivm forbids CLOS from nesting inside another CLOS (amivm_spec.md
// §2.1's "CLOSはFUNC本体内にのみ出現し、ネスト不可") — sema.Check already
// rejects a closure literal appearing inside another closure's body
// before codegen ever runs (see checker.closureDepth), so this function
// never has to guard against it itself.
func genClosureLit(g *funcGen, lit *ast.ClosureLit) (string, error) {
	irType, err := typeToIR(g.types, lit.ResolvedType)
	if err != nil {
		return "", err
	}
	tmp := g.newTemp(irType)

	inner := &funcGen{scope: newScope(g.scope), types: g.types, structs: g.structs, sigs: g.sigs, methods: g.methods}

	var paramIRTypes []string
	argIdx := 0
	for _, p := range lit.Params {
		pIRType, err := typeToIR(g.types, p.Type)
		if err != nil {
			return "", err
		}
		argIdx++
		ref := varRef{Type: p.Type, ValOp: fmt.Sprintf("&%d", argIdx)}
		paramIRTypes = append(paramIRTypes, pIRType)
		if needsIssetSlot(p.Type) {
			argIdx++
			ref.SetOp = fmt.Sprintf("&%d", argIdx)
			paramIRTypes = append(paramIRTypes, "^bool")
		}
		inner.scope.declare(p.Name, ref)
	}

	resultIRTypes := make([]string, len(lit.Results))
	for i, r := range lit.Results {
		rIRType, err := typeToIR(g.types, r)
		if err != nil {
			return "", err
		}
		resultIRTypes[i] = rIRType
	}

	for _, stmt := range lit.Body {
		if err := genStmt(inner, stmt); err != nil {
			return "", err
		}
	}

	var head strings.Builder
	fmt.Fprintf(&head, "\tCLOS\t%s", tmp)
	for _, t := range paramIRTypes {
		head.WriteString("\t")
		head.WriteString(t)
	}
	head.WriteString("\t:")
	for _, t := range resultIRTypes {
		head.WriteString("\t")
		head.WriteString(t)
	}
	head.WriteString("\n")
	g.b.WriteString(head.String())
	for _, d := range inner.decls {
		g.emit("\tVAR\t%s\t%s\n", d.Op, d.IRType)
	}
	g.b.WriteString(inner.b.String())
	g.emit("ENDCLOS\n")
	return tmp, nil
}

// closureCallTarget returns an operand valid in amivm's callname category
// (`!xxx`/`!main`/`?xxx`/`?xxx.xxx`/`%xxx` — see amivm_spec.md §5) for
// calling the function value held in valOp. A `%xxx` (or `@xxx`) token is
// already valid as-is; anything else — most notably a plain function
// parameter (`$N`) or closure parameter (`&N`) holding a closure value,
// e.g. `func applyTwice(f: func(int): int, x: int): int { return
// f(f(x)) }` — is NOT accepted there (a real amivm constraint discovered
// empirically running examples/09_closures.cas through amivm — see
// CLAUDE.md's "確定した設計判断" — despite amivm/test_ir/15_closure.ir's
// own example only ever calling a `%xxx`), so it's copied into a fresh
// local temp first.
func closureCallTarget(g *funcGen, valOp, irType string) string {
	if strings.HasPrefix(valOp, "%") || strings.HasPrefix(valOp, "@") {
		return valOp
	}
	tmp := g.newTemp(irType)
	g.emit("\tSET\t%s\t%s\n", tmp, valOp)
	return tmp
}

// genClosureCallValue compiles a call to a closure-valued local variable
// (cascade_spec.md §8.3) used as a value, into a fresh temp — see
// closureCallTarget's doc for why the callee operand isn't always
// ref.ValOp directly.
func genClosureCallValue(g *funcGen, call *ast.CallExpr) (string, error) {
	ref, ok := g.scope.lookup(call.Callee)
	if !ok {
		return "", fmt.Errorf("codegen: undefined name %q (sema bug)", call.Callee)
	}
	sig := funcSig{Params: ref.Type.Func.Params, Results: ref.Type.Func.Results}
	args, err := genCallArgs(g, call, sig)
	if err != nil {
		return "", err
	}
	calleeIRType, err := typeToIR(g.types, ref.Type)
	if err != nil {
		return "", err
	}
	resultIRType, err := typeToIR(g.types, sig.Results[0])
	if err != nil {
		return "", err
	}
	tmp := g.newTemp(resultIRType)
	emitCall(g, []string{tmp}, closureCallTarget(g, ref.ValOp, calleeIRType), args)
	return tmp, nil
}

// genClosureCallStmt compiles a call to a closure-valued local variable
// used as a bare statement, discarding every result — mirrors
// genUserFuncCallStmt/genMethodCallStmt.
func genClosureCallStmt(g *funcGen, call *ast.CallExpr) error {
	ref, ok := g.scope.lookup(call.Callee)
	if !ok {
		return fmt.Errorf("codegen: undefined name %q (sema bug)", call.Callee)
	}
	sig := funcSig{Params: ref.Type.Func.Params, Results: ref.Type.Func.Results}
	args, err := genCallArgs(g, call, sig)
	if err != nil {
		return err
	}
	calleeIRType, err := typeToIR(g.types, ref.Type)
	if err != nil {
		return err
	}
	emitCall(g, nil, closureCallTarget(g, ref.ValOp, calleeIRType), args)
	return nil
}

// genFilterCall compiles filter(list, pred) (cascade_spec.md §8.4, §13).
// The predicate is evaluated exactly once per element (a predicate may
// have side effects, e.g. mutating a captured variable — see
// checkClosureLit's doc — so it must never run twice for the same
// element). Since the number of matches isn't known ahead of time, the
// result is over-allocated to the input's own length, filled from index
// 0 up as matches are found, then trimmed to the actual match count via
// SLICE — giving CLAUDE.md's "命令使用ゴール" SLICE instruction its first
// real use.
func genFilterCall(g *funcGen, call *ast.CallExpr) (string, error) {
	listOp, err := genValue(g, call.Args[0])
	if err != nil {
		return "", err
	}
	predOp, err := genValue(g, call.Args[1])
	if err != nil {
		return "", err
	}
	hofIRType, err := typeToIR(g.types, ast.Type{Func: call.HOFType, Nullable: true})
	if err != nil {
		return "", err
	}
	predOp = closureCallTarget(g, predOp, hofIRType)

	listIRType, err := typeToIR(g.types, call.ArgType)
	if err != nil {
		return "", err
	}
	elemIRType, err := typeToIR(g.types, *call.ArgType.Elem)
	if err != nil {
		return "", err
	}

	lenOp := g.newTemp("^int")
	g.emit("\tCALL\t%s\t:\t?len\t%s\n", lenOp, listOp)
	fullOp := g.newTemp(listIRType)
	g.emit("\tSLMAKE\t%s\t%s\t%s\n", fullOp, listIRType, lenOp)

	idxOp := g.newTemp("^int")
	g.emit("\tSET\t%s\t0\n", idxOp)
	outIdxOp := g.newTemp("^int")
	g.emit("\tSET\t%s\t0\n", outIdxOp)

	startLabel := g.newLabel()
	bodyLabel := g.newLabel()
	includeLabel := g.newLabel()
	skipLabel := g.newLabel()
	endLabel := g.newLabel()

	g.emit("\tLABEL\t#%s\n", startLabel)
	cmp := g.newTemp("^bool")
	g.emit("\tLT\t%s\t%s\t%s\n", cmp, idxOp, lenOp)
	g.emit("\tIF\t%s\t#%s\n", cmp, bodyLabel)
	g.emit("\tGOTO\t#%s\n", endLabel)
	g.emit("\tLABEL\t#%s\n", bodyLabel)

	elemOp := g.newTemp(elemIRType)
	g.emit("\tAGET\t%s\t%s\t%s\n", elemOp, listOp, idxOp)
	predResult := g.newTemp("^bool")
	emitCall(g, []string{predResult}, predOp, []string{elemOp})
	g.emit("\tIF\t%s\t#%s\n", predResult, includeLabel)
	g.emit("\tGOTO\t#%s\n", skipLabel)
	g.emit("\tLABEL\t#%s\n", includeLabel)
	g.emit("\tASET\t%s\t%s\t%s\n", fullOp, outIdxOp, elemOp)
	g.emit("\tADD\t%s\t%s\t1\n", outIdxOp, outIdxOp)
	g.emit("\tLABEL\t#%s\n", skipLabel)
	g.emit("\tADD\t%s\t%s\t1\n", idxOp, idxOp)
	g.emit("\tGOTO\t#%s\n", startLabel)
	g.emit("\tLABEL\t#%s\n", endLabel)

	result := g.newTemp(listIRType)
	g.emit("\tSLICE\t%s\t%s\t0\t%s\n", result, fullOp, outIdxOp)
	return result, nil
}

// genMapCall compiles map(list, f) (cascade_spec.md §8.4, §13) into a
// freshly SLMAKE'd result of the same length as the input, filled
// element-by-element by calling f — mirrors genRangeCall/genAppendCall's
// counted-loop shape.
func genMapCall(g *funcGen, call *ast.CallExpr) (string, error) {
	listOp, err := genValue(g, call.Args[0])
	if err != nil {
		return "", err
	}
	fnOp, err := genValue(g, call.Args[1])
	if err != nil {
		return "", err
	}
	hofIRType, err := typeToIR(g.types, ast.Type{Func: call.HOFType, Nullable: true})
	if err != nil {
		return "", err
	}
	fnOp = closureCallTarget(g, fnOp, hofIRType)

	elemIRType, err := typeToIR(g.types, *call.ArgType.Elem)
	if err != nil {
		return "", err
	}
	resultElemType := call.HOFType.Results[0]
	resultElemIRType, err := typeToIR(g.types, resultElemType)
	if err != nil {
		return "", err
	}
	resultListIRType, err := typeToIR(g.types, ast.Type{Elem: &resultElemType})
	if err != nil {
		return "", err
	}

	lenOp := g.newTemp("^int")
	g.emit("\tCALL\t%s\t:\t?len\t%s\n", lenOp, listOp)
	result := g.newTemp(resultListIRType)
	g.emit("\tSLMAKE\t%s\t%s\t%s\n", result, resultListIRType, lenOp)

	idxOp := g.newTemp("^int")
	g.emit("\tSET\t%s\t0\n", idxOp)
	startLabel := g.newLabel()
	bodyLabel := g.newLabel()
	endLabel := g.newLabel()
	g.emit("\tLABEL\t#%s\n", startLabel)
	cmp := g.newTemp("^bool")
	g.emit("\tLT\t%s\t%s\t%s\n", cmp, idxOp, lenOp)
	g.emit("\tIF\t%s\t#%s\n", cmp, bodyLabel)
	g.emit("\tGOTO\t#%s\n", endLabel)
	g.emit("\tLABEL\t#%s\n", bodyLabel)
	elemOp := g.newTemp(elemIRType)
	g.emit("\tAGET\t%s\t%s\t%s\n", elemOp, listOp, idxOp)
	mappedOp := g.newTemp(resultElemIRType)
	emitCall(g, []string{mappedOp}, fnOp, []string{elemOp})
	g.emit("\tASET\t%s\t%s\t%s\n", result, idxOp, mappedOp)
	g.emit("\tADD\t%s\t%s\t1\n", idxOp, idxOp)
	g.emit("\tGOTO\t#%s\n", startLabel)
	g.emit("\tLABEL\t#%s\n", endLabel)
	return result, nil
}

// genReduceCall compiles reduce(list, initial, f) (cascade_spec.md §8.4,
// §13) as a running accumulator, updated in place by calling f once per
// element (folding left to right, matching "initialから始めてfを畳み込ん
// だ結果を返す").
func genReduceCall(g *funcGen, call *ast.CallExpr) (string, error) {
	listOp, err := genValue(g, call.Args[0])
	if err != nil {
		return "", err
	}
	initOp, err := genValue(g, call.Args[1])
	if err != nil {
		return "", err
	}
	fnOp, err := genValue(g, call.Args[2])
	if err != nil {
		return "", err
	}
	hofIRType, err := typeToIR(g.types, ast.Type{Func: call.HOFType, Nullable: true})
	if err != nil {
		return "", err
	}
	fnOp = closureCallTarget(g, fnOp, hofIRType)

	elemIRType, err := typeToIR(g.types, *call.ArgType.Elem)
	if err != nil {
		return "", err
	}
	accType := call.HOFType.Results[0]
	accIRType, err := typeToIR(g.types, accType)
	if err != nil {
		return "", err
	}

	acc := g.newTemp(accIRType)
	g.emit("\tSET\t%s\t%s\n", acc, initOp)

	lenOp := g.newTemp("^int")
	g.emit("\tCALL\t%s\t:\t?len\t%s\n", lenOp, listOp)
	idxOp := g.newTemp("^int")
	g.emit("\tSET\t%s\t0\n", idxOp)
	startLabel := g.newLabel()
	bodyLabel := g.newLabel()
	endLabel := g.newLabel()
	g.emit("\tLABEL\t#%s\n", startLabel)
	cmp := g.newTemp("^bool")
	g.emit("\tLT\t%s\t%s\t%s\n", cmp, idxOp, lenOp)
	g.emit("\tIF\t%s\t#%s\n", cmp, bodyLabel)
	g.emit("\tGOTO\t#%s\n", endLabel)
	g.emit("\tLABEL\t#%s\n", bodyLabel)
	elemOp := g.newTemp(elemIRType)
	g.emit("\tAGET\t%s\t%s\t%s\n", elemOp, listOp, idxOp)
	emitCall(g, []string{acc}, fnOp, []string{acc, elemOp})
	g.emit("\tADD\t%s\t%s\t1\n", idxOp, idxOp)
	g.emit("\tGOTO\t#%s\n", startLabel)
	g.emit("\tLABEL\t#%s\n", endLabel)
	return acc, nil
}
