package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/amisonnet8/cascade/internal/ast"
)

// typeRegistry tracks which composite AMIVM-IR deftypes need a top-level
// declaration before any FUNC references them (amivm requires SLTYPE/
// FNTYPE/MPTYPE before any use — see amivm_spec.md §2.2): list element
// types for SLTYPE (cascade_spec.md §2.2's `[]T`, mirrors Seed's
// identical registry), function-type shapes for FNTYPE (§2.2, §8.3), and
// map key/value shapes for MPTYPE (§2.2, §4.5). It's shared by the whole
// Generate() call, since these declarations are emitted once, up front,
// ahead of every FUNC block.
type typeRegistry struct {
	used map[string]bool

	funcTypes    map[string]*funcTypeEntry // canonical shape key -> entry
	funcTypeList []*funcTypeEntry          // first-seen order, for stable FNTYPE emission

	mapTypes    map[string]*mapTypeEntry // canonical shape key -> entry
	mapTypeList []*mapTypeEntry          // first-seen order, for stable MPTYPE emission

	chanTypes    map[string]*chanTypeEntry // canonical shape key -> entry
	chanTypeList []*chanTypeEntry          // first-seen order, for stable CHTYPE emission
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

// mapTypeEntry is one map-type shape registered with useMapType, carrying
// its already-resolved key/value IR type tokens (needed to emit its own
// MPTYPE line — see mapTypeDecls). Mirrors funcTypeEntry.
type mapTypeEntry struct {
	Name  string // deftype name, no leading `^`
	Key   string
	Value string
}

// useMapType registers a map type shape — its already-resolved key/value
// IR type tokens — as needing a top-level MPTYPE declaration,
// deduplicating by shape so two Cascade map<K, V> types with the same
// actual key/value types share one deftype, and returns its AMIVM-IR
// type token (a synthesized name, e.g. "^MapType1" — mirrors
// useFuncType).
func (r *typeRegistry) useMapType(keyToken, valueToken string) string {
	if r.mapTypes == nil {
		r.mapTypes = map[string]*mapTypeEntry{}
	}
	key := keyToken + ":" + valueToken
	if e, ok := r.mapTypes[key]; ok {
		return "^" + e.Name
	}
	e := &mapTypeEntry{
		Name:  fmt.Sprintf("MapType%d", len(r.mapTypeList)+1),
		Key:   keyToken,
		Value: valueToken,
	}
	r.mapTypes[key] = e
	r.mapTypeList = append(r.mapTypeList, e)
	return "^" + e.Name
}

// mapTypeDecls returns every registered map-type shape's MPTYPE
// declaration line, in first-seen order (stable, deterministic output).
func (r *typeRegistry) mapTypeDecls() []string {
	decls := make([]string, len(r.mapTypeList))
	for i, e := range r.mapTypeList {
		decls[i] = fmt.Sprintf("MPTYPE\t^%s\t%s\t%s", e.Name, e.Key, e.Value)
	}
	return decls
}

// chanTypeEntry is one channel-type shape registered with useChan,
// carrying its already-resolved element IR type token (needed to emit its
// own CHTYPE line — see chanTypeDecls). Mirrors mapTypeEntry, keyed by
// the element token alone rather than a Name (cascade_spec.md §2.2's
// chan<T> is unnamed on the Cascade side, unlike a list's own elemName-
// derived token — see listTypeToken — since a channel's element type
// isn't restricted to a scalar/struct the way a list's is; typeToIR
// resolves T generically first, so the same deduplication-by-shape
// approach useFuncType/useMapType already use applies here unchanged).
type chanTypeEntry struct {
	Name string // deftype name, no leading `^`
	Elem string
}

// useChan registers a channel-type shape — its already-resolved element
// IR type token — as needing a top-level CHTYPE declaration,
// deduplicating by shape so two Cascade chan<T> types with the same
// actual element type share one deftype, and returns its AMIVM-IR type
// token (a synthesized name, e.g. "^ChanType1" — mirrors useFuncType/
// useMapType).
func (r *typeRegistry) useChan(elemToken string) string {
	if r.chanTypes == nil {
		r.chanTypes = map[string]*chanTypeEntry{}
	}
	if e, ok := r.chanTypes[elemToken]; ok {
		return "^" + e.Name
	}
	e := &chanTypeEntry{
		Name: fmt.Sprintf("ChanType%d", len(r.chanTypeList)+1),
		Elem: elemToken,
	}
	r.chanTypes[elemToken] = e
	r.chanTypeList = append(r.chanTypeList, e)
	return "^" + e.Name
}

// chanTypeDecls returns every registered channel-type shape's CHTYPE
// declaration line, in first-seen order (stable, deterministic output).
// Emitted last among Generate()'s TYPE-decl loops (after STTYPE/SLTYPE/
// FNTYPE/MPTYPE) since a channel's element type may itself be any one of
// those composite kinds — see typeToIR's Chan branch, which resolves T's
// own token (registering whichever of those it needs) before calling
// this.
func (r *typeRegistry) chanTypeDecls() []string {
	decls := make([]string, len(r.chanTypeList))
	for i, e := range r.chanTypeList {
		decls[i] = fmt.Sprintf("CHTYPE\t^%s\t%s", e.Name, e.Elem)
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

// typeToIR maps any Cascade type (scalar, struct, list, pointer,
// function, or map) to its AMIVM-IR type token, registering the element
// type with reg if t is a list, or the whole shape if t is a function or
// map type. Nested list types (`[][]T`) aren't supported yet —
// cascade_spec.md never shows one — so t.Elem itself being a list is an
// error here; likewise a pointer to a list or another pointer.
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
		// Both params and results need the same needsIssetSlot two-token
		// expansion here that FUNC/CLOS's own declarations already apply
		// (see genFuncDecl/genClosureLit) — this FNTYPE deftype describes
		// the exact callable shape those blocks emit, so the two must
		// always agree in operand count or amivm's own go/types checking
		// rejects the mismatch outright (a real error caught only by
		// actually running a nullable-result closure through amivm — see
		// CLAUDE.md's "確定した設計判断").
		var paramTokens []string
		for _, p := range t.Func.Params {
			tok, err := typeToIR(reg, p)
			if err != nil {
				return "", err
			}
			paramTokens = append(paramTokens, tok)
			if needsIssetSlot(p) {
				paramTokens = append(paramTokens, "^bool")
			}
		}
		var resultTokens []string
		for _, r := range t.Func.Results {
			tok, err := typeToIR(reg, r)
			if err != nil {
				return "", err
			}
			resultTokens = append(resultTokens, tok)
			if needsIssetSlot(r) {
				resultTokens = append(resultTokens, "^bool")
			}
		}
		return reg.useFuncType(paramTokens, resultTokens), nil
	}
	if t.Map != nil {
		keyTok, err := typeToIR(reg, t.Map.Key)
		if err != nil {
			return "", err
		}
		valTok, err := typeToIR(reg, t.Map.Value)
		if err != nil {
			return "", err
		}
		return reg.useMapType(keyTok, valTok), nil
	}
	if t.Chan != nil {
		elemTok, err := typeToIR(reg, *t.Chan)
		if err != nil {
			return "", err
		}
		return reg.useChan(elemTok), nil
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

// genIndexRead compiles `xs[i]` (list index, cascade_spec.md §5) or
// `m[k]` (map key read, §4.5) into a fresh temp of e.ResultType (the
// list's element type, or the map's value type, filled in by
// sema.Check). e.ResultType.Nullable distinguishes the two: a list read
// is never nullable (elements aren't independently nullable — see
// ast.Type's doc), while a map read always is (`m[k]` returns `V?` via
// MGET's comma-ok form — see ast.IndexExpr's exprType case in sema),
// so it's a clean, already-available discriminator with no extra field
// needed. This value-only MGET form (no ok flag) is only reached where
// sema has already guaranteed the isset flag doesn't matter for this
// particular read — see genNullableOperands's IndexExpr case for the
// comma-ok form used everywhere the result's nullability is actually
// significant (e.g. assigning into a `V?`-typed variable).
func genIndexRead(g *funcGen, e *ast.IndexExpr) (string, error) {
	xOp, err := genValue(g, e.X)
	if err != nil {
		return "", err
	}
	keyOrIdxOp, err := genValue(g, e.Index)
	if err != nil {
		return "", err
	}
	if e.ResultType.Nullable {
		irType, err := typeToIR(g.types, e.ResultType)
		if err != nil {
			return "", err
		}
		tmp := g.newTemp(irType)
		g.emit("\tMGET\t%s\t%s\t%s\n", tmp, xOp, keyOrIdxOp)
		return tmp, nil
	}
	irType, err := scalarTypeToIR(e.ResultType)
	if err != nil {
		return "", err
	}
	tmp := g.newTemp(irType)
	g.emit("\tAGET\t%s\t%s\t%s\n", tmp, xOp, keyOrIdxOp)
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
// genForInStmt (seed/internal/codegen/stmt.go). The two-variable map
// form (`for k, v in m`) is a different shape entirely — see
// genForInMapStmt.
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
	if stmt.ValueVarName != "" {
		return genForInMapStmt(g, stmt)
	}
	if stmt.IsChannel {
		return genForInChannelStmt(g, stmt)
	}
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
