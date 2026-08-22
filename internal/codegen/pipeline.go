// pipeline.go compiles cascade_spec.md §9 (pipelines): source/stage/sink
// declarations, chan<T> parameters, send()/abort(), `for v in channel`, a
// `|>`-chained pipeline statement ending in a sink (§9.2), the
// value-producing form ending in `collect` (§9.3), and `merge` (§9.5) —
// see codegen.go's package doc.
package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/amisonnet8/cascade/internal/ast"
)

// stageInfo is codegen's own copy of one source/stage/sink's channel
// element type(s) (cascade_spec.md §9.1) — kept independent from sema's
// identical stageSig (see CLAUDE.md's "意味検証の責任分担"/
// seed_implementation_notes.md §2's package-independence principle),
// built directly from the AST by buildStageInfo rather than consulting
// sema. Used by genPipelineStmt/genPipelineCollect to know each
// inter-stage channel's element type when emitting its CHMAKE.
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

// boolChanIRType returns the AMIVM-IR deftype token for chan<bool> —
// every source/stage/sink's two hidden trailing parameters (see
// genStageDecl) and every abort/guard channel genPipelineStmt/
// genPipelineCollect/genMergeCall create share this one shape.
func boolChanIRType(types *typeRegistry) (string, error) {
	return typeToIR(types, ast.Type{Chan: &ast.Type{Name: "bool"}})
}

// genStageDecl compiles one source/stage/sink declaration (cascade_spec.md
// §9.1) into a plain top-level FUNC with no results. Every user-declared
// parameter is a channel type (sema's validateChanParamType already
// guarantees this), which is never nullable (see ast.Type's doc for
// Chan), so unlike genFuncDecl there is no isset-slot expansion to do for
// any of them.
//
// Two more parameters are appended after the user's own — invisible from
// the Cascade source, purely a codegen-internal mechanism for §9.4's
// abort(): an abort-broadcast channel (chan<bool>, only ever closed,
// never sent to — see genForInChannelStmt/genSendCall, which select
// against it, and genAbortCall, which closes it) and a close-once guard
// channel (chan<bool>, buffer 1 — see genAbortCall's doc for why closing
// the broadcast channel safely exactly once, even under concurrent
// abort() calls from different stages, needs this second channel).
// EVERY source/stage/sink gets both, whether or not that particular one
// calls abort() itself, since every stage must still be able to react to
// an abort() called from a DIFFERENT stage, and every SPAWN/CALL site
// invoking any of them (genPipelineStmt, genPipelineCollect,
// genMergeCall) must supply both uniformly to match this fixed shape.
//
// A source or stage additionally gets an automatic `DEFER ?close
// <output>` as the very first thing in its body, so its output channel
// closes whenever the function returns by any path (falling off the end,
// an early `return`, or abort()'s own implicit return) — this is the
// mechanism genForInChannelStmt's downstream `for v in input` loops rely
// on to detect "no more data" and exit, and the mechanism
// genPipelineStmt's synchronous final CALL to the sink relies on to
// eventually return at all.
func genStageDecl(sd *ast.StageDecl, types *typeRegistry, structs map[string]*ast.StructDecl, sigs map[string]funcSig, methods map[string]map[string]funcSig, stages map[string]stageInfo, packageScope *scope) (string, error) {
	g := &funcGen{scope: newScope(packageScope), types: types, structs: structs, sigs: sigs, methods: methods, stages: stages}

	var paramIRTypes []string
	for i, p := range sd.Params {
		irType, err := typeToIR(types, p.Type)
		if err != nil {
			return "", err
		}
		paramIRTypes = append(paramIRTypes, irType)
		g.scope.declare(p.Name, varRef{Type: p.Type, ValOp: fmt.Sprintf("$%d", i+1)})
	}

	boolChan, err := boolChanIRType(types)
	if err != nil {
		return "", err
	}
	paramIRTypes = append(paramIRTypes, boolChan, boolChan)
	g.abortChanOp = fmt.Sprintf("$%d", len(sd.Params)+1)
	g.guardChanOp = fmt.Sprintf("$%d", len(sd.Params)+2)

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
// §13). Outside a stage body (g.abortChanOp == "" — e.g. sending into a
// merge() result from an ordinary function) this is a plain CHSEND.
// Inside one, it becomes a non-blocking-on-abort SEL: either the send
// succeeds normally, or abort() (called from anywhere in the pipeline)
// fires first, in which case this stage returns immediately rather than
// blocking forever trying to send into a channel nothing may ever read
// from again — the same reasoning genForInChannelStmt's own SEL applies
// to receiving.
func genSendCall(g *funcGen, call *ast.CallExpr) error {
	ch, err := genValue(g, call.Args[0])
	if err != nil {
		return err
	}
	v, err := genValue(g, call.Args[1])
	if err != nil {
		return err
	}
	if g.abortChanOp == "" {
		g.emit("\tCHSEND\t%s\t%s\n", ch, v)
		return nil
	}
	g.emit("\tSEL\n")
	g.emit("\tCASESEND\t%s\t%s\n", ch, v)
	g.emit("\tCASERECV\t_\t%s\n", g.abortChanOp)
	g.emit("\tRET\n")
	g.emit("\tENDSEL\n")
	return nil
}

// genAbortCall compiles `abort(message)` (cascade_spec.md §9.4): prints
// the message (there's no spec-defined destination for it — see
// CLAUDE.md's "確定した設計判断" for the call made here), then makes
// exactly one caller — the first, across however many stages call
// abort() concurrently — actually close the shared abort-broadcast
// channel, via a non-blocking SEL-guarded send into g.guardChanOp (buffer
// 1): whoever wins that race proceeds to `close`; everyone else silently
// no-ops. Closing (rather than sending) the broadcast channel itself is
// what lets every stage's own SEL (genForInChannelStmt/genSendCall)
// observe it — a plain send is consumed by exactly one receiver, which
// can't correctly notify multiple concurrently-blocked stages at once,
// but every receive from an already-closed channel immediately succeeds,
// giving true broadcast semantics for free. Finally, and unconditionally
// (regardless of who won the race), returns immediately — cascade_spec.md
// §9.4's own example (`if n < 0 { abort(...) }` followed by an
// unconditional `send(output, n)`) only makes sense if calling abort()
// itself stops the enclosing stage right there, since the user's own
// source has no explicit `return` after it.
func genAbortCall(g *funcGen, call *ast.CallExpr) error {
	msgOp, err := genValue(g, call.Args[0])
	if err != nil {
		return err
	}
	prefixed := g.newTemp("^string")
	g.emit("\tCONCAT\t%s\t%s\t%s\n", prefixed, strconv.Quote("abort: "), msgOp)
	g.emit("\tCALL\t:\t?fmt.Println\t%s\n", prefixed)

	g.emit("\tSEL\n")
	g.emit("\tCASESEND\t%s\ttrue\n", g.guardChanOp)
	g.emit("\tCALL\t:\t?close\t%s\n", g.abortChanOp)
	g.emit("\tDEFAULT\n")
	g.emit("\tENDSEL\n")
	g.emit("\tRET\n")
	return nil
}

// genForInChannelStmt compiles `for v in ch { ... }` over a channel
// (cascade_spec.md §7, §9.1) as a LOOP. Outside a stage body
// (g.abortChanOp == "") this is the plain Step 12 form: CHRECV's
// comma-ok result doubles as both the loop condition (false once ch is
// closed and drained) and each iteration's value fetch. Inside a stage
// body, the receive becomes a SEL between that same CHRECV and a
// CASERECV on the abort-broadcast channel — if abort() fires (anywhere
// in the pipeline) before ch's next value arrives, this stage returns
// immediately instead of continuing to wait; the abort case's own RET
// sits inside its CASERECV body, so it only fires when abort actually
// wins the race, and both SEL branches otherwise fall through to the
// same ok-check below. Either way, there is no separate index variable
// or increment step at all — `continue` is a native CONTINUE, same as
// genWhileStmt.
func genForInChannelStmt(g *funcGen, stmt *ast.ForInStmt) error {
	chOp, err := genValue(g, stmt.List)
	if err != nil {
		return err
	}
	elemIRType, err := scalarTypeToIR(stmt.ElemType)
	if err != nil {
		return err
	}

	outer := g.scope
	g.scope = newScope(outer)
	loopRef := varRef{Type: stmt.ElemType, ValOp: "%" + g.freshName(stmt.VarName)}
	g.declareVar(loopRef.ValOp, elemIRType)
	g.scope.declare(stmt.VarName, loopRef)
	okOp := g.newTemp("^bool")

	g.emit("\tLOOP\n")
	if g.abortChanOp == "" {
		g.emit("\tCHRECV\t%s\t%s\t%s\n", loopRef.ValOp, okOp, chOp)
	} else {
		g.emit("\tSEL\n")
		g.emit("\tCASERECV\t%s\t%s\t%s\n", loopRef.ValOp, okOp, chOp)
		g.emit("\tCASERECV\t_\t%s\n", g.abortChanOp)
		g.emit("\tRET\n")
		g.emit("\tENDSEL\n")
	}
	g.emitBreakUnless(okOp)

	g.pushBreak(breakTarget{})
	g.pushContinue(continueTarget{})
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

	g.emit("\tENDLOOP\n")
	return nil
}

// genFreshAbortAndGuardChans creates one pipeline invocation's own
// abort-broadcast channel (buffer 0 — never sent to, only closed, see
// genAbortCall) and close-once guard channel (buffer 1 — see
// genAbortCall's doc for why the buffer matters there specifically),
// shared by genPipelineStmt, genPipelineCollect, and genMergeCall, each
// of which creates a fresh, independent pair per invocation (so an
// abort() inside one `|>` chain or merge() never reaches across into an
// unrelated one — see CLAUDE.md's "確定した設計判断" for the scope this
// deliberately leaves out: abort() propagating across a merge()'s own
// two independently-spawned sources).
func genFreshAbortAndGuardChans(g *funcGen) (abortOp, guardOp string, err error) {
	boolChan, err := boolChanIRType(g.types)
	if err != nil {
		return "", "", err
	}
	abortOp = g.newTemp(boolChan)
	guardOp = g.newTemp(boolChan)
	g.emit("\tCHMAKE\t%s\t%s\t0\n", abortOp, boolChan)
	g.emit("\tCHMAKE\t%s\t%s\t1\n", guardOp, boolChan)
	return abortOp, guardOp, nil
}

// genPipelineStmt compiles a `|>`-chained pipeline statement
// (cascade_spec.md §9.2): a fresh CHMAKE'd channel between each adjacent
// pair, a fresh abort/guard channel pair for this invocation (§9.4), every
// stage but the last SPAWNed as its own goroutine (each given the
// abort/guard channels as its two trailing hidden arguments — see
// genStageDecl), and the last (always a sink — sema's checkPipelineStmt
// guarantees this; collect, §9.3, uses genPipelineCollect instead) called
// synchronously so the statement as a whole blocks until the entire
// pipeline has drained. This is what makes the statement's own control
// flow wait for completion: the sink's `for v in input` loop only returns
// once every upstream stage has finished (normally, or via abort) and
// closed its output channel in turn (see genStageDecl's automatic DEFER
// ?close), so the final, unspawned CALL naturally doesn't return until
// then either.
func genPipelineStmt(g *funcGen, expr *ast.PipelineExpr) error {
	n := len(expr.Stages)
	chanOps := make([]string, n-1)
	for i := 0; i < n-1; i++ {
		elem := g.stages[expr.Stages[i].Name].OutputElem
		chanIRType, err := typeToIR(g.types, ast.Type{Chan: &elem})
		if err != nil {
			return err
		}
		chanOps[i] = g.newTemp(chanIRType)
		g.emit("\tCHMAKE\t%s\t%s\t0\n", chanOps[i], chanIRType)
	}

	abortChanOp, guardChanOp, err := genFreshAbortAndGuardChans(g)
	if err != nil {
		return err
	}

	g.emit("\tSPAWN\t!%s\t%s\t%s\t%s\n", expr.Stages[0].Name, chanOps[0], abortChanOp, guardChanOp)
	for i := 1; i < n-1; i++ {
		g.emit("\tSPAWN\t!%s\t%s\t%s\t%s\t%s\n", expr.Stages[i].Name, chanOps[i-1], chanOps[i], abortChanOp, guardChanOp)
	}
	g.emit("\tCALL\t:\t!%s\t%s\t%s\t%s\n", expr.Stages[n-1].Name, chanOps[n-2], abortChanOp, guardChanOp)
	return nil
}

// genCollectorFunc synthesizes one collect (cascade_spec.md §9.3)
// collector goroutine: it receives from its input channel (elemType,
// forwarded from the pipeline's last real stage) until closed, appending
// each value into a growing []elemType via Go's own raw append() —
// unlike Cascade's user-facing append() builtin (see list.go's
// genAppendCall), which always reallocates to avoid aliasing between
// independent Cascade-level list *values*, this accumulator is a
// purely-local, single-owner temporary never exposed as a Cascade value
// until the one final CHSEND, so there is no aliasing risk to guard
// against and the simpler/faster raw append is safe. On normal
// completion (input closed) it sends the finished list out over its
// result channel and returns; on abort() firing first (its own SEL's
// second case), it returns immediately without ever sending — the result
// channel's own deferred close then signals "no result" (ok=false) to
// genPipelineCollect's single comma-ok CHRECV, which is exactly what
// collect's own nullable result needs (§9.3: "abortされるとnoneになる").
func genCollectorFunc(types *typeRegistry, elemType, listType ast.Type) (funcName, funcIR string, err error) {
	inputChanIRType, err := typeToIR(types, ast.Type{Chan: &elemType})
	if err != nil {
		return "", "", err
	}
	resultChanIRType, err := typeToIR(types, ast.Type{Chan: &listType})
	if err != nil {
		return "", "", err
	}
	abortChanIRType, err := boolChanIRType(types)
	if err != nil {
		return "", "", err
	}
	elemIRType, err := typeToIR(types, elemType)
	if err != nil {
		return "", "", err
	}
	listIRType, err := typeToIR(types, listType)
	if err != nil {
		return "", "", err
	}

	name := types.newCollectorName()
	var b strings.Builder
	fmt.Fprintf(&b, "FUNC\t!%s\t%s\t%s\t%s\t:\n", name, inputChanIRType, resultChanIRType, abortChanIRType)
	fmt.Fprintf(&b, "\tVAR\t%%result\t%s\n", listIRType)
	fmt.Fprintf(&b, "\tVAR\t%%v\t%s\n", elemIRType)
	b.WriteString("\tVAR\t%ok\t^bool\n")
	fmt.Fprintf(&b, "\tSLMAKE\t%%result\t%s\t0\n", listIRType)
	b.WriteString("\tDEFER\t?close\t$2\n")
	b.WriteString("\tLOOP\n")
	b.WriteString("\tSEL\n")
	b.WriteString("\tCASERECV\t%v\t%ok\t$1\n")
	b.WriteString("\tIF\t%ok\n")
	b.WriteString("\tCALL\t%result\t:\t?append\t%result\t%v\n")
	b.WriteString("\tELSE\n")
	b.WriteString("\tCHSEND\t$2\t%result\n")
	b.WriteString("\tRET\n")
	b.WriteString("\tENDIF\n")
	b.WriteString("\tCASERECV\t_\t$3\n")
	b.WriteString("\tRET\n")
	b.WriteString("\tENDSEL\n")
	b.WriteString("\tENDLOOP\n")
	b.WriteString("ENDFUNC\n")
	return name, b.String(), nil
}

// genPipelineCollect compiles a `|>`-chained pipeline ending in `collect`
// (cascade_spec.md §9.3) into its own (value, isset) pair for
// genNullableOperands (the sole caller) — isset=false meaning the
// pipeline was aborted, matching every other nullable-result mechanism
// this compiler already has. Spawns every real stage exactly like
// genPipelineStmt, then additionally spawns a freshly synthesized
// collector goroutine (genCollectorFunc) fed from the last real stage's
// output, and does a single comma-ok CHRECV from the collector's own
// dedicated result channel to get the final (value, isset) pair.
func genPipelineCollect(g *funcGen, expr *ast.PipelineExpr) (val, isset string, err error) {
	n := len(expr.Stages)
	chanOps := make([]string, n-1)
	for i := 0; i < n-1; i++ {
		elem := g.stages[expr.Stages[i].Name].OutputElem
		chanIRType, terr := typeToIR(g.types, ast.Type{Chan: &elem})
		if terr != nil {
			return "", "", terr
		}
		chanOps[i] = g.newTemp(chanIRType)
		g.emit("\tCHMAKE\t%s\t%s\t0\n", chanOps[i], chanIRType)
	}

	abortChanOp, guardChanOp, err := genFreshAbortAndGuardChans(g)
	if err != nil {
		return "", "", err
	}

	g.emit("\tSPAWN\t!%s\t%s\t%s\t%s\n", expr.Stages[0].Name, chanOps[0], abortChanOp, guardChanOp)
	for i := 1; i < n-1; i++ {
		g.emit("\tSPAWN\t!%s\t%s\t%s\t%s\t%s\n", expr.Stages[i].Name, chanOps[i-1], chanOps[i], abortChanOp, guardChanOp)
	}

	elemType := g.stages[expr.Stages[n-2].Name].OutputElem
	listType := ast.Type{Elem: &elemType}
	listIRType, err := typeToIR(g.types, listType)
	if err != nil {
		return "", "", err
	}
	resultChanIRType, err := typeToIR(g.types, ast.Type{Chan: &listType})
	if err != nil {
		return "", "", err
	}
	resultChanOp := g.newTemp(resultChanIRType)
	g.emit("\tCHMAKE\t%s\t%s\t0\n", resultChanOp, resultChanIRType)

	collectorName, collectorIR, err := genCollectorFunc(g.types, elemType, listType)
	if err != nil {
		return "", "", err
	}
	g.types.addCollectorFunc(collectorIR)

	g.emit("\tSPAWN\t!%s\t%s\t%s\t%s\n", collectorName, chanOps[n-2], resultChanOp, abortChanOp)

	valTmp := g.newTemp(listIRType)
	okTmp := g.newTemp("^bool")
	g.emit("\tCHRECV\t%s\t%s\t%s\n", valTmp, okTmp, resultChanOp)
	return valTmp, okTmp, nil
}

// genMergeFunc synthesizes one merge (cascade_spec.md §9.5) fan-in
// goroutine: it forwards whichever of its two input channels has a value
// ready first (a blocking SEL between both, no DEFAULT — see
// amivm/cmd/amivm/parse_block.go, which confirms DEFAULT is optional, so
// this genuinely blocks rather than polling), using the well-known Go
// idiom of setting a channel variable to nil once it's been observed
// closed — a nil channel's SEL case can never become ready, so that
// input is effectively (and permanently) dropped from the select without
// needing to restructure the SEL itself — and closes its own output only
// once *both* inputs have gone nil this way (checked before each select,
// to avoid ever entering a SEL where neither case could possibly fire).
func genMergeFunc(types *typeRegistry, chanIRType, elemIRType string) (funcName, funcIR string, err error) {
	name := types.newMergeName()
	var b strings.Builder
	fmt.Fprintf(&b, "FUNC\t!%s\t%s\t%s\t%s\t:\n", name, chanIRType, chanIRType, chanIRType)
	fmt.Fprintf(&b, "\tVAR\t%%chA\t%s\n", chanIRType)
	fmt.Fprintf(&b, "\tVAR\t%%chB\t%s\n", chanIRType)
	fmt.Fprintf(&b, "\tVAR\t%%v\t%s\n", elemIRType)
	b.WriteString("\tVAR\t%ok\t^bool\n")
	b.WriteString("\tVAR\t%doneA\t^bool\n")
	b.WriteString("\tVAR\t%doneB\t^bool\n")
	b.WriteString("\tVAR\t%bothDone\t^bool\n")
	b.WriteString("\tSET\t%chA\t$1\n")
	b.WriteString("\tSET\t%chB\t$2\n")
	b.WriteString("\tDEFER\t?close\t$3\n")
	b.WriteString("\tLOOP\n")
	b.WriteString("\tEQ\t%doneA\t%chA\tnil\n")
	b.WriteString("\tEQ\t%doneB\t%chB\tnil\n")
	b.WriteString("\tAND\t%bothDone\t%doneA\t%doneB\n")
	b.WriteString("\tIF\t%bothDone\n")
	b.WriteString("\tRET\n")
	b.WriteString("\tENDIF\n")
	b.WriteString("\tSEL\n")
	b.WriteString("\tCASERECV\t%v\t%ok\t%chA\n")
	b.WriteString("\tIF\t%ok\n")
	b.WriteString("\tCHSEND\t$3\t%v\n")
	b.WriteString("\tELSE\n")
	b.WriteString("\tSET\t%chA\tnil\n")
	b.WriteString("\tENDIF\n")
	b.WriteString("\tCASERECV\t%v\t%ok\t%chB\n")
	b.WriteString("\tIF\t%ok\n")
	b.WriteString("\tCHSEND\t$3\t%v\n")
	b.WriteString("\tELSE\n")
	b.WriteString("\tSET\t%chB\tnil\n")
	b.WriteString("\tENDIF\n")
	b.WriteString("\tENDSEL\n")
	b.WriteString("\tENDLOOP\n")
	b.WriteString("ENDFUNC\n")
	return name, b.String(), nil
}

// genMergeCall compiles `merge(sourceA, sourceB)` (cascade_spec.md §9.5)
// into a fresh chan<T> value: sema's checkCallExprValue guarantees
// call.Args[0]/[1] are each a bare identifier naming a declared source
// (never evaluated as ordinary expressions — see its own doc for why),
// so this reads their names directly rather than calling genValue. Both
// sources are spawned into their own fresh channel, sharing one fresh
// abort/guard pair scoped only to this merge call (see
// genFreshAbortAndGuardChans's doc — deliberately not shared with any
// enclosing pipeline), and a freshly synthesized fan-in goroutine
// (genMergeFunc) is spawned to forward both into the returned output
// channel.
func genMergeCall(g *funcGen, call *ast.CallExpr) (string, error) {
	nameA := call.Args[0].(*ast.Ident).Name
	nameB := call.Args[1].(*ast.Ident).Name
	elemType := call.ArgType

	chanIRType, err := typeToIR(g.types, ast.Type{Chan: &elemType})
	if err != nil {
		return "", err
	}

	chanA := g.newTemp(chanIRType)
	chanB := g.newTemp(chanIRType)
	g.emit("\tCHMAKE\t%s\t%s\t0\n", chanA, chanIRType)
	g.emit("\tCHMAKE\t%s\t%s\t0\n", chanB, chanIRType)

	abortChanOp, guardChanOp, err := genFreshAbortAndGuardChans(g)
	if err != nil {
		return "", err
	}
	g.emit("\tSPAWN\t!%s\t%s\t%s\t%s\n", nameA, chanA, abortChanOp, guardChanOp)
	g.emit("\tSPAWN\t!%s\t%s\t%s\t%s\n", nameB, chanB, abortChanOp, guardChanOp)

	outputChanOp := g.newTemp(chanIRType)
	g.emit("\tCHMAKE\t%s\t%s\t0\n", outputChanOp, chanIRType)

	elemIRType, err := typeToIR(g.types, elemType)
	if err != nil {
		return "", err
	}
	mergeName, mergeIR, err := genMergeFunc(g.types, chanIRType, elemIRType)
	if err != nil {
		return "", err
	}
	g.types.addMergeFunc(mergeIR)

	g.emit("\tSPAWN\t!%s\t%s\t%s\t%s\n", mergeName, chanA, chanB, outputChanOp)
	return outputChanOp, nil
}
