// closure.go compiles cascade_spec.md §8.3 (closures) and §8.4
// (filter/map/reduce) — see codegen.go's package doc.
package codegen

import (
	"fmt"
	"strings"

	"github.com/amisonnet8/cascade/internal/ast"
)

// genClosureLit compiles a closure literal (cascade_spec.md §8.3) into a
// fresh temp — the general case, used when a closure literal appears as
// a subexpression with no single target variable to write into directly
// (e.g. a call argument). See genClosureLitInto for the target-aware
// form genInit uses instead for `let f = func(...) {...}` (or a plain
// reassignment `f = func(...) {...}`), which writes straight into the
// destination variable — now that amivm's CLOS target category accepts
// a pre-declared $N/%xxx/@xxx directly ("shallow", amivm_spec.md §4.17/
// §5) rather than only a fresh local, this skips the redundant temp+SET
// this function still needs for the general case.
func genClosureLit(g *funcGen, lit *ast.ClosureLit) (string, error) {
	irType, err := typeToIR(g.types, lit.ResolvedType)
	if err != nil {
		return "", err
	}
	tmp := g.newTemp(irType)
	if err := genClosureLitInto(g, tmp, lit); err != nil {
		return "", err
	}
	return tmp, nil
}

// genClosureLitInto compiles a closure literal directly into target — a
// token already valid in amivm's CLOS "single1" category ($N/&N/&L-N/
// %xxx/@xxx; a %xxx or @xxx target must already be VAR/GVAR-declared,
// which is always true by the time this runs: a %xxx local is hoisted
// before its initializer compiles, and @xxx globals are GVAR-declared at
// the top level before cascade_init ever runs) — via a nested
// CLOS...ENDCLOS block emitted inline within the enclosing function's
// own body.
//
// The closure body compiles through its own, completely independent
// funcGen — fresh decls/seq/labelSeq/breakStack/continueStack, since a
// CLOS is its own nested Go func literal with its own local scope and
// its own goto/VAR-hoisting problem (seed_implementation_notes.md §1) —
// except its *scope* is a child of the enclosing scope at the point the
// literal appears, exactly mirroring real Go closure capture: a captured
// identifier resolves to the *same* underlying token used outside, with
// no copying or special capture instruction needed (see
// amivm/test_ir/15_closure.ir, where `%count`/`%base` are referenced
// directly, unchanged, inside a CLOS body).
//
// A closure parameter is a `&depth-N` slot exactly like a FUNC
// parameter's `$N` (including the same two-slot nullable expansion — see
// needsIssetSlot), where depth is this closure's own nesting depth
// (inner.closureDepth = g.closureDepth+1, FUNC-direct is 1 —
// amivm_spec.md §4.17/§10). amivm also accepts the bare `&N` shorthand
// (meaning "the closure I'm currently compiling"), but this always emits
// the fully-qualified `&depth-N` form instead, for every parameter at
// every depth, even a depth-1 closure that could use the short form —
// because a closure literal may now itself contain another (cascade_spec
// .md §8.3, amivm's CLOS nesting support), and a varRef's ValOp is a
// single fixed string decided once at declaration time and reused
// verbatim by every future scope lookup, however many closure bodies
// deep that lookup happens to come from (the whole mechanism this
// function's capture story above relies on). A bare `&N` stored at
// declaration time would resolve correctly only for a reference from
// that *same* closure body — a deeper nested closure capturing it would
// misread it as its own Nth parameter instead. The fully-qualified form
// has no such blind spot: it is unambiguous regardless of which closure
// body — this one or any depth beneath it — ends up reading it back out.
func genClosureLitInto(g *funcGen, target string, lit *ast.ClosureLit) error {
	depth := g.closureDepth + 1
	inner := &funcGen{scope: newScope(g.scope), types: g.types, structs: g.structs, sigs: g.sigs, methods: g.methods, closureDepth: depth}

	var paramIRTypes []string
	argIdx := 0
	for _, p := range lit.Params {
		pIRType, err := typeToIR(g.types, p.Type)
		if err != nil {
			return err
		}
		argIdx++
		ref := varRef{Type: p.Type, ValOp: fmt.Sprintf("&%d-%d", depth, argIdx)}
		paramIRTypes = append(paramIRTypes, pIRType)
		if needsIssetSlot(p.Type) {
			argIdx++
			ref.SetOp = fmt.Sprintf("&%d-%d", depth, argIdx)
			paramIRTypes = append(paramIRTypes, "^bool")
		}
		inner.scope.declare(p.Name, ref)
	}

	var resultIRTypes []string
	for _, r := range lit.Results {
		rIRType, err := typeToIR(g.types, r)
		if err != nil {
			return err
		}
		resultIRTypes = append(resultIRTypes, rIRType)
		if needsIssetSlot(r) {
			resultIRTypes = append(resultIRTypes, "^bool")
		}
	}
	inner.results = lit.Results

	for _, stmt := range lit.Body {
		if err := genStmt(inner, stmt); err != nil {
			return err
		}
	}

	var head strings.Builder
	fmt.Fprintf(&head, "\tCLOS\t%s", target)
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
	return nil
}

// closureCallTarget returns an operand valid in amivm's callname category
// (`!xxx`/`!main`/`?xxx`/`?xxx.xxx`/`%xxx`/`@xxx`/`$N`/`&N` — see
// amivm_spec.md §5) for calling the function value held in valOp.
// `%xxx`/`@xxx`/`$N`/`&N` tokens — every shape genValue can ever hand
// back for a closure-typed expression — are all valid as-is now, so the
// copy-to-temp fallback below is unreachable in practice; it stays only
// as a defensive catch-all (e.g. a bare `nil` literal, which is not a
// valid callname token on its own).
//
// This used to be a real, exercised fallback: an older amivm callname
// category accepted only `!xxx`/`!main`/`?xxx`/`?xxx.xxx`/`%xxx`, forcing
// a copy for `$N`/`&N` (a plain function/closure parameter directly
// holding a closure value, e.g. `func applyTwice(f: func(int): int, x:
// int): int { return f(f(x)) }` — discovered empirically running
// examples/09_closures.cas through amivm) and, later, for `@xxx` (a
// global closure variable) once `$N`/`&N` were added but `@xxx` still
// wasn't — see CLAUDE.md's "確定した設計判断" for both rounds.
func closureCallTarget(g *funcGen, valOp, irType string) string {
	if strings.HasPrefix(valOp, "%") || strings.HasPrefix(valOp, "@") || strings.HasPrefix(valOp, "$") || strings.HasPrefix(valOp, "&") {
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
	calleeName, sig, args, err := resolveCall(g, call)
	if err != nil {
		return "", err
	}
	refs, err := emitCallWithResults(g, calleeName, args, sig.Results)
	if err != nil {
		return "", err
	}
	return refs[0].ValOp, nil
}

// genClosureCallStmt compiles a call to a closure-valued local variable
// used as a bare statement, discarding every result — mirrors
// genUserFuncCallStmt/genMethodCallStmt.
func genClosureCallStmt(g *funcGen, call *ast.CallExpr) error {
	calleeName, _, args, err := resolveCall(g, call)
	if err != nil {
		return err
	}
	emitCall(g, nil, calleeName, args)
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
