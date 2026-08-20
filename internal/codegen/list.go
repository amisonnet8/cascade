package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/amisonnet8/cascade/internal/ast"
)

// typeRegistry tracks which composite AMIVM-IR deftypes need a top-level
// declaration before any FUNC references them (amivm requires SLTYPE/
// FNTYPE before any use — see amivm_spec.md §2.2): list element types
// for SLTYPE (cascade_spec.md §2.2's `[]T`, mirrors Seed's identical
// registry) and function-type shapes for FNTYPE (§2.2, §8.3). It's
// shared by the whole Generate() call, since these declarations are
// emitted once, up front, ahead of every FUNC block.
type typeRegistry struct {
	used map[string]bool

	funcTypes    map[string]*funcTypeEntry // canonical shape key -> entry
	funcTypeList []*funcTypeEntry          // first-seen order, for stable FNTYPE emission
}

// use registers elemName (a scalar type name — nested list element types
// aren't supported yet, see typeToIR) as needing a list type, and returns
// its AMIVM-IR type token.
func (r *typeRegistry) use(elemName string) string {
	r.used[elemName] = true
	return listTypeToken(elemName)
}

func (r *typeRegistry) sorted() []string {
	names := make([]string, 0, len(r.used))
	for name := range r.used {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// funcTypeEntry is one function-type shape registered with useFuncType,
// carrying its already-resolved parameter/result IR type tokens (needed
// to emit its own FNTYPE line — see funcTypeDecls).
type funcTypeEntry struct {
	Name    string // deftype name, no leading `^`
	Params  []string
	Results []string
}

// useFuncType registers a function type shape — its already-resolved
// parameter/result IR type tokens — as needing a top-level FNTYPE
// declaration, deduplicating by shape so two Cascade func(...): R types
// with the same actual signature share one deftype, and returns its
// AMIVM-IR type token (a synthesized name, e.g. "^FuncType1" — unlike
// list element types, a func type's own shape has no natural short name
// to derive one from).
func (r *typeRegistry) useFuncType(paramTokens, resultTokens []string) string {
	if r.funcTypes == nil {
		r.funcTypes = map[string]*funcTypeEntry{}
	}
	key := strings.Join(paramTokens, ",") + ":" + strings.Join(resultTokens, ",")
	if e, ok := r.funcTypes[key]; ok {
		return "^" + e.Name
	}
	e := &funcTypeEntry{
		Name:    fmt.Sprintf("FuncType%d", len(r.funcTypeList)+1),
		Params:  paramTokens,
		Results: resultTokens,
	}
	r.funcTypes[key] = e
	r.funcTypeList = append(r.funcTypeList, e)
	return "^" + e.Name
}

// funcTypeDecls returns every registered function-type shape's FNTYPE
// declaration line, in first-seen order (stable, deterministic output).
func (r *typeRegistry) funcTypeDecls() []string {
	decls := make([]string, len(r.funcTypeList))
	for i, e := range r.funcTypeList {
		var b strings.Builder
		fmt.Fprintf(&b, "FNTYPE\t^%s", e.Name)
		for _, t := range e.Params {
			b.WriteString("\t")
			b.WriteString(t)
		}
		b.WriteString("\t:")
		for _, t := range e.Results {
			b.WriteString("\t")
			b.WriteString(t)
		}
		decls[i] = b.String()
	}
	return decls
}

// listTypeToken is the AMIVM-IR type token for elemName's list form, e.g.
// "int" -> "^intlist". Cascade lists compile to Go slices (SLTYPE+
// SLMAKE), not Go's fixed-size array type — cascade_spec.md §4.3's `let
// xs: []int` never fixes a size, so only a slice can represent it.
func listTypeToken(elemName string) string {
	return "^" + elemName + "list"
}

// typeToIR maps any Cascade type (scalar, struct, list, pointer, or
// function) to its AMIVM-IR type token, registering the element type
// with reg if t is a list or the whole shape if t is a function type.
// Nested list types (`[][]T`) aren't supported yet — cascade_spec.md
// never shows one — so t.Elem itself being a list is an error here;
// likewise a pointer to a list or another pointer.
func typeToIR(reg *typeRegistry, t ast.Type) (string, error) {
	if t.Elem != nil {
		if t.Elem.Elem != nil {
			return "", fmt.Errorf("codegen: nested list types are not supported yet")
		}
		return reg.use(t.Elem.Name), nil
	}
	if t.Ptr != nil {
		if t.Ptr.Elem != nil || t.Ptr.Ptr != nil {
			return "", fmt.Errorf("codegen: pointers to list/pointer types are not supported yet")
		}
		base, err := baseTypeName(*t.Ptr)
		if err != nil {
			return "", err
		}
		return "^*" + base, nil
	}
	if t.Func != nil {
		paramTokens := make([]string, len(t.Func.Params))
		for i, p := range t.Func.Params {
			tok, err := typeToIR(reg, p)
			if err != nil {
				return "", err
			}
			paramTokens[i] = tok
		}
		resultTokens := make([]string, len(t.Func.Results))
		for i, r := range t.Func.Results {
			tok, err := typeToIR(reg, r)
			if err != nil {
				return "", err
			}
			resultTokens[i] = tok
		}
		return reg.useFuncType(paramTokens, resultTokens), nil
	}
	return scalarTypeToIR(t)
}

// genListLiteralInit compiles a list literal (cascade_spec.md §3) into
// ref: SLMAKE allocates a fresh backing array sized exactly to the
// literal (matching Seed's "no resize is ever generated" fixed-length
// guarantee at creation time — see cascade_spec.md §4.3's doc), then each
// element is ASET at its literal, compile-time-known index.
func genListLiteralInit(g *funcGen, ref varRef, lit *ast.ListLit) error {
	irType, err := typeToIR(g.types, ref.Type)
	if err != nil {
		return err
	}
	g.emit("\tSLMAKE\t%s\t%s\t%d\n", ref.ValOp, irType, len(lit.Elems))
	for i, elem := range lit.Elems {
		v, err := genValue(g, elem)
		if err != nil {
			return err
		}
		g.emit("\tASET\t%s\t%d\t%s\n", ref.ValOp, i, v)
	}
	if ref.SetOp != "" {
		g.emit("\tSET\t%s\ttrue\n", ref.SetOp)
	}
	return nil
}

// genIndexRead compiles `xs[i]` (cascade_spec.md §5) into a fresh temp of
// e.ResultType (the list's element type, filled in by sema.Check).
func genIndexRead(g *funcGen, e *ast.IndexExpr) (string, error) {
	listOp, err := genValue(g, e.X)
	if err != nil {
		return "", err
	}
	idxOp, err := genValue(g, e.Index)
	if err != nil {
		return "", err
	}
	irType, err := scalarTypeToIR(e.ResultType)
	if err != nil {
		return "", err
	}
	tmp := g.newTemp(irType)
	g.emit("\tAGET\t%s\t%s\t%s\n", tmp, listOp, idxOp)
	return tmp, nil
}

// genLenCall compiles len(x) (cascade_spec.md §13). Go's builtin len()
// is itself polymorphic over string/slice/map — the same `?len` CALL
// works for every one of len()'s overloads, so unlike string() there is
// no argument-type dispatch needed here at all (mirrors Seed's identical
// `?len` usage in its for-in codegen).
func genLenCall(g *funcGen, call *ast.CallExpr) (string, error) {
	x, err := genValue(g, call.Args[0])
	if err != nil {
		return "", err
	}
	tmp := g.newTemp("^int")
	g.emit("\tCALL\t%s\t:\t?len\t%s\n", tmp, x)
	return tmp, nil
}

// genRangeCall compiles range(from, to) (cascade_spec.md §13) into a
// freshly SLMAKE'd []int, filled in by a runtime counting loop (the same
// LABEL/IF/GOTO shape genWhileStmt/genForInStmt use, emitted directly
// since this loop has no user-visible break/continue target and declares
// no Cascade-level variables).
func genRangeCall(g *funcGen, call *ast.CallExpr) (string, error) {
	from, err := genValue(g, call.Args[0])
	if err != nil {
		return "", err
	}
	to, err := genValue(g, call.Args[1])
	if err != nil {
		return "", err
	}

	intElem := ast.Type{Name: "int"}
	listIRType, err := typeToIR(g.types, ast.Type{Elem: &intElem})
	if err != nil {
		return "", err
	}

	sizeOp := g.newTemp("^int")
	g.emit("\tSUB\t%s\t%s\t%s\n", sizeOp, to, from)
	result := g.newTemp(listIRType)
	g.emit("\tSLMAKE\t%s\t%s\t%s\n", result, listIRType, sizeOp)

	idxOp := g.newTemp("^int")
	g.emit("\tSET\t%s\t0\n", idxOp)
	startLabel := g.newLabel()
	bodyLabel := g.newLabel()
	endLabel := g.newLabel()
	g.emit("\tLABEL\t#%s\n", startLabel)
	cmp := g.newTemp("^bool")
	g.emit("\tLT\t%s\t%s\t%s\n", cmp, idxOp, sizeOp)
	g.emit("\tIF\t%s\t#%s\n", cmp, bodyLabel)
	g.emit("\tGOTO\t#%s\n", endLabel)
	g.emit("\tLABEL\t#%s\n", bodyLabel)
	valOp := g.newTemp("^int")
	g.emit("\tADD\t%s\t%s\t%s\n", valOp, from, idxOp)
	g.emit("\tASET\t%s\t%s\t%s\n", result, idxOp, valOp)
	g.emit("\tADD\t%s\t%s\t1\n", idxOp, idxOp)
	g.emit("\tGOTO\t#%s\n", startLabel)
	g.emit("\tLABEL\t#%s\n", endLabel)
	return result, nil
}

// genAppendCall compiles append(list, value) (cascade_spec.md §13) by
// always allocating a fresh, exactly-sized backing array and copying the
// old elements into it, rather than emitting a raw Go append(). Go's
// append() may write into unused capacity of the *original* backing
// array when there's room, which is invisible to the original variable
// itself but would let two independent append()s off the same list race
// over the same backing slot — reallocating unconditionally, as
// SLMAKE-per-list-literal already does elsewhere, avoids that aliasing
// risk entirely and matches "元のリストは変更しない" (§13) unconditionally,
// not just usually.
func genAppendCall(g *funcGen, call *ast.CallExpr) (string, error) {
	listOp, err := genValue(g, call.Args[0])
	if err != nil {
		return "", err
	}
	valueOp, err := genValue(g, call.Args[1])
	if err != nil {
		return "", err
	}

	listIRType, err := typeToIR(g.types, call.ArgType)
	if err != nil {
		return "", err
	}
	elemIRType, err := scalarTypeToIR(*call.ArgType.Elem)
	if err != nil {
		return "", err
	}

	lenOp := g.newTemp("^int")
	g.emit("\tCALL\t%s\t:\t?len\t%s\n", lenOp, listOp)
	newLenOp := g.newTemp("^int")
	g.emit("\tADD\t%s\t%s\t1\n", newLenOp, lenOp)
	result := g.newTemp(listIRType)
	g.emit("\tSLMAKE\t%s\t%s\t%s\n", result, listIRType, newLenOp)

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
	g.emit("\tASET\t%s\t%s\t%s\n", result, idxOp, elemOp)
	g.emit("\tADD\t%s\t%s\t1\n", idxOp, idxOp)
	g.emit("\tGOTO\t#%s\n", startLabel)
	g.emit("\tLABEL\t#%s\n", endLabel)

	g.emit("\tASET\t%s\t%s\t%s\n", result, lenOp, valueOp)
	return result, nil
}

// genForInStmt compiles `for x in List { ... }` (cascade_spec.md §7) as
// an index-counted loop over len(List) — mirrors Seed's identical
// genForInStmt (seed/internal/codegen/stmt.go).
//
//	CALL lenOp : ?len list; SET idxOp 0
//	LABEL start; LT cmp idxOp lenOp; IF cmp body; GOTO end
//	LABEL body; AGET x list idxOp; ...; LABEL continue
//	ADD idxOp idxOp 1; GOTO start
//	LABEL end
//
// Unlike genWhileStmt, `continue` cannot target start directly: start
// only re-checks the condition, and skipping straight to it would skip
// the idxOp++ step, looping forever on the same element. So continue
// targets a dedicated label positioned right before that increment,
// which the body's normal (non-break/continue) fallthrough also reaches.
func genForInStmt(g *funcGen, stmt *ast.ForInStmt) error {
	listOp, err := genValue(g, stmt.List)
	if err != nil {
		return err
	}
	elemIRType, err := scalarTypeToIR(stmt.ElemType)
	if err != nil {
		return err
	}

	lenOp := g.newTemp("^int")
	g.emit("\tCALL\t%s\t:\t?len\t%s\n", lenOp, listOp)
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
	loopRef := varRef{Type: stmt.ElemType, ValOp: "%" + g.freshName(stmt.VarName)}
	g.declareVar(loopRef.ValOp, elemIRType)
	g.scope.declare(stmt.VarName, loopRef)
	g.emit("\tAGET\t%s\t%s\t%s\n", loopRef.ValOp, listOp, idxOp)

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
