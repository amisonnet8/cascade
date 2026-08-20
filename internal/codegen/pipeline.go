// pipeline.go compiles cascade_spec.md §9.1/§9.2 (pipeline basics):
// source/stage/sink declarations, chan<T> parameters, send(), `for v in
// channel`, and a `|>`-chained pipeline statement ending in a sink — see
// codegen.go's package doc.
package codegen

import (
	"fmt"
	"strings"

	"github.com/amisonnet8/cascade/internal/ast"
)

// stageInfo is codegen's own copy of one source/stage/sink's channel
// element type(s) (cascade_spec.md §9.1) — kept independent from sema's
// identical stageSig (see CLAUDE.md's "意味検証の責任分担"/
// seed_implementation_notes.md §2's package-independence principle),
// built directly from the AST by buildStageInfo rather than consulting
// sema. Used by genPipelineStmt to know each inter-stage channel's
// element type when emitting its CHMAKE.
type stageInfo struct {
	Kind       ast.StageKind
	InputElem  ast.Type
	OutputElem ast.Type
}

// buildStageInfo extracts sd's channel element type(s) positionally from
// its parameter list (cascade_spec.md §9.1): Params[0] is the output
// channel for a source, or the input channel for a stage/sink;
// Params[1] (stage only) is the output channel. Trusts sema.Check already
// validated every parameter's shape (see CLAUDE.md's "意味検証の責任分担")
// — codegen never re-validates it here.
func buildStageInfo(sd *ast.StageDecl) stageInfo {
	info := stageInfo{Kind: sd.Kind}
	switch sd.Kind {
	case ast.SourceStage:
		info.OutputElem = *sd.Params[0].Type.Chan
	case ast.MiddleStage:
		info.InputElem = *sd.Params[0].Type.Chan
		info.OutputElem = *sd.Params[1].Type.Chan
	case ast.SinkStage:
		info.InputElem = *sd.Params[0].Type.Chan
	}
	return info
}

// genStageDecl compiles one source/stage/sink declaration (cascade_spec.md
// §9.1) into a plain top-level FUNC with no results — every parameter is
// a channel type (sema's validateChanParamType already guarantees this),
// which is never nullable (see ast.Type's doc for Chan), so unlike
// genFuncDecl there is no isset-slot expansion to do for any parameter.
//
// A source or stage additionally gets an automatic `DEFER ?close
// <output>` as the very first thing in its body, so its output channel
// closes whenever the function returns by any path (falling off the end,
// or an early `return`) — this is the mechanism genForInChannelStmt's
// downstream `for v in input` loops rely on to detect "no more data" and
// exit, and the mechanism genPipelineStmt's synchronous final CALL to
// the sink relies on to eventually return at all. This resolves
// CLAUDE.md's "オープンな設計課題" DEFER candidate — see its "確定した設計
// 判断" for Step 12.
func genStageDecl(sd *ast.StageDecl, types *typeRegistry, structs map[string]*ast.StructDecl, sigs map[string]funcSig, methods map[string]map[string]funcSig, stages map[string]stageInfo) (string, error) {
	g := &funcGen{scope: newScope(nil), types: types, structs: structs, sigs: sigs, methods: methods, stages: stages}

	var paramIRTypes []string
	for i, p := range sd.Params {
		irType, err := typeToIR(types, p.Type)
		if err != nil {
			return "", err
		}
		paramIRTypes = append(paramIRTypes, irType)
		g.scope.declare(p.Name, varRef{Type: p.Type, ValOp: fmt.Sprintf("$%d", i+1)})
	}

	outputIdx := 0
	switch sd.Kind {
	case ast.SourceStage:
		outputIdx = 1
	case ast.MiddleStage:
		outputIdx = 2
	}
	if outputIdx != 0 {
		g.emit("\tDEFER\t?close\t$%d\n", outputIdx)
	}

	for _, stmt := range sd.Body {
		if err := genStmt(g, stmt); err != nil {
			return "", err
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "FUNC\t!%s", sd.Name)
	for _, t := range paramIRTypes {
		b.WriteString("\t")
		b.WriteString(t)
	}
	b.WriteString("\t:\n")
	for _, d := range g.decls {
		fmt.Fprintf(&b, "\tVAR\t%s\t%s\n", d.Op, d.IRType)
	}
	b.WriteString(g.b.String())
	b.WriteString("ENDFUNC\n")
	return b.String(), nil
}

// genSendCall compiles `send(output, value)` (cascade_spec.md §9.1,
// §13) directly to CHSEND — send is a statement-only builtin (see
// parser's parseSendStmt), so this is only ever reached from genExprStmt.
func genSendCall(g *funcGen, call *ast.CallExpr) error {
	ch, err := genValue(g, call.Args[0])
	if err != nil {
		return err
	}
	v, err := genValue(g, call.Args[1])
	if err != nil {
		return err
	}
	g.emit("\tCHSEND\t%s\t%s\n", ch, v)
	return nil
}

// genForInChannelStmt compiles `for v in ch { ... }` over a channel
// (cascade_spec.md §7, §9.1): CHRECV's comma-ok form doubles as both the
// loop condition (false once ch is closed and drained — see
// genStageDecl's automatic DEFER ?close) and each iteration's value
// fetch, in one instruction. Unlike the list/map for-in forms
// (genForInStmt/genForInMapStmt), there is no separate index variable or
// increment step at all: `continue` simply re-issues the next CHRECV,
// the same shape genWhileStmt's own continue target already uses.
func genForInChannelStmt(g *funcGen, stmt *ast.ForInStmt) error {
	chOp, err := genValue(g, stmt.List)
	if err != nil {
		return err
	}
	elemIRType, err := scalarTypeToIR(stmt.ElemType)
	if err != nil {
		return err
	}

	startLabel := g.newLabel()
	bodyLabel := g.newLabel()
	endLabel := g.newLabel()

	outer := g.scope
	g.scope = newScope(outer)
	loopRef := varRef{Type: stmt.ElemType, ValOp: "%" + g.freshName(stmt.VarName)}
	g.declareVar(loopRef.ValOp, elemIRType)
	g.scope.declare(stmt.VarName, loopRef)
	okOp := g.newTemp("^bool")

	g.emit("\tLABEL\t#%s\n", startLabel)
	g.emit("\tCHRECV\t%s\t%s\t%s\n", loopRef.ValOp, okOp, chOp)
	g.emit("\tIF\t%s\t#%s\n", okOp, bodyLabel)
	g.emit("\tGOTO\t#%s\n", endLabel)
	g.emit("\tLABEL\t#%s\n", bodyLabel)

	g.pushBreak(endLabel)
	g.pushContinue(startLabel)
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

	g.emit("\tGOTO\t#%s\n", startLabel)
	g.emit("\tLABEL\t#%s\n", endLabel)
	return nil
}

// genPipelineStmt compiles a `|>`-chained pipeline statement
// (cascade_spec.md §9.2): a fresh CHMAKE'd channel between each adjacent
// pair, every stage but the last SPAWNed as its own goroutine, and the
// last (always a sink — sema's checkPipelineStmt guarantees this; collect,
// §9.3, is Step 13) called synchronously so the statement as a whole
// blocks until the entire pipeline has drained. This is what makes the
// statement's own control flow wait for completion: the sink's `for v in
// input` loop only returns once every upstream stage has finished and
// closed its output channel in turn (see genStageDecl's automatic DEFER
// ?close), so the final, unspawned CALL naturally doesn't return until
// then either.
func genPipelineStmt(g *funcGen, stmt *ast.PipelineStmt) error {
	n := len(stmt.Stages)
	chanOps := make([]string, n-1)
	for i := 0; i < n-1; i++ {
		elem := g.stages[stmt.Stages[i].Name].OutputElem
		chanIRType, err := typeToIR(g.types, ast.Type{Chan: &elem})
		if err != nil {
			return err
		}
		chanOps[i] = g.newTemp(chanIRType)
		g.emit("\tCHMAKE\t%s\t%s\t0\n", chanOps[i], chanIRType)
	}

	g.emit("\tSPAWN\t!%s\t%s\n", stmt.Stages[0].Name, chanOps[0])
	for i := 1; i < n-1; i++ {
		g.emit("\tSPAWN\t!%s\t%s\t%s\n", stmt.Stages[i].Name, chanOps[i-1], chanOps[i])
	}
	g.emit("\tCALL\t:\t!%s\t%s\n", stmt.Stages[n-1].Name, chanOps[n-2])
	return nil
}
