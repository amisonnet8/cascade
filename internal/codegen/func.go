package codegen

import (
	"fmt"
	"strings"

	"github.com/amisonnet8/cascade/internal/ast"
)

// funcSig is one function's signature (cascade_spec.md §8.1): its
// parameter and result types. codegen keeps its own copy of this table
// (built once in Generate, shared via every funcGen's sigs field) rather
// than consulting sema — the same "each package keeps its own scope
// structure" independence seed_implementation_notes.md §2 recommends, so
// codegen never depends on sema internals.
type funcSig struct {
	Params  []ast.Type
	Results []ast.Type
}

// funcAmivmName is the amivm-level name fn's Cascade name is emitted
// under: mainInternalName for "main" (see codegen.go's package doc for
// why), the name itself for everything else.
func funcAmivmName(name string) string {
	if name == "main" {
		return mainInternalName
	}
	return name
}

// genFuncDecl compiles one function declaration (cascade_spec.md §8.1,
// §8.5), or, when Receiver is non-nil, a receiver function (§8.2) — see
// CLAUDE.md's "確定した設計判断" for why this is emitted as a plain
// top-level FUNC named StructName_Method rather than a Go method: the
// receiver is simply the function's first parameter, exactly like any
// other. Unlike an ordinary nullable parameter, the receiver never gets a
// second isset-flag slot — its declared type is always either a bare
// struct name (never nullable) or a pointer to one (always nullable via
// native nil, needing no flag at all — see needsIssetSlot).
//
// A nullable parameter (`x: T?`) expands into *two* consecutive Go
// parameter slots — the value, then a `^bool` isset flag — exactly
// mirroring how a nullable local variable is two separate hoisted VARs
// (scope.go's varRef). There is no other way for "is this none?" to
// cross a CALL boundary: a Cascade argument passes only one value per
// declared parameter type, so a nullable parameter's flag has to be its
// own, explicit second parameter, filled in by the caller (see
// genCallArgs) — it cannot be synthesized locally the way Seed
// synthesized `true` for its own (always-assigned) parameters. A
// nullable *result* type gets the identical two-slot expansion on the
// way out (cascade_spec.md §8.6's `(T, error?)` convention, and any
// other nullable result along with it — see needsIssetSlot and
// genReturnStmt's matching expansion).
func genFuncDecl(fn *ast.FuncDecl, types *typeRegistry, structs map[string]*ast.StructDecl, sigs map[string]funcSig, methods map[string]map[string]funcSig, stages map[string]stageInfo, packageScope *scope) (string, error) {
	g := &funcGen{scope: newScope(packageScope), types: types, structs: structs, sigs: sigs, methods: methods, stages: stages}

	var paramIRTypes []string
	argIdx := 0
	if fn.Receiver != nil {
		irType, err := typeToIR(types, fn.Receiver.Type)
		if err != nil {
			return "", err
		}
		argIdx++
		paramIRTypes = append(paramIRTypes, irType)
		g.scope.declare(fn.Receiver.Name, varRef{Type: fn.Receiver.Type, ValOp: fmt.Sprintf("$%d", argIdx)})
	}
	for _, p := range fn.Params {
		irType, err := typeToIR(types, p.Type)
		if err != nil {
			return "", err
		}
		argIdx++
		ref := varRef{Type: p.Type, ValOp: fmt.Sprintf("$%d", argIdx)}
		paramIRTypes = append(paramIRTypes, irType)
		if needsIssetSlot(p.Type) {
			argIdx++
			ref.SetOp = fmt.Sprintf("$%d", argIdx)
			paramIRTypes = append(paramIRTypes, "^bool")
		}
		g.scope.declare(p.Name, ref)
	}

	var resultIRTypes []string
	for _, r := range fn.Results {
		irType, err := typeToIR(types, r)
		if err != nil {
			return "", err
		}
		resultIRTypes = append(resultIRTypes, irType)
		if needsIssetSlot(r) {
			resultIRTypes = append(resultIRTypes, "^bool")
		}
	}
	g.results = fn.Results

	for _, stmt := range fn.Body {
		if err := genStmt(g, stmt); err != nil {
			return "", err
		}
	}

	amivmName := funcAmivmName(fn.Name)
	if fn.Receiver != nil {
		amivmName = receiverStructName(fn.Receiver.Type) + "_" + fn.Name
	}

	var b strings.Builder
	fmt.Fprintf(&b, "FUNC\t!%s", amivmName)
	for _, t := range paramIRTypes {
		b.WriteString("\t")
		b.WriteString(t)
	}
	b.WriteString("\t:")
	for _, t := range resultIRTypes {
		b.WriteString("\t")
		b.WriteString(t)
	}
	b.WriteString("\n")
	for _, d := range g.decls {
		fmt.Fprintf(&b, "\tVAR\t%s\t%s\n", d.Op, d.IRType)
	}
	b.WriteString(g.b.String())
	b.WriteString("ENDFUNC\n")
	return b.String(), nil
}

// genNullableOperands compiles expr — already validated by sema.Check as
// assignable into a target of type targetType, which is nullable — into
// its (value, isset) AMIVM-IR token pair: `none` becomes (zero value,
// "false"); a plain identifier that is itself a nullable variable passes
// its own ValOp/SetOp straight through (so a currently-none source stays
// none, rather than being coerced to "set" — see genInit's doc for the
// bug this fixes); a map key read (`m[k]`, always nullable — see
// ast.IndexExpr's exprType case in sema) uses MGET's comma-ok form
// directly, giving both operands from a single instruction rather than
// reading the value and then separately re-deriving whether the key
// existed; anything else (a literal or computed non-nullable value being
// widened into a nullable target) is definitely set, so its isset token
// is just the literal "true".
//
// This is shared by every place a value crosses into nullable-typed
// storage across a boundary genValue's single-token return can't
// express: local declaration/assignment (genInit) and function-call
// arguments (genCallArgs).
func genNullableOperands(g *funcGen, expr ast.Expr, targetType ast.Type) (val, isset string, err error) {
	if _, isNone := expr.(*ast.NoneLit); isNone {
		zero, err := genZeroValueToken(g, targetType)
		if err != nil {
			return "", "", err
		}
		return zero, "false", nil
	}
	if id, isIdent := expr.(*ast.Ident); isIdent {
		if ref, ok := g.scope.lookup(id.Name); ok && ref.SetOp != "" {
			return ref.ValOp, ref.SetOp, nil
		}
	}
	if pipe, isPipe := expr.(*ast.PipelineExpr); isPipe {
		return genPipelineCollect(g, pipe)
	}
	if idx, isIndex := expr.(*ast.IndexExpr); isIndex && idx.ResultType.Nullable {
		xOp, err := genValue(g, idx.X)
		if err != nil {
			return "", "", err
		}
		keyOp, err := genValue(g, idx.Index)
		if err != nil {
			return "", "", err
		}
		valIRType, err := typeToIR(g.types, idx.ResultType)
		if err != nil {
			return "", "", err
		}
		valTmp := g.newTemp(valIRType)
		okTmp := g.newTemp("^bool")
		g.emit("\tMGET\t%s\t%s\t%s\t%s\n", valTmp, okTmp, xOp, keyOp)
		return valTmp, okTmp, nil
	}
	if call, isCall := expr.(*ast.CallExpr); isCall {
		// A call's own single result may itself be nullable (e.g. `let e:
		// error? = makeError()`, a function returning just `error?`) —
		// call it exactly once (never via a separate genValue(expr) after
		// this, which would invoke it a second time) and read both its
		// value and isset operands directly from that one CALL. Builtins
		// (append/len/range/string/filter/map/reduce/error) are excluded:
		// none of them can currently produce a nullable result, so they
		// fall through to the generic path below instead.
		_, isPlainFunc := g.sigs[call.Callee]
		if call.Receiver != nil || call.IsClosureVar || isPlainFunc {
			calleeName, sig, args, err := resolveCall(g, call)
			if err != nil {
				return "", "", err
			}
			refs, err := emitCallWithResults(g, calleeName, args, sig.Results)
			if err != nil {
				return "", "", err
			}
			if refs[0].SetOp != "" {
				return refs[0].ValOp, refs[0].SetOp, nil
			}
			return refs[0].ValOp, "true", nil
		}
	}
	v, err := genValue(g, expr)
	if err != nil {
		return "", "", err
	}
	return v, "true", nil
}

// genCallArgs compiles call's arguments against sig's parameter types,
// expanding each nullable parameter into its two operands (see
// genFuncDecl's doc) so the result lines up positionally with the
// callee's own expanded FUNC parameter list.
func genCallArgs(g *funcGen, call *ast.CallExpr, sig funcSig) ([]string, error) {
	var args []string
	for i, argExpr := range call.Args {
		if needsIssetSlot(sig.Params[i]) {
			val, isset, err := genNullableOperands(g, argExpr, sig.Params[i])
			if err != nil {
				return nil, err
			}
			args = append(args, val, isset)
			continue
		}
		v, err := genValue(g, argExpr)
		if err != nil {
			return nil, err
		}
		args = append(args, v)
	}
	return args, nil
}

// emitCall writes one CALL instruction: results (possibly empty, for a
// discarded-result statement call) `:` calleeName args....
func emitCall(g *funcGen, results []string, calleeName string, args []string) {
	format := "\tCALL"
	var parts []any
	for _, r := range results {
		format += "\t%s"
		parts = append(parts, r)
	}
	format += "\t:\t%s"
	parts = append(parts, calleeName)
	for _, a := range args {
		format += "\t%s"
		parts = append(parts, a)
	}
	format += "\n"
	g.emit(format, parts...)
}

// genResultTemps allocates one or two fresh anonymous temps per result
// type (two when needsIssetSlot(t) — mirrors genFuncDecl's identical
// parameter expansion, applied here to CALL's own result side),
// returning each result's own varRef (ValOp, and SetOp when nullable)
// alongside the flat list of result-operand tokens to pass to CALL.
func genResultTemps(g *funcGen, resultTypes []ast.Type) ([]varRef, []string, error) {
	refs := make([]varRef, len(resultTypes))
	var tokens []string
	for i, rt := range resultTypes {
		irType, err := typeToIR(g.types, rt)
		if err != nil {
			return nil, nil, err
		}
		ref := varRef{Type: rt, ValOp: g.newTemp(irType)}
		tokens = append(tokens, ref.ValOp)
		if needsIssetSlot(rt) {
			ref.SetOp = g.newTemp("^bool")
			tokens = append(tokens, ref.SetOp)
		}
		refs[i] = ref
	}
	return refs, tokens, nil
}

// emitCallWithResults emits one CALL against calleeName with args,
// expanding results per genResultTemps, and returns each result's own
// varRef for the caller to pick out whichever it needs — a single-value
// call needs only refs[0].ValOp (genUserFuncCallValue/genMethodCallValue/
// genClosureCallValue), while genNullableOperands's CallExpr case and
// error.go's genErrorProp need refs[0]'s (or the last result's) SetOp
// too. Discarding every result entirely never calls this at all — see
// genUserFuncCallStmt et al., which just pass CALL an empty result list
// directly (amivm allows fully omitting CALL's results for a bare
// side-effect call, regardless of how many the callee actually has).
func emitCallWithResults(g *funcGen, calleeName string, args []string, results []ast.Type) ([]varRef, error) {
	refs, tokens, err := genResultTemps(g, results)
	if err != nil {
		return nil, err
	}
	emitCall(g, tokens, calleeName, args)
	return refs, nil
}

// resolveCall resolves call's callee token, signature, and compiled
// argument list, uniformly across every call kind (plain function,
// method, closure variable) — shared wherever a caller needs to inspect
// or specially handle a call's *results* itself, rather than simply
// taking the first one as an ordinary value (see genMultiLetDecl,
// genNullableOperands's CallExpr case, and error.go's genErrorProp, all
// of which need every result operand, not just the first). Only ever
// called with call.Receiver != nil, call.IsClosureVar, or a call.Callee
// present in g.sigs — never a builtin, which callers must exclude
// themselves (see genNullableOperands's isPlainFunc guard).
func resolveCall(g *funcGen, call *ast.CallExpr) (calleeName string, sig funcSig, args []string, err error) {
	switch {
	case call.Receiver != nil:
		sig = g.methods[call.RecvStruct][call.Callee]
		args, err = genMethodCallArgs(g, call, sig)
		calleeName = "!" + methodAmivmName(call)
	case call.IsClosureVar:
		ref, ok := g.scope.lookup(call.Callee)
		if !ok {
			return "", funcSig{}, nil, fmt.Errorf("codegen: undefined name %q (sema bug)", call.Callee)
		}
		sig = funcSig{Params: ref.Type.Func.Params, Results: ref.Type.Func.Results}
		args, err = genCallArgs(g, call, sig)
		if err != nil {
			return "", funcSig{}, nil, err
		}
		irType, terr := typeToIR(g.types, ref.Type)
		if terr != nil {
			return "", funcSig{}, nil, terr
		}
		calleeName = closureCallTarget(g, ref.ValOp, irType)
		return calleeName, sig, args, nil
	default:
		sig = g.sigs[call.Callee]
		args, err = genCallArgs(g, call, sig)
		calleeName = "!" + funcAmivmName(call.Callee)
	}
	return calleeName, sig, args, err
}

// genUserFuncCallValue compiles a call to a user-defined function used
// as a value (cascade_spec.md §8.1) into a fresh temp, taking its first
// result — sema.Check guarantees len(sig.Results) >= 1 here (a
// zero-result call can't be used as a value at all — see
// checkCallSigAsValue) and that a genuinely multi-result call only
// appears as a MultiLetDecl's Init (see genMultiLetDecl) — so refs[0] is
// always the right (and, in every case that reaches this function via
// genCallValue's dispatch, only) one to return.
func genUserFuncCallValue(g *funcGen, call *ast.CallExpr, sig funcSig) (string, error) {
	args, err := genCallArgs(g, call, sig)
	if err != nil {
		return "", err
	}
	refs, err := emitCallWithResults(g, "!"+funcAmivmName(call.Callee), args, sig.Results)
	if err != nil {
		return "", err
	}
	return refs[0].ValOp, nil
}

// genUserFuncCallStmt compiles a call to any function used as a bare
// statement (cascade_spec.md §8.1), discarding every result regardless
// of how many there are — AMIVM-IR/Go both allow a CALL/call expression
// with its result(s) simply omitted.
func genUserFuncCallStmt(g *funcGen, call *ast.CallExpr, sig funcSig) error {
	args, err := genCallArgs(g, call, sig)
	if err != nil {
		return err
	}
	emitCall(g, nil, "!"+funcAmivmName(call.Callee), args)
	return nil
}

// genMultiLetDecl compiles a multi-value `let`/`const` declaration
// (cascade_spec.md §5, §8.5): one CALL whose result operands are either
// one or two freshly hoisted VARs per named result (two when that
// result's own type needs an isset slot — cascade_spec.md §8.6's `(T,
// error?)` convention is exactly this, with error? in the last
// position, but the expansion itself applies uniformly to any nullable
// result), or "_" (twice, for a discarded nullable result) for a
// discarded one (AMIVM-IR's own discard token, matching the multi1/
// multi2 operand category — see amivm_spec.md §5). decl.ResolvedTypes
// (filled in by sema.Check) gives each name's type without re-deriving
// it from sig. Init's callee may be a plain function, a method call
// (§8.2), or a closure-valued variable (§8.3) — see resolveCall.
func genMultiLetDecl(g *funcGen, decl *ast.MultiLetDecl) error {
	calleeName, _, args, err := resolveCall(g, decl.Init)
	if err != nil {
		return err
	}

	var results []string
	for i, name := range decl.Names {
		typ := decl.ResolvedTypes[i]
		if name == "_" {
			results = append(results, "_")
			if needsIssetSlot(typ) {
				results = append(results, "_")
			}
			continue
		}
		irType, err := typeToIR(g.types, typ)
		if err != nil {
			return err
		}
		internal := g.freshName(name)
		ref := varRef{Type: typ, ValOp: "%" + internal}
		g.declareVar(ref.ValOp, irType)
		results = append(results, ref.ValOp)
		if needsIssetSlot(typ) {
			ref.SetOp = "%" + internal + "_isset"
			g.declareVar(ref.SetOp, "^bool")
			results = append(results, ref.SetOp)
		}
		g.scope.declare(name, ref)
	}

	emitCall(g, results, calleeName, args)
	return nil
}
