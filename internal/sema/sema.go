// Package sema performs semantic analysis on a Cascade ast.File, since
// amivm delegates all type/scope checking to go/types and does not check
// anything itself (see CLAUDE.md "意味検証の責任分担"). Only the checks
// Steps 1-10 need are implemented so far: a single well-formed `main`
// plus any number of other functions (§8.1) — including multi-value
// returns (§8.5) — scope-resolved `let`/`const` declarations and scalar/
// list-element/map-element/struct-field/compound assignment,
// nullable-type (`T?`) compatibility and narrowing (§2.3, §4.2, §5, §7),
// the full operator set (§6), control flow (if/elif/else, while, for-in
// over a list or a map, switch, break/continue), lists (`[]T` —
// literals, indexing, append/range/len), structs/pointers/receiver
// functions (§4.1, §4.4, §8.2 — struct declarations, struct literals
// requiring every field, field read/write on a struct or a pointer to
// one, `&`/`*` address-of/dereference, and method calls with automatic
// address-of/dereference between value and pointer receivers),
// closures/higher-order functions (§8.3, §8.4 — closure literals
// capturing the surrounding scope, calling a closure-valued local
// variable, and the filter/map/reduce builtins), and maps (§2.2, §4.5 —
// `map<K, V>` with a non-nullable scalar key and non-nullable value,
// literals, `m[k]` returning `V?`, `m[k] = v`, `delete`, and the
// two-variable `for k, v in m` form). Later steps add channels/
// pipelines and everything past that.
//
// A function whose non-void body doesn't obviously return on every path
// isn't checked here (no control-flow reachability analysis is
// implemented yet) — such a mistake surfaces as amivm's less friendly
// "missing return" error instead. Nullable return types (`func f(): T?`)
// are rejected outright for now; see checkFuncSig.
package sema

import (
	"strings"

	"fmt"

	"github.com/amisonnet8/cascade/internal/ast"
)

// mainInternalName is the amivm-level name codegen emits the user's `main`
// under (see codegen.go's package doc for why `main` itself can't be used
// directly). Must match codegen's mainInternalName constant.
const mainInternalName = "cascade_main"

// funcSig is one function's signature (cascade_spec.md §8.1): its
// parameter and result types, used to validate every call to it. The
// whole table is built before any function body is checked (see Check),
// so forward references and (mutual) recursion both work regardless of
// declaration order.
type funcSig struct {
	Params  []ast.Type
	Results []ast.Type
}

// methodSig is one receiver function's signature (cascade_spec.md §8.2):
// like funcSig, plus the receiver's own declared type (Name for a value
// receiver, Ptr set for a pointer receiver — see receiverStructName).
// Methods are looked up per struct name rather than through the flat
// funcSig table, so a method and a plain function may freely share a
// name, and so can two different structs' same-named methods.
type methodSig struct {
	RecvType ast.Type
	Params   []ast.Type
	Results  []ast.Type
}

// checker carries state shared across every function body being checked:
// the signature table, the struct registry, and the method table. This is
// the "小さな構造体(checker)" seed_implementation_notes.md §7 recommends
// introducing once program-wide (not just per-call-site) shared
// information is needed. Scope, by contrast, changes with every nested
// block, so it stays an explicit parameter throughout rather than
// becoming a checker field.
type checker struct {
	sigs    map[string]funcSig
	structs map[string]*ast.StructDecl
	methods map[string]map[string]methodSig // struct name -> method name -> sig

	// closureDepth counts enclosing closure-literal bodies currently being
	// checked (0 outside any closure). amivm's CLOS cannot itself contain
	// another CLOS (amivm_spec.md §2.1's "CLOSはFUNC本体内にのみ出現し、ネ
	// スト不可") — see checkClosureLit, which rejects a ClosureLit found
	// while this is already > 0, so codegen never has to guard against it.
	closureDepth int
}

// reservedBuiltinNames are plain identifiers (not lexer keywords, unlike
// print/string/int/float/bool are not, but see below) that sema/codegen
// dispatch on directly by name (cascade_spec.md §13). A user-defined
// *function* reusing one of these names would be silently unreachable —
// shadowed by the builtin's own dispatch, which always checks first — so
// it's rejected outright instead. A *local variable* is not restricted
// this way: a closure-valued variable is deliberately allowed to shadow
// a same-named builtin (cascade_spec.md §10's ordinary shadowing rules,
// extended to the "callable" namespace — see resolveClosureCall).
// "string"/"int"/"float"/"bool"/"map" need no entry here: they're lexer
// keywords, so the parser already can't parse them as a function (or
// variable) name in the first place (see parser's parsePrimaryAtom), and
// likewise for "send"/"chan"/"collect"/"error".
var reservedBuiltinNames = map[string]bool{
	"print":  true,
	"len":    true,
	"range":  true,
	"append": true,
	"filter": true,
	"reduce": true,
	"delete": true,
}

// Check validates f. cascade_spec.md §12 requires exactly one `main`
// function, taking no parameters and returning a single non-nullable int;
// every function's signature (including main's) is collected into one
// table first, and then every function's body — main's and every other
// one's — is checked against it.
func Check(f *ast.File) error {
	c := &checker{
		sigs:    map[string]funcSig{},
		structs: map[string]*ast.StructDecl{},
		methods: map[string]map[string]methodSig{},
	}

	// Structs are registered in their own pass, before any field's type is
	// validated, so a field may reference another struct regardless of
	// declaration order — including its own struct (e.g. `next: *Node`).
	for _, sd := range f.Structs {
		if _, exists := c.structs[sd.Name]; exists {
			return fmt.Errorf("line %d: duplicate struct %q", sd.Line, sd.Name)
		}
		c.structs[sd.Name] = sd
	}
	for _, sd := range f.Structs {
		seen := map[string]bool{}
		for _, field := range sd.Fields {
			if seen[field.Name] {
				return fmt.Errorf("line %d: struct %q: duplicate field %q", sd.Line, sd.Name, field.Name)
			}
			seen[field.Name] = true
			if err := c.validateType(field.Type, sd.Line); err != nil {
				return err
			}
			if field.Type.Nullable && field.Type.Ptr == nil {
				return fmt.Errorf("line %d: struct %q: field %q: nullable non-pointer field types are not supported yet", sd.Line, sd.Name, field.Name)
			}
		}
	}

	var main *ast.FuncDecl
	for _, fn := range f.Funcs {
		if fn.Receiver != nil {
			continue // registered into c.methods below, once every struct/plain-function name is known
		}
		if fn.Name == mainInternalName {
			return fmt.Errorf("line %d: %q is a reserved name and cannot be used as a function name", fn.Line, mainInternalName)
		}
		if reservedBuiltinNames[fn.Name] {
			return fmt.Errorf("line %d: %q is a builtin function name and cannot be redefined", fn.Line, fn.Name)
		}
		if _, exists := c.sigs[fn.Name]; exists {
			return fmt.Errorf("line %d: duplicate function %q", fn.Line, fn.Name)
		}
		if err := c.checkFuncSig(fn); err != nil {
			return err
		}
		params := make([]ast.Type, len(fn.Params))
		for i, p := range fn.Params {
			params[i] = p.Type
		}
		c.sigs[fn.Name] = funcSig{Params: params, Results: fn.Results}
		if fn.Name == "main" {
			main = fn
		}
	}
	if main == nil {
		return fmt.Errorf("no 'main' function found")
	}
	if len(main.Params) != 0 {
		return fmt.Errorf("line %d: 'main' must take no parameters (cascade_spec.md §12)", main.Line)
	}
	if len(main.Results) != 1 || main.Results[0].Name != "int" || main.Results[0].Nullable {
		return fmt.Errorf("line %d: 'main' must return int (cascade_spec.md §12)", main.Line)
	}

	// Methods are namespaced per struct (see methodSig's doc), so a
	// duplicate check here only rejects the same struct declaring the same
	// method name twice, never a collision with c.sigs.
	for _, fn := range f.Funcs {
		if fn.Receiver == nil {
			continue
		}
		structName, _, err := c.receiverStructName(*fn.Receiver)
		if err != nil {
			return err
		}
		if err := c.checkFuncSig(fn); err != nil {
			return err
		}
		params := make([]ast.Type, len(fn.Params))
		for i, p := range fn.Params {
			params[i] = p.Type
		}
		if c.methods[structName] == nil {
			c.methods[structName] = map[string]methodSig{}
		}
		if _, exists := c.methods[structName][fn.Name]; exists {
			return fmt.Errorf("line %d: struct %q: duplicate method %q", fn.Line, structName, fn.Name)
		}
		c.methods[structName][fn.Name] = methodSig{RecvType: fn.Receiver.Type, Params: params, Results: fn.Results}
	}

	for _, fn := range f.Funcs {
		if err := c.checkFuncBody(fn); err != nil {
			return err
		}
	}
	return nil
}

// checkFuncSig validates fn's declared signature on its own, before any
// body is checked: its receiver (if any) must name a known struct, and
// every parameter/result type must be one validateType recognizes.
// Nullable return types would need a function's result to carry a
// companion isset flag across the CALL/RET boundary — the same 2-slot
// expansion codegen already does for a nullable *parameter* (see
// codegen's genCallArgs) — but nothing through Step 7 demonstrates it,
// and it's exactly what Step 11's `(T, error?)` convention needs, so it's
// deliberately deferred there rather than half-built now.
func (c *checker) checkFuncSig(fn *ast.FuncDecl) error {
	if fn.Receiver != nil {
		if _, _, err := c.receiverStructName(*fn.Receiver); err != nil {
			return err
		}
	}
	for _, p := range fn.Params {
		if err := c.validateType(p.Type, p.Line); err != nil {
			return err
		}
	}
	for _, r := range fn.Results {
		if needsRetIssetExpansion(r) {
			return fmt.Errorf("line %d: function %q: nullable return types are not supported yet", fn.Line, fn.Name)
		}
		if err := c.validateType(r, fn.Line); err != nil {
			return err
		}
	}
	return nil
}

// needsRetIssetExpansion reports whether a nullable result type t would
// need the same value+isset two-operand expansion across a CALL/RET
// boundary that a nullable *parameter* already gets (see codegen's
// identically-reasoned needsIssetSlot) — every nullable type except a
// pointer or a function value, both of which represent "none" via Go's
// native nil in a single operand, needing no flag at all. Nothing
// through Step 9 implements that expansion for *results* (see this
// function's callers, checkFuncSig and checkClosureLit), so only this
// narrower set is rejected — a pointer- or function-typed return was
// never blocked by the underlying problem in the first place (an
// ordinary single-value RET/CALL already handles it, exactly like any
// other non-nullable-shaped result).
func needsRetIssetExpansion(t ast.Type) bool {
	return t.Nullable && t.Ptr == nil && t.Func == nil
}

// receiverStructName resolves a receiver's declared type to the struct
// name it names (cascade_spec.md §8.2: either `Name` for a value receiver
// or `*Name` for a pointer receiver — nothing else is valid there).
func (c *checker) receiverStructName(p ast.Param) (name string, isPtr bool, err error) {
	t := p.Type
	if t.Ptr != nil {
		t = *t.Ptr
		isPtr = true
	}
	if t.Elem != nil || t.Name == "" {
		return "", false, fmt.Errorf("line %d: receiver type must be a struct or a pointer to a struct", p.Line)
	}
	if _, ok := c.structs[t.Name]; !ok {
		return "", false, fmt.Errorf("line %d: unknown struct type %q in receiver", p.Line, t.Name)
	}
	return t.Name, isPtr, nil
}

// checkFuncBody checks fn's body in a fresh scope pre-populated with its
// receiver (if any, cascade_spec.md §8.2) and parameters (§8.1) — §10's
// scoping rules apply to both the same as any other declaration,
// including rejecting a parameter that shadows the receiver's name.
func (c *checker) checkFuncBody(fn *ast.FuncDecl) error {
	sc := newScope(nil)
	if fn.Receiver != nil {
		if !sc.declareLocal(fn.Receiver.Name, varInfo{Type: fn.Receiver.Type}) {
			return fmt.Errorf("line %d: duplicate receiver name %q", fn.Receiver.Line, fn.Receiver.Name)
		}
	}
	for _, p := range fn.Params {
		if !sc.declareLocal(p.Name, varInfo{Type: p.Type}) {
			return fmt.Errorf("line %d: duplicate parameter name %q", p.Line, p.Name)
		}
	}
	for _, stmt := range fn.Body {
		if err := c.checkStmt(sc, stmt, fn.Results, 0, 0); err != nil {
			return err
		}
	}
	return nil
}

// validateType reports whether t names a type sema recognizes: a builtin
// scalar (int/float/string/bool), a list of a valid type, a pointer to a
// valid type, a function type whose every parameter/result is itself
// valid, a map type whose key is a non-nullable scalar and whose value is
// a valid non-nullable type, or a declared struct name (cascade_spec.md
// §2, §4.1). The parser accepts any identifier as a type name (see
// parseTypeBase) since it cannot know which ones are declared structs —
// that check happens here instead, once every struct is registered.
func (c *checker) validateType(t ast.Type, line int) error {
	if t.Elem != nil {
		return c.validateType(*t.Elem, line)
	}
	if t.Ptr != nil {
		return c.validateType(*t.Ptr, line)
	}
	if t.Func != nil {
		for _, p := range t.Func.Params {
			if err := c.validateType(p, line); err != nil {
				return err
			}
		}
		for _, r := range t.Func.Results {
			if err := c.validateType(r, line); err != nil {
				return err
			}
		}
		return nil
	}
	if t.Map != nil {
		if err := c.validateMapKeyType(t.Map.Key, line); err != nil {
			return err
		}
		if err := c.validateType(t.Map.Value, line); err != nil {
			return err
		}
		if t.Map.Value.Nullable {
			return fmt.Errorf("line %d: map value type cannot itself be nullable (m[k] already returns V?)", line)
		}
		return nil
	}
	switch t.Name {
	case "int", "float", "string", "bool":
		return nil
	}
	if _, ok := c.structs[t.Name]; ok {
		return nil
	}
	return fmt.Errorf("line %d: unknown type %q", line, t.Name)
}

// validateMapKeyType reports whether t is a valid map key type
// (cascade_spec.md §4.5: "map<K, V>のKにはスカラー型(int/float/string/bool)
// のみ使える") — a non-nullable int/float/string/bool, nothing else
// (struct/list/pointer/func/map, or a nullable scalar, are all rejected).
func (c *checker) validateMapKeyType(t ast.Type, line int) error {
	if !t.Nullable && t.Elem == nil && t.Ptr == nil && t.Func == nil && t.Map == nil {
		switch t.Name {
		case "int", "float", "string", "bool":
			return nil
		}
	}
	return fmt.Errorf("line %d: map key type must be a non-nullable scalar (int/float/string/bool), got %s", line, typeString(t))
}

// structTypeName reports the struct name t refers to, directly or
// through a pointer (cascade_spec.md §4.1/§8.2 — a field/method access
// works the same either way, see ast.FieldExpr's doc), and whether t
// itself was a pointer. ok is false for any non-struct type (scalar,
// list, or an unregistered name).
func (c *checker) structTypeName(t ast.Type) (name string, isPtr bool, ok bool) {
	if t.Ptr != nil {
		t = *t.Ptr
		isPtr = true
	}
	if t.Elem != nil || t.Name == "" {
		return "", false, false
	}
	if _, exists := c.structs[t.Name]; !exists {
		return "", false, false
	}
	return t.Name, isPtr, true
}

// isAddressable reports whether e is a valid operand for unary `&`
// (cascade_spec.md §6) or a value-receiver method call's implicit
// address-of (§8.2) — a plain variable only. Go itself allows the
// broader set of lvalues (a struct field, a list element, ...), but
// amivm's ADDR instruction accepts only a bare variable as its operand
// (the "variable" category, amivm_spec.md §6: $N/&N/%xxx/@xxx — never a
// field-path or index expression), so codegen has no way to honor
// anything wider even though Go's own semantics would allow it.
func isAddressable(e ast.Expr) bool {
	_, ok := e.(*ast.Ident)
	return ok
}

// checkStmt dispatches to one statement's own check function. want is the
// enclosing function's declared return types, needed by ReturnStmt.
// loopDepth counts enclosing while loops (continue requires loopDepth >
// 0); breakDepth counts enclosing while loops *and* switches (break
// requires breakDepth > 0) — cascade_spec.md §7: break exits the
// innermost loop or switch, but continue always skips over an enclosing
// switch to reach the innermost loop, since "switch自体はループではない".
func (c *checker) checkStmt(sc *scope, stmt ast.Stmt, want []ast.Type, loopDepth, breakDepth int) error {
	switch s := stmt.(type) {
	case *ast.LetDecl:
		return c.checkLetDecl(sc, s)
	case *ast.MultiLetDecl:
		return c.checkMultiLetDecl(sc, s)
	case *ast.AssignStmt:
		return c.checkAssignStmt(sc, s)
	case *ast.DerefAssignStmt:
		return c.checkDerefAssignStmt(sc, s)
	case *ast.CompoundAssignStmt:
		return c.checkCompoundAssignStmt(sc, s)
	case *ast.IncDecStmt:
		return c.checkIncDecStmt(sc, s)
	case *ast.ExprStmt:
		return c.checkExprStmt(sc, s)
	case *ast.ReturnStmt:
		return c.checkReturnStmt(sc, s, want)
	case *ast.IfStmt:
		return c.checkIfStmt(sc, s, want, loopDepth, breakDepth)
	case *ast.WhileStmt:
		return c.checkWhileStmt(sc, s, want, loopDepth, breakDepth)
	case *ast.SwitchStmt:
		return c.checkSwitchStmt(sc, s, want, loopDepth, breakDepth)
	case *ast.ForInStmt:
		return c.checkForInStmt(sc, s, want, loopDepth, breakDepth)
	case *ast.BreakStmt:
		if breakDepth == 0 {
			return fmt.Errorf("line %d: break outside of a loop or switch", s.Line)
		}
		return nil
	case *ast.ContinueStmt:
		if loopDepth == 0 {
			return fmt.Errorf("line %d: continue outside of a loop", s.Line)
		}
		return nil
	default:
		return fmt.Errorf("line %d: unsupported statement %T", ast.StmtLine(stmt), stmt)
	}
}

// checkBlock checks stmts in a fresh child scope of sc (cascade_spec.md
// §10: every `{ }` block gets its own scope).
func (c *checker) checkBlock(sc *scope, stmts []ast.Stmt, want []ast.Type, loopDepth, breakDepth int) error {
	return c.checkStmtsIn(newScope(sc), stmts, want, loopDepth, breakDepth)
}

// checkStmtsIn checks stmts directly in sc, with no new scope pushed —
// used where the caller already created (and possibly narrowed, see
// checkIfStmt) the scope stmts should run in.
func (c *checker) checkStmtsIn(sc *scope, stmts []ast.Stmt, want []ast.Type, loopDepth, breakDepth int) error {
	for _, stmt := range stmts {
		if err := c.checkStmt(sc, stmt, want, loopDepth, breakDepth); err != nil {
			return err
		}
	}
	return nil
}

// checkIfStmt checks an if/elif/else chain (cascade_spec.md §7): every
// condition must be a non-nullable bool, and each clause/else body is
// checked in its own scope with §2.3's null-narrowing applied where it
// can be determined statically from the clause's own condition (see
// narrowedVarInfo) — and, for the single-clause `if x is none { ... }`
// shape whose body always exits, propagated into sc itself for the rest
// of the enclosing block (cascade_spec.md §2.3's own example).
func (c *checker) checkIfStmt(sc *scope, stmt *ast.IfStmt, want []ast.Type, loopDepth, breakDepth int) error {
	for _, clause := range stmt.Clauses {
		ct, err := c.exprType(sc, clause.Cond)
		if err != nil {
			return err
		}
		if ct.Name != "bool" || ct.Nullable {
			return fmt.Errorf("line %d: if condition must be bool, got %s", ast.ExprLine(clause.Cond), typeString(ct))
		}

		inner := newScope(sc)
		if name, info, ok := narrowedVarInfo(sc, clause.Cond, true); ok {
			inner.shadow(name, info)
		}
		if err := c.checkStmtsIn(inner, clause.Body, want, loopDepth, breakDepth); err != nil {
			return err
		}
	}

	if stmt.Else != nil {
		inner := newScope(sc)
		if len(stmt.Clauses) == 1 {
			if name, info, ok := narrowedVarInfo(sc, stmt.Clauses[0].Cond, false); ok {
				inner.shadow(name, info)
			}
		}
		if err := c.checkStmtsIn(inner, stmt.Else, want, loopDepth, breakDepth); err != nil {
			return err
		}
	}

	if len(stmt.Clauses) == 1 && stmt.Else == nil {
		if name, info, ok := narrowedVarInfo(sc, stmt.Clauses[0].Cond, false); ok && endsInUnconditionalExit(stmt.Clauses[0].Body) {
			sc.shadow(name, info)
		}
	}

	return nil
}

// narrowedVarInfo returns the varInfo X would have if narrowed to
// non-null, when cond is exactly `X is none`/`X is not none`
// (cascade_spec.md §2.3, §7) on a plain identifier already declared (and
// nullable) in sc — wantNot selects which of the two (true for "is not
// none", false for "is none") cond must be for narrowing to apply.
//
// A pointer or a function value is deliberately excluded even though
// both are always Nullable (see ast.Type's doc): there is no distinct
// "non-nullable pointer"/"non-nullable func" type to narrow into — *T is
// *T and func(...): R is func(...): R whether or not either happens to
// be nil right now — so reconstructing ast.Type{Name: orig.Type.Name,
// ...} below would silently drop the Ptr/Func field and produce a
// broken, nameless type (a real bug caught by
// TestGenerate_PointerNullCheckUsesEQNilNotIssetFlag). Nothing is lost by
// skipping it: field/method access through a pointer is already
// unconditionally allowed regardless of nullness, mirroring Go's own "a
// nil-pointer method call only panics on actual dereference" behavior
// (see structTypeName's callers), and a closure call through a nil
// closure variable panics the same way at runtime regardless of
// narrowing — narrowing was never load-bearing for either in the first
// place.
func narrowedVarInfo(sc *scope, cond ast.Expr, wantNot bool) (name string, info varInfo, ok bool) {
	nc, isCheck := cond.(*ast.NullCheckExpr)
	if !isCheck || nc.Not != wantNot {
		return "", varInfo{}, false
	}
	id, isIdent := nc.X.(*ast.Ident)
	if !isIdent {
		return "", varInfo{}, false
	}
	orig, found := sc.lookup(id.Name)
	if !found || !orig.Type.Nullable || orig.Type.Ptr != nil || orig.Type.Func != nil {
		return "", varInfo{}, false
	}
	narrowed := orig
	narrowed.Type = ast.Type{Name: orig.Type.Name, Nullable: false}
	return id.Name, narrowed, true
}

// endsInUnconditionalExit reports whether body's last statement always
// leaves it (cascade_spec.md §2.3's narrowing condition: "`return`/
// `break`/`continue`で抜ける形になっている場合"). This is a syntactic
// check on the last statement only, not full control-flow analysis —
// matching the spec's own single-statement example (`if input is none {
// return }`).
func endsInUnconditionalExit(body []ast.Stmt) bool {
	if len(body) == 0 {
		return false
	}
	switch body[len(body)-1].(type) {
	case *ast.ReturnStmt, *ast.BreakStmt, *ast.ContinueStmt:
		return true
	default:
		return false
	}
}

func (c *checker) checkWhileStmt(sc *scope, stmt *ast.WhileStmt, want []ast.Type, loopDepth, breakDepth int) error {
	ct, err := c.exprType(sc, stmt.Cond)
	if err != nil {
		return err
	}
	if ct.Name != "bool" || ct.Nullable {
		return fmt.Errorf("line %d: while condition must be bool, got %s", ast.ExprLine(stmt.Cond), typeString(ct))
	}
	return c.checkBlock(sc, stmt.Body, want, loopDepth+1, breakDepth+1)
}

// checkSwitchStmt checks a tagged or untagged switch (cascade_spec.md
// §7). Tagged: each case value must match the tag's type exactly (no
// implicit conversion, same rule as every other comparison). Untagged:
// each case value is itself a bool condition. Every case/default body is
// checked in its own scope with breakDepth+1 (switch is not a loop, so
// loopDepth is unchanged — see checkStmt's doc).
func (c *checker) checkSwitchStmt(sc *scope, stmt *ast.SwitchStmt, want []ast.Type, loopDepth, breakDepth int) error {
	var tagType ast.Type
	if stmt.Tag != nil {
		t, err := c.exprType(sc, stmt.Tag)
		if err != nil {
			return err
		}
		if t.Nullable {
			return fmt.Errorf("line %d: switch tag must be non-nullable, got %s", ast.ExprLine(stmt.Tag), typeString(t))
		}
		tagType = t
	}

	for _, cs := range stmt.Cases {
		for _, v := range cs.Values {
			vt, err := c.exprType(sc, v)
			if err != nil {
				return err
			}
			if stmt.Tag != nil {
				if vt.Nullable || vt.Name != tagType.Name {
					return fmt.Errorf("line %d: case value type %s does not match switch tag type %s", ast.ExprLine(v), typeString(vt), typeString(tagType))
				}
			} else if vt.Nullable || vt.Name != "bool" {
				return fmt.Errorf("line %d: case condition must be bool, got %s", ast.ExprLine(v), typeString(vt))
			}
		}
		if err := c.checkBlock(sc, cs.Body, want, loopDepth, breakDepth+1); err != nil {
			return err
		}
	}

	if stmt.Default != nil {
		if err := c.checkBlock(sc, stmt.Default, want, loopDepth, breakDepth+1); err != nil {
			return err
		}
	}
	return nil
}

// checkForInStmt checks `for x in List { ... }` over a list, or `for k,
// v in M { ... }` over a map when ValueVarName is set (cascade_spec.md
// §7): List/M must be non-nullable, and the loop variable(s) are
// declared with their element (or key/value) type(s) in a fresh scope
// wrapping the body (loopDepth+1, breakDepth+1, same as while — see
// checkStmt's doc).
func (c *checker) checkForInStmt(sc *scope, stmt *ast.ForInStmt, want []ast.Type, loopDepth, breakDepth int) error {
	t, err := c.exprType(sc, stmt.List)
	if err != nil {
		return err
	}
	if t.Nullable {
		return fmt.Errorf("line %d: for-in requires a list or map, got %s", ast.ExprLine(stmt.List), typeString(t))
	}

	if stmt.ValueVarName != "" {
		if t.Map == nil {
			return fmt.Errorf("line %d: for-in with two variables requires a map, got %s", ast.ExprLine(stmt.List), typeString(t))
		}
		stmt.ElemType = t.Map.Key
		stmt.ValueType = t.Map.Value
		inner := newScope(sc)
		if !inner.declareLocal(stmt.VarName, varInfo{Type: stmt.ElemType}) {
			return fmt.Errorf("line %d: %q is already declared in this scope (cascade_spec.md §10)", stmt.Line, stmt.VarName)
		}
		if !inner.declareLocal(stmt.ValueVarName, varInfo{Type: stmt.ValueType}) {
			return fmt.Errorf("line %d: %q is already declared in this scope (cascade_spec.md §10)", stmt.Line, stmt.ValueVarName)
		}
		return c.checkStmtsIn(inner, stmt.Body, want, loopDepth+1, breakDepth+1)
	}

	if t.Elem == nil {
		return fmt.Errorf("line %d: for-in requires a list, got %s", ast.ExprLine(stmt.List), typeString(t))
	}
	stmt.ElemType = *t.Elem

	inner := newScope(sc)
	inner.declareLocal(stmt.VarName, varInfo{Type: stmt.ElemType})
	return c.checkStmtsIn(inner, stmt.Body, want, loopDepth+1, breakDepth+1)
}

// checkCompoundAssignStmt validates `name op= value` (cascade_spec.md
// §5) by reusing binaryResultType as if it were `name = name op value` —
// the same rules apply (e.g. `+=` on a string concatenates, `%=` requires
// int), so there is no separate rule set to maintain.
func (c *checker) checkCompoundAssignStmt(sc *scope, stmt *ast.CompoundAssignStmt) error {
	info, ok := sc.lookup(stmt.Name)
	if !ok {
		return fmt.Errorf("line %d: undefined name %q", stmt.Line, stmt.Name)
	}
	if info.Const {
		return fmt.Errorf("line %d: cannot assign to %q (declared const)", stmt.Line, stmt.Name)
	}
	vt, err := c.exprType(sc, stmt.Value)
	if err != nil {
		return err
	}
	rt, err := binaryResultType(stmt.Op, info.Type, vt, stmt.Line)
	if err != nil {
		return err
	}
	if rt.Name != info.Type.Name {
		return fmt.Errorf("line %d: cannot assign %s to %s", stmt.Line, typeString(rt), typeString(info.Type))
	}
	return nil
}

// checkIncDecStmt validates `name++`/`name--` (cascade_spec.md §5): name
// must be a non-nullable, non-const int or float.
func (c *checker) checkIncDecStmt(sc *scope, stmt *ast.IncDecStmt) error {
	info, ok := sc.lookup(stmt.Name)
	if !ok {
		return fmt.Errorf("line %d: undefined name %q", stmt.Line, stmt.Name)
	}
	if info.Const {
		return fmt.Errorf("line %d: cannot assign to %q (declared const)", stmt.Line, stmt.Name)
	}
	if info.Type.Nullable || (info.Type.Name != "int" && info.Type.Name != "float") {
		return fmt.Errorf("line %d: %s requires a non-nullable int or float, got %s", stmt.Line, stmt.Op, typeString(info.Type))
	}
	return nil
}

// checkLetDecl validates a `let`/`const` declaration (cascade_spec.md
// §4.2) and records its resolved type both in sc (for later statements to
// look up) and on the AST node itself (ResolvedType, so codegen doesn't
// have to re-infer it — see LetDecl's doc comment).
func (c *checker) checkLetDecl(sc *scope, decl *ast.LetDecl) error {
	if decl.Const && decl.Init == nil {
		return fmt.Errorf("line %d: 'const %s' requires an initializer", decl.Line, decl.Name)
	}

	typ := decl.Type
	if !typeGiven(typ) {
		// `let x = Init`: the type is inferred entirely from Init, which
		// rules out `let x = none` (§2.3's whole point is that `T?` must
		// be written explicitly) — exprType already rejects a bare
		// NoneLit for exactly this reason.
		if decl.Init == nil {
			return fmt.Errorf("line %d: 'let %s' needs either a type or an initializer", decl.Line, decl.Name)
		}
		t, err := c.exprType(sc, decl.Init)
		if err != nil {
			return err
		}
		typ = t
	} else {
		if err := c.validateType(typ, decl.Line); err != nil {
			return err
		}
		if decl.Init != nil {
			if err := c.checkAssignable(sc, typ, decl.Init); err != nil {
				return err
			}
		}
	}
	decl.ResolvedType = typ

	if !sc.declareLocal(decl.Name, varInfo{Type: typ, Const: decl.Const}) {
		return fmt.Errorf("line %d: %q is already declared in this scope (cascade_spec.md §10)", decl.Line, decl.Name)
	}
	return nil
}

// checkMultiLetDecl validates a multi-value `let`/`const` declaration
// (cascade_spec.md §5, §8.5): Init's callee must be a known function
// (sema has no way to know a value-typed expression's "arity" beyond a
// direct call — the parser already enforces this shape, see
// parseMultiLetDecl) returning exactly as many values as there are
// Names. "_" entries are validated like any other but declare nothing
// (cascade_spec.md §5).
func (c *checker) checkMultiLetDecl(sc *scope, decl *ast.MultiLetDecl) error {
	sig, err := c.callSig(sc, decl.Init)
	if err != nil {
		return err
	}
	if err := c.checkCallArgs(sc, decl.Init, sig); err != nil {
		return err
	}
	if len(sig.Results) != len(decl.Names) {
		return fmt.Errorf("line %d: %s() returns %d value(s), but %d name(s) given", decl.Init.Line, decl.Init.Callee, len(sig.Results), len(decl.Names))
	}
	decl.ResolvedTypes = sig.Results

	for i, name := range decl.Names {
		if name == "_" {
			continue
		}
		if !sc.declareLocal(name, varInfo{Type: sig.Results[i], Const: decl.Const}) {
			return fmt.Errorf("line %d: %q is already declared in this scope (cascade_spec.md §10)", decl.Line, name)
		}
	}
	return nil
}

// checkAssignStmt validates `name = value`; `name[Index] = value` when
// Index is set; or `name.Field = value` when Field is set
// (cascade_spec.md §5). Both Index and Field assignment mutate existing
// content rather than rebinding the variable itself, so both are allowed
// even on a `const`-declared variable (only bare `name = ...` is
// forbidden there).
func (c *checker) checkAssignStmt(sc *scope, stmt *ast.AssignStmt) error {
	info, ok := sc.lookup(stmt.Name)
	if !ok {
		return fmt.Errorf("line %d: undefined name %q", stmt.Line, stmt.Name)
	}
	if stmt.Index != nil {
		if info.Type.Nullable {
			return fmt.Errorf("line %d: cannot index into %s", stmt.Line, typeString(info.Type))
		}
		if info.Type.Map != nil {
			kt, err := c.exprType(sc, stmt.Index)
			if err != nil {
				return err
			}
			if kt.Nullable || !typeShapeEqual(kt, info.Type.Map.Key) {
				return fmt.Errorf("line %d: map key must be %s, got %s", ast.ExprLine(stmt.Index), typeString(info.Type.Map.Key), typeString(kt))
			}
			return c.checkAssignable(sc, info.Type.Map.Value, stmt.Value)
		}
		if info.Type.Elem == nil {
			return fmt.Errorf("line %d: cannot index into %s", stmt.Line, typeString(info.Type))
		}
		it, err := c.exprType(sc, stmt.Index)
		if err != nil {
			return err
		}
		if it.Nullable || it.Name != "int" {
			return fmt.Errorf("line %d: list index must be int, got %s", ast.ExprLine(stmt.Index), typeString(it))
		}
		return c.checkAssignable(sc, *info.Type.Elem, stmt.Value)
	}
	if stmt.Field != "" {
		name, isPtr, ok := c.structTypeName(info.Type)
		if !ok {
			return fmt.Errorf("line %d: cannot access field %q on non-struct type %s", stmt.Line, stmt.Field, typeString(info.Type))
		}
		if !isPtr && info.Type.Nullable {
			return fmt.Errorf("line %d: cannot access a field of a nullable struct value %s (narrow with 'is not none' first)", stmt.Line, typeString(info.Type))
		}
		fieldType, found := structFieldType(c.structs[name], stmt.Field)
		if !found {
			return fmt.Errorf("line %d: struct %q has no field %q", stmt.Line, name, stmt.Field)
		}
		return c.checkAssignable(sc, fieldType, stmt.Value)
	}
	if info.Const {
		return fmt.Errorf("line %d: cannot assign to %q (declared const)", stmt.Line, stmt.Name)
	}
	return c.checkAssignable(sc, info.Type, stmt.Value)
}

// checkDerefAssignStmt validates `*Ptr = Value` (cascade_spec.md §5):
// Ptr must have a pointer type, and Value must be assignable to its
// pointee type.
func (c *checker) checkDerefAssignStmt(sc *scope, stmt *ast.DerefAssignStmt) error {
	pt, err := c.exprType(sc, stmt.Ptr)
	if err != nil {
		return err
	}
	if pt.Ptr == nil {
		return fmt.Errorf("line %d: cannot dereference non-pointer type %s", ast.ExprLine(stmt.Ptr), typeString(pt))
	}
	return c.checkAssignable(sc, *pt.Ptr, stmt.Value)
}

// checkExprStmt validates a call-expression statement: the `print`
// builtin (cascade_spec.md §13), or a call to any known function
// (cascade_spec.md §8.1) — a statement discards whatever it returns
// (zero, one, or many values), so unlike checkCallExprValue there is no
// result-count restriction here.
func (c *checker) checkExprStmt(sc *scope, stmt *ast.ExprStmt) error {
	call, ok := stmt.X.(*ast.CallExpr)
	if !ok {
		return fmt.Errorf("line %d: expected a call expression", ast.ExprLine(stmt.X))
	}
	if call.Receiver != nil {
		sig, err := c.resolveMethodCall(sc, call)
		if err != nil {
			return err
		}
		return c.checkCallArgs(sc, call, sig)
	}
	if sig, ok, err := c.resolveClosureCall(sc, call); err != nil {
		return err
	} else if ok {
		return c.checkCallArgs(sc, call, sig)
	}
	switch call.Callee {
	case "print":
		if len(call.Args) != 1 {
			return fmt.Errorf("line %d: print expects exactly 1 argument, got %d", call.Line, len(call.Args))
		}
		t, err := c.exprType(sc, call.Args[0])
		if err != nil {
			return err
		}
		if t.Name != "string" || t.Nullable {
			return fmt.Errorf("line %d: print expects string, got %s", call.Line, typeString(t))
		}
		return nil
	case "delete":
		if len(call.Args) != 2 {
			return fmt.Errorf("line %d: delete() expects exactly 2 arguments, got %d", call.Line, len(call.Args))
		}
		mt, err := c.exprType(sc, call.Args[0])
		if err != nil {
			return err
		}
		if mt.Nullable || mt.Map == nil {
			return fmt.Errorf("line %d: delete() expects a map as its first argument, got %s", ast.ExprLine(call.Args[0]), typeString(mt))
		}
		kt, err := c.exprType(sc, call.Args[1])
		if err != nil {
			return err
		}
		if kt.Nullable || !typeShapeEqual(kt, mt.Map.Key) {
			return fmt.Errorf("line %d: delete() key must be %s, got %s", ast.ExprLine(call.Args[1]), typeString(mt.Map.Key), typeString(kt))
		}
		return nil
	default:
		sig, ok := c.sigs[call.Callee]
		if !ok {
			return fmt.Errorf("line %d: unsupported call to %q", call.Line, call.Callee)
		}
		return c.checkCallArgs(sc, call, sig)
	}
}

func (c *checker) checkReturnStmt(sc *scope, stmt *ast.ReturnStmt, want []ast.Type) error {
	if len(stmt.Results) != len(want) {
		return fmt.Errorf("line %d: expected %d return value(s), got %d", stmt.Line, len(want), len(stmt.Results))
	}
	for i, r := range stmt.Results {
		if err := c.checkAssignable(sc, want[i], r); err != nil {
			return err
		}
	}
	return nil
}

// checkAssignable validates that value may be assigned/initialized into a
// variable of type target (cascade_spec.md §2.3, §5, §3): `none` is only
// valid against a nullable target; a list literal is checked recursively
// against target's element type (see checkListLiteralAgainst) rather than
// through the general exprType path, since an empty `[]` (and, less
// obviously, a non-empty one) has no type of its own without a target —
// mirroring how NoneLit is handled; otherwise value's own type must have
// target's exact shape (ignoring nullability — Cascade never does
// implicit conversion, and §6's note on `+` makes the same point for
// operators), and a nullable value may only widen into a nullable target,
// never narrow implicitly into a non-nullable one (narrowing requires an
// explicit `is not none` check, §2.3). This same function doubles as
// argument-type checking for a function call (see checkCallArgs) — a
// parameter is assigned into exactly the same way a `let`/`return` target
// is.
func (c *checker) checkAssignable(sc *scope, target ast.Type, value ast.Expr) error {
	if _, isNone := value.(*ast.NoneLit); isNone {
		if !target.Nullable {
			return fmt.Errorf("line %d: cannot assign 'none' to non-nullable type %s", ast.ExprLine(value), typeString(target))
		}
		return nil
	}
	if lit, isList := value.(*ast.ListLit); isList {
		return c.checkListLiteralAgainst(sc, lit, target)
	}
	if lit, isMap := value.(*ast.MapLit); isMap {
		return c.checkMapLiteralAgainst(sc, lit, target)
	}
	vt, err := c.exprType(sc, value)
	if err != nil {
		return err
	}
	if vt.Nullable && !target.Nullable {
		return fmt.Errorf("line %d: cannot assign %s to non-nullable %s (narrow with 'is not none' first)", ast.ExprLine(value), typeString(vt), typeString(target))
	}
	if !typeShapeEqual(vt, target) {
		return fmt.Errorf("line %d: cannot assign %s to %s", ast.ExprLine(value), typeString(vt), typeString(target))
	}
	return nil
}

// checkCallArgs validates call's arguments against sig's parameter types
// (cascade_spec.md §8.1): the argument count must match, and each
// argument must be assignable to its corresponding parameter type (see
// checkAssignable — a call argument is checked exactly like any other
// assignment, including nullable widening).
func (c *checker) checkCallArgs(sc *scope, call *ast.CallExpr, sig funcSig) error {
	if len(call.Args) != len(sig.Params) {
		return fmt.Errorf("line %d: %s() expects %d argument(s), got %d", call.Line, call.Callee, len(sig.Params), len(call.Args))
	}
	for i, arg := range call.Args {
		if err := c.checkAssignable(sc, sig.Params[i], arg); err != nil {
			return err
		}
	}
	return nil
}

// checkListAndHOFArg validates the shape every filter/map/reduce call
// shares (cascade_spec.md §8.4, §13): exactly wantArgs arguments, a
// non-nullable list at listArgIdx, and a function value at hofArgIdx.
// The specific parameter/result shape each builtin's own function
// argument must have — which differs between filter/map's one-parameter
// predicate/mapper and reduce's two-parameter folder — is left to the
// caller to check against the returned *ast.FuncType.
func (c *checker) checkListAndHOFArg(sc *scope, call *ast.CallExpr, name string, wantArgs, listArgIdx, hofArgIdx int) (ast.Type, *ast.FuncType, error) {
	if len(call.Args) != wantArgs {
		return ast.Type{}, nil, fmt.Errorf("line %d: %s() expects exactly %d argument(s), got %d", call.Line, name, wantArgs, len(call.Args))
	}
	lt, err := c.exprType(sc, call.Args[listArgIdx])
	if err != nil {
		return ast.Type{}, nil, err
	}
	if lt.Nullable || lt.Elem == nil {
		return ast.Type{}, nil, fmt.Errorf("line %d: %s() expects a list as its first argument, got %s", ast.ExprLine(call.Args[listArgIdx]), name, typeString(lt))
	}
	ft, err := c.exprType(sc, call.Args[hofArgIdx])
	if err != nil {
		return ast.Type{}, nil, err
	}
	if ft.Func == nil {
		return ast.Type{}, nil, fmt.Errorf("line %d: %s() expects a function as its last argument, got %s", ast.ExprLine(call.Args[hofArgIdx]), name, typeString(ft))
	}
	return lt, ft.Func, nil
}

// resolveMethodCall resolves a method call's receiver (cascade_spec.md
// §8.2): the receiver expression's type must name a known struct
// (directly or through a pointer), that struct must declare a method
// named call.Callee, and — when the receiver's own value/pointer kind
// doesn't already match the method's declared receiver kind — the
// mismatch must be bridgeable automatically: a value receiver expression
// used where a pointer receiver is wanted must be addressable (§6;
// codegen emits an implicit ADDR, see CallExpr.RecvNeedsAddr), while a
// pointer receiver expression used where a value receiver is wanted is
// always bridgeable (an implicit PGET, see RecvNeedsDeref). The
// resolution is recorded onto call itself so codegen doesn't have to redo
// it.
func (c *checker) resolveMethodCall(sc *scope, call *ast.CallExpr) (funcSig, error) {
	rt, err := c.exprType(sc, call.Receiver)
	if err != nil {
		return funcSig{}, err
	}
	name, isPtr, ok := c.structTypeName(rt)
	if !ok {
		return funcSig{}, fmt.Errorf("line %d: cannot call a method on non-struct type %s", call.Line, typeString(rt))
	}
	if !isPtr && rt.Nullable {
		return funcSig{}, fmt.Errorf("line %d: cannot call a method on a nullable struct value %s (narrow with 'is not none' first)", call.Line, typeString(rt))
	}
	sig, ok := c.methods[name][call.Callee]
	if !ok {
		return funcSig{}, fmt.Errorf("line %d: struct %q has no method %q", call.Line, name, call.Callee)
	}
	wantPtr := sig.RecvType.Ptr != nil
	switch {
	case wantPtr && !isPtr:
		if !isAddressable(call.Receiver) {
			return funcSig{}, fmt.Errorf("line %d: cannot take the address of this expression to call %s.%s (pointer receiver)", call.Line, name, call.Callee)
		}
		call.RecvNeedsAddr = true
	case !wantPtr && isPtr:
		call.RecvNeedsDeref = true
	}
	call.RecvStruct = name
	return funcSig{Params: sig.Params, Results: sig.Results}, nil
}

// callSig resolves call's signature — a method call (Receiver non-nil), a
// closure-valued local variable, or a plain global function, in that
// priority order — shared by checkMultiLetDecl, the one call site that
// needs a signature without also wanting checkCallExprValue/
// checkExprStmt's single-/void-value restrictions.
func (c *checker) callSig(sc *scope, call *ast.CallExpr) (funcSig, error) {
	if call.Receiver != nil {
		return c.resolveMethodCall(sc, call)
	}
	if sig, ok, err := c.resolveClosureCall(sc, call); err != nil {
		return funcSig{}, err
	} else if ok {
		return sig, nil
	}
	sig, ok := c.sigs[call.Callee]
	if !ok {
		return funcSig{}, fmt.Errorf("line %d: undefined function %q", call.Line, call.Callee)
	}
	return sig, nil
}

// checkListLiteralAgainst validates a list literal's elements against
// target's element type (cascade_spec.md §3, §4.3). target must itself be
// a list type — an empty `[]` is valid here (the loop simply doesn't
// run), unlike through exprType's context-free inference.
func (c *checker) checkListLiteralAgainst(sc *scope, lit *ast.ListLit, target ast.Type) error {
	if target.Elem == nil {
		return fmt.Errorf("line %d: cannot assign a list literal to non-list type %s", lit.Line, typeString(target))
	}
	for _, e := range lit.Elems {
		if err := c.checkAssignable(sc, *target.Elem, e); err != nil {
			return err
		}
	}
	return nil
}

// checkMapLiteralAgainst validates a map literal's pairs against target's
// key/value types (cascade_spec.md §3, §4.5). target must itself be a map
// type — an empty `{}` is valid here (the loop simply doesn't run),
// unlike through exprType's context-free inference (see checkMapLit).
func (c *checker) checkMapLiteralAgainst(sc *scope, lit *ast.MapLit, target ast.Type) error {
	if target.Map == nil {
		return fmt.Errorf("line %d: cannot assign a map literal to non-map type %s", lit.Line, typeString(target))
	}
	for _, pair := range lit.Pairs {
		if err := c.checkAssignable(sc, target.Map.Key, pair.Key); err != nil {
			return err
		}
		if err := c.checkAssignable(sc, target.Map.Value, pair.Value); err != nil {
			return err
		}
	}
	return nil
}

// checkMapLit infers a map literal's type with no target context
// (cascade_spec.md §3), by inferring its key/value types from Pairs[0]
// and then checking every other pair matches — mirrors inferListLitType.
// An empty `{}` has nothing to infer from and is always an error here —
// callers with a target type instead go through checkMapLiteralAgainst,
// which allows an empty literal.
func (c *checker) checkMapLit(sc *scope, lit *ast.MapLit) (ast.Type, error) {
	if len(lit.Pairs) == 0 {
		return ast.Type{}, fmt.Errorf("line %d: cannot infer a type from an empty map literal; give it an explicit map<K, V> type", lit.Line)
	}
	keyType, err := c.exprType(sc, lit.Pairs[0].Key)
	if err != nil {
		return ast.Type{}, err
	}
	valueType, err := c.exprType(sc, lit.Pairs[0].Value)
	if err != nil {
		return ast.Type{}, err
	}
	target := ast.Type{Map: &ast.MapType{Key: keyType, Value: valueType}}
	if err := c.checkMapLiteralAgainst(sc, lit, target); err != nil {
		return ast.Type{}, err
	}
	return target, nil
}

// checkStructLit validates a struct literal (cascade_spec.md §4.1): every
// field the struct declares must be given exactly once, with a value
// assignable to that field's declared type — see ast.StructLit's doc for
// why there is no partial/defaulted-field form.
func (c *checker) checkStructLit(sc *scope, lit *ast.StructLit) (ast.Type, error) {
	sd, ok := c.structs[lit.TypeName]
	if !ok {
		return ast.Type{}, fmt.Errorf("line %d: unknown struct type %q", lit.Line, lit.TypeName)
	}
	given := map[string]bool{}
	for _, fi := range lit.Fields {
		if given[fi.Name] {
			return ast.Type{}, fmt.Errorf("line %d: field %q given more than once", lit.Line, fi.Name)
		}
		given[fi.Name] = true
		fieldType, found := structFieldType(sd, fi.Name)
		if !found {
			return ast.Type{}, fmt.Errorf("line %d: struct %q has no field %q", lit.Line, lit.TypeName, fi.Name)
		}
		if err := c.checkAssignable(sc, fieldType, fi.Value); err != nil {
			return ast.Type{}, err
		}
	}
	for _, f := range sd.Fields {
		if !given[f.Name] {
			return ast.Type{}, fmt.Errorf("line %d: struct literal %q is missing field %q", lit.Line, lit.TypeName, f.Name)
		}
	}
	return ast.Type{Name: lit.TypeName}, nil
}

// checkClosureLit validates a closure literal (cascade_spec.md §8.3):
// every parameter/result type must be one validateType recognizes (the
// same restriction checkFuncSig applies to a top-level function), and its
// body is checked exactly like a function body (checkFuncBody) with two
// differences — the closure's own scope is a *child* of sc rather than a
// fresh root scope, so the body can read and mutate every surrounding-
// scope variable it references (real Go closure capture, requiring no
// separate capture-analysis pass here — see CLAUDE.md's "確定した設計判
// 断"), and loopDepth/breakDepth reset to 0 (a closure body is its own
// function-like boundary: `break`/`continue` inside it can never reach a
// loop outside it, mirroring Go's own func-literal semantics). Rejects a
// closure literal found while already checking another closure's body —
// see checker.closureDepth's doc for why.
func (c *checker) checkClosureLit(sc *scope, lit *ast.ClosureLit) (ast.Type, error) {
	if c.closureDepth > 0 {
		return ast.Type{}, fmt.Errorf("line %d: a closure literal cannot itself contain another closure literal (amivm's CLOS cannot nest)", lit.Line)
	}

	paramTypes := make([]ast.Type, len(lit.Params))
	inner := newScope(sc)
	for i, p := range lit.Params {
		if err := c.validateType(p.Type, p.Line); err != nil {
			return ast.Type{}, err
		}
		paramTypes[i] = p.Type
		if !inner.declareLocal(p.Name, varInfo{Type: p.Type}) {
			return ast.Type{}, fmt.Errorf("line %d: duplicate parameter name %q", p.Line, p.Name)
		}
	}
	for _, r := range lit.Results {
		if needsRetIssetExpansion(r) {
			return ast.Type{}, fmt.Errorf("line %d: closure literal: nullable return types are not supported yet", lit.Line)
		}
		if err := c.validateType(r, lit.Line); err != nil {
			return ast.Type{}, err
		}
	}

	c.closureDepth++
	err := c.checkStmtsIn(inner, lit.Body, lit.Results, 0, 0)
	c.closureDepth--
	if err != nil {
		return ast.Type{}, err
	}

	rt := ast.Type{Func: &ast.FuncType{Params: paramTypes, Results: lit.Results}, Nullable: true}
	lit.ResolvedType = rt
	return rt, nil
}

// resolveClosureCall reports whether call.Callee names a closure-valued
// local variable (rather than a builtin or a global function) — if so,
// it takes priority over both, matching cascade_spec.md §10's ordinary
// shadowing rules extended to the "callable" namespace, and its
// signature is returned with ok=true; ok=false with a nil error means
// call.Callee isn't in scope at all, letting the caller fall through to
// builtin/global-function dispatch as usual. A same-named variable that
// isn't a function value is a hard error either way — no silent
// fallthrough to some other same-named callable, since writing `x(...)`
// for a non-callable local `x` is almost certainly a mistake.
func (c *checker) resolveClosureCall(sc *scope, call *ast.CallExpr) (sig funcSig, ok bool, err error) {
	info, found := sc.lookup(call.Callee)
	if !found {
		return funcSig{}, false, nil
	}
	if info.Type.Func == nil {
		return funcSig{}, false, fmt.Errorf("line %d: %q is not callable (%s)", call.Line, call.Callee, typeString(info.Type))
	}
	call.IsClosureVar = true
	return funcSig{Params: info.Type.Func.Params, Results: info.Type.Func.Results}, true, nil
}

// structFieldType looks up name among sd's declared fields.
func structFieldType(sd *ast.StructDecl, name string) (ast.Type, bool) {
	for _, f := range sd.Fields {
		if f.Name == name {
			return f.Type, true
		}
	}
	return ast.Type{}, false
}

// typeShapeEqual reports whether a and b are the same type, ignoring
// Nullable (checkAssignable already handles nullability separately: a
// non-nullable value may widen into a nullable target, but never the
// reverse). Elem/Ptr/Func are compared recursively (a bare a.Name == b.Name
// check would wrongly treat every pointer — or every func type — as
// shape-equal to every other, since neither has a Name of its own; a
// real bug this fixes, caught while adding func-type support and
// verified to have been latent since Ptr landed in Step 8 — see
// CLAUDE.md's "確定した設計判断").
func typeShapeEqual(a, b ast.Type) bool {
	if (a.Elem == nil) != (b.Elem == nil) {
		return false
	}
	if a.Elem != nil {
		return typeShapeEqual(*a.Elem, *b.Elem)
	}
	if (a.Ptr == nil) != (b.Ptr == nil) {
		return false
	}
	if a.Ptr != nil {
		return typeShapeEqual(*a.Ptr, *b.Ptr)
	}
	if (a.Func == nil) != (b.Func == nil) {
		return false
	}
	if a.Func != nil {
		return funcTypeShapeEqual(*a.Func, *b.Func)
	}
	if (a.Map == nil) != (b.Map == nil) {
		return false
	}
	if a.Map != nil {
		return typeShapeEqual(a.Map.Key, b.Map.Key) && typeShapeEqual(a.Map.Value, b.Map.Value)
	}
	return a.Name == b.Name
}

// funcTypeShapeEqual reports whether a and b have the same parameter and
// result types, ignoring each one's own Nullable exactly like
// typeShapeEqual does for every other type.
func funcTypeShapeEqual(a, b ast.FuncType) bool {
	if len(a.Params) != len(b.Params) || len(a.Results) != len(b.Results) {
		return false
	}
	for i := range a.Params {
		if !typeShapeEqual(a.Params[i], b.Params[i]) {
			return false
		}
	}
	for i := range a.Results {
		if !typeShapeEqual(a.Results[i], b.Results[i]) {
			return false
		}
	}
	return true
}

// typeGiven reports whether t is an explicitly written type (`let x: T`)
// as opposed to the zero Type `let x = Init` leaves for inference — a
// list, pointer, function, or map type has an empty Name, so checking
// Name alone isn't enough once those exist.
func typeGiven(t ast.Type) bool {
	return t.Name != "" || t.Elem != nil || t.Ptr != nil || t.Func != nil || t.Map != nil
}

// exprType infers e's type. NoneLit has no type of its own (see its doc
// comment) and always fails here — callers that accept `none`
// (checkAssignable) special-case it before calling exprType.
func (c *checker) exprType(sc *scope, e ast.Expr) (ast.Type, error) {
	switch v := e.(type) {
	case *ast.StringLit:
		return ast.Type{Name: "string"}, nil
	case *ast.IntLit:
		return ast.Type{Name: "int"}, nil
	case *ast.FloatLit:
		return ast.Type{Name: "float"}, nil
	case *ast.BoolLit:
		return ast.Type{Name: "bool"}, nil
	case *ast.NoneLit:
		return ast.Type{}, fmt.Errorf("line %d: cannot infer a type from 'none' alone; give the variable an explicit nullable type (e.g. 'T?')", v.Line)
	case *ast.ListLit:
		return c.inferListLitType(sc, v)
	case *ast.MapLit:
		return c.checkMapLit(sc, v)
	case *ast.IndexExpr:
		xt, err := c.exprType(sc, v.X)
		if err != nil {
			return ast.Type{}, err
		}
		if xt.Nullable {
			return ast.Type{}, fmt.Errorf("line %d: cannot index into %s", v.Line, typeString(xt))
		}
		if xt.Map != nil {
			kt, err := c.exprType(sc, v.Index)
			if err != nil {
				return ast.Type{}, err
			}
			if kt.Nullable || !typeShapeEqual(kt, xt.Map.Key) {
				return ast.Type{}, fmt.Errorf("line %d: map key must be %s, got %s", ast.ExprLine(v.Index), typeString(xt.Map.Key), typeString(kt))
			}
			// m[k] always returns V? (cascade_spec.md §4.5's comma-ok
			// nullability), regardless of whether V itself was declared
			// nullable — validateType already forbids V from being
			// independently nullable, so this is the only nullability
			// layer m[k] can ever have.
			resultType := xt.Map.Value
			resultType.Nullable = true
			v.ResultType = resultType
			return resultType, nil
		}
		if xt.Elem == nil {
			return ast.Type{}, fmt.Errorf("line %d: cannot index into %s", v.Line, typeString(xt))
		}
		it, err := c.exprType(sc, v.Index)
		if err != nil {
			return ast.Type{}, err
		}
		if it.Nullable || it.Name != "int" {
			return ast.Type{}, fmt.Errorf("line %d: list index must be int, got %s", ast.ExprLine(v.Index), typeString(it))
		}
		v.ResultType = *xt.Elem
		return *xt.Elem, nil
	case *ast.Ident:
		info, ok := sc.lookup(v.Name)
		if !ok {
			return ast.Type{}, fmt.Errorf("line %d: undefined name %q", v.Line, v.Name)
		}
		return info.Type, nil
	case *ast.CallExpr:
		return c.checkCallExprValue(sc, v)
	case *ast.ClosureLit:
		return c.checkClosureLit(sc, v)
	case *ast.StructLit:
		return c.checkStructLit(sc, v)
	case *ast.FieldExpr:
		xt, err := c.exprType(sc, v.X)
		if err != nil {
			return ast.Type{}, err
		}
		name, isPtr, ok := c.structTypeName(xt)
		if !ok {
			return ast.Type{}, fmt.Errorf("line %d: cannot access field %q on non-struct type %s", v.Line, v.Field, typeString(xt))
		}
		if !isPtr && xt.Nullable {
			return ast.Type{}, fmt.Errorf("line %d: cannot access a field of a nullable struct value %s (narrow with 'is not none' first)", v.Line, typeString(xt))
		}
		fieldType, found := structFieldType(c.structs[name], v.Field)
		if !found {
			return ast.Type{}, fmt.Errorf("line %d: struct %q has no field %q", v.Line, name, v.Field)
		}
		v.ResultType = fieldType
		return fieldType, nil
	case *ast.UnaryExpr:
		// "&" (address-of) and "*" (dereference) don't fit
		// unaryResultType's generic scalar-operator shape — see
		// isAddressable's doc and ast.Type's pointer-nullability doc.
		if v.Op == "&" {
			if !isAddressable(v.X) {
				return ast.Type{}, fmt.Errorf("line %d: cannot take the address of this expression", v.Line)
			}
			xt, err := c.exprType(sc, v.X)
			if err != nil {
				return ast.Type{}, err
			}
			rt := ast.Type{Ptr: &xt, Nullable: true}
			v.ResultType = rt
			return rt, nil
		}
		if v.Op == "*" {
			xt, err := c.exprType(sc, v.X)
			if err != nil {
				return ast.Type{}, err
			}
			if xt.Ptr == nil {
				return ast.Type{}, fmt.Errorf("line %d: cannot dereference non-pointer type %s", v.Line, typeString(xt))
			}
			rt := *xt.Ptr
			v.ResultType = rt
			return rt, nil
		}
		xt, err := c.exprType(sc, v.X)
		if err != nil {
			return ast.Type{}, err
		}
		rt, err := unaryResultType(v.Op, xt, v.Line)
		if err != nil {
			return ast.Type{}, err
		}
		v.ResultType = rt
		return rt, nil
	case *ast.BinaryExpr:
		xt, err := c.exprType(sc, v.X)
		if err != nil {
			return ast.Type{}, err
		}
		yt, err := c.exprType(sc, v.Y)
		if err != nil {
			return ast.Type{}, err
		}
		rt, err := binaryResultType(v.Op, xt, yt, v.Line)
		if err != nil {
			return ast.Type{}, err
		}
		v.ResultType = rt
		return rt, nil
	case *ast.NullCheckExpr:
		xt, err := c.exprType(sc, v.X)
		if err != nil {
			return ast.Type{}, err
		}
		if !xt.Nullable {
			desc := "is none"
			if v.Not {
				desc = "is not none"
			}
			return ast.Type{}, fmt.Errorf("line %d: '%s' requires a nullable type, got %s", v.Line, desc, typeString(xt))
		}
		return ast.Type{Name: "bool"}, nil
	default:
		return ast.Type{}, fmt.Errorf("line %d: cannot determine the type of this expression yet", ast.ExprLine(e))
	}
}

// inferListLitType infers a list literal's type with no target context
// (cascade_spec.md §3), by inferring its element type from Elems[0] and
// then checking every other element matches it. An empty `[]` has
// nothing to infer from and is always an error here — callers with a
// target type instead go through checkListLiteralAgainst, which allows
// an empty literal.
func (c *checker) inferListLitType(sc *scope, lit *ast.ListLit) (ast.Type, error) {
	if len(lit.Elems) == 0 {
		return ast.Type{}, fmt.Errorf("line %d: cannot infer a type from an empty list literal; give it an explicit []T type", lit.Line)
	}
	elemType, err := c.exprType(sc, lit.Elems[0])
	if err != nil {
		return ast.Type{}, err
	}
	target := ast.Type{Elem: &elemType}
	if err := c.checkListLiteralAgainst(sc, lit, target); err != nil {
		return ast.Type{}, err
	}
	return target, nil
}

// checkCallSigAsValue validates call's arguments against sig and returns
// its single result type — shared by every call kind used as a value
// (plain function, method, closure variable): each must return exactly
// one value in this position (cascade_spec.md §8.1 vs §8.5's multi-value
// form, which only a MultiLetDecl's Init can receive).
func (c *checker) checkCallSigAsValue(sc *scope, call *ast.CallExpr, sig funcSig) (ast.Type, error) {
	if err := c.checkCallArgs(sc, call, sig); err != nil {
		return ast.Type{}, err
	}
	if len(sig.Results) == 0 {
		return ast.Type{}, fmt.Errorf("line %d: %s() returns no value", call.Line, call.Callee)
	}
	if len(sig.Results) > 1 {
		return ast.Type{}, fmt.Errorf("line %d: %s() returns multiple values; use a multi-value let (e.g. 'let a, b = %s(...)') to receive them", call.Line, call.Callee, call.Callee)
	}
	return sig.Results[0], nil
}

// checkCallExprValue validates a call expression used as a value (as
// opposed to a bare statement — see checkExprStmt): the `string()`
// conversion, the list/closure builtins `len()`/`range()`/`append()`/
// `filter()`/`map()`/`reduce()` (cascade_spec.md §13), a call to a
// closure-valued local variable (§8.3), or a call to any known
// single-value-returning function (§8.1) — a multi-value function (§8.5)
// can't produce one value, so it's rejected here with a pointer to the
// multi-value `let` form instead (see checkCallSigAsValue).
func (c *checker) checkCallExprValue(sc *scope, call *ast.CallExpr) (ast.Type, error) {
	if call.Receiver != nil {
		sig, err := c.resolveMethodCall(sc, call)
		if err != nil {
			return ast.Type{}, err
		}
		return c.checkCallSigAsValue(sc, call, sig)
	}
	if sig, ok, err := c.resolveClosureCall(sc, call); err != nil {
		return ast.Type{}, err
	} else if ok {
		return c.checkCallSigAsValue(sc, call, sig)
	}
	switch call.Callee {
	case "string":
		if len(call.Args) != 1 {
			return ast.Type{}, fmt.Errorf("line %d: string() expects exactly 1 argument, got %d", call.Line, len(call.Args))
		}
		t, err := c.exprType(sc, call.Args[0])
		if err != nil {
			return ast.Type{}, err
		}
		if t.Nullable || (t.Name != "int" && t.Name != "float" && t.Name != "bool") {
			return ast.Type{}, fmt.Errorf("line %d: string() does not support %s", call.Line, typeString(t))
		}
		call.ArgType = t
		return ast.Type{Name: "string"}, nil
	case "len":
		if len(call.Args) != 1 {
			return ast.Type{}, fmt.Errorf("line %d: len() expects exactly 1 argument, got %d", call.Line, len(call.Args))
		}
		t, err := c.exprType(sc, call.Args[0])
		if err != nil {
			return ast.Type{}, err
		}
		if t.Nullable || (t.Name != "string" && t.Elem == nil && t.Map == nil) {
			return ast.Type{}, fmt.Errorf("line %d: len() does not support %s", call.Line, typeString(t))
		}
		return ast.Type{Name: "int"}, nil
	case "range":
		if len(call.Args) != 2 {
			return ast.Type{}, fmt.Errorf("line %d: range() expects exactly 2 arguments, got %d", call.Line, len(call.Args))
		}
		for _, a := range call.Args {
			t, err := c.exprType(sc, a)
			if err != nil {
				return ast.Type{}, err
			}
			if t.Nullable || t.Name != "int" {
				return ast.Type{}, fmt.Errorf("line %d: range() requires int arguments, got %s", ast.ExprLine(a), typeString(t))
			}
		}
		elem := ast.Type{Name: "int"}
		return ast.Type{Elem: &elem}, nil
	case "append":
		if len(call.Args) != 2 {
			return ast.Type{}, fmt.Errorf("line %d: append() expects exactly 2 arguments, got %d", call.Line, len(call.Args))
		}
		lt, err := c.exprType(sc, call.Args[0])
		if err != nil {
			return ast.Type{}, err
		}
		if lt.Nullable || lt.Elem == nil {
			return ast.Type{}, fmt.Errorf("line %d: append() expects a list as its first argument, got %s", ast.ExprLine(call.Args[0]), typeString(lt))
		}
		if err := c.checkAssignable(sc, *lt.Elem, call.Args[1]); err != nil {
			return ast.Type{}, err
		}
		call.ArgType = lt
		return lt, nil
	case "filter":
		lt, ft, err := c.checkListAndHOFArg(sc, call, "filter", 2, 0, 1)
		if err != nil {
			return ast.Type{}, err
		}
		if len(ft.Params) != 1 || !typeShapeEqual(ft.Params[0], *lt.Elem) {
			return ast.Type{}, fmt.Errorf("line %d: filter()'s function must take exactly one %s parameter", call.Line, typeString(*lt.Elem))
		}
		if len(ft.Results) != 1 || ft.Results[0].Name != "bool" || ft.Results[0].Nullable {
			return ast.Type{}, fmt.Errorf("line %d: filter()'s function must return bool", call.Line)
		}
		call.ArgType = lt
		call.HOFType = ft
		return lt, nil
	case "map":
		lt, ft, err := c.checkListAndHOFArg(sc, call, "map", 2, 0, 1)
		if err != nil {
			return ast.Type{}, err
		}
		if len(ft.Params) != 1 || !typeShapeEqual(ft.Params[0], *lt.Elem) {
			return ast.Type{}, fmt.Errorf("line %d: map()'s function must take exactly one %s parameter", call.Line, typeString(*lt.Elem))
		}
		if len(ft.Results) != 1 {
			return ast.Type{}, fmt.Errorf("line %d: map()'s function must return exactly one value", call.Line)
		}
		call.ArgType = lt
		call.HOFType = ft
		resultElem := ft.Results[0]
		return ast.Type{Elem: &resultElem}, nil
	case "reduce":
		lt, ft, err := c.checkListAndHOFArg(sc, call, "reduce", 3, 0, 2)
		if err != nil {
			return ast.Type{}, err
		}
		initType, err := c.exprType(sc, call.Args[1])
		if err != nil {
			return ast.Type{}, err
		}
		if len(ft.Params) != 2 || !typeShapeEqual(ft.Params[0], initType) || !typeShapeEqual(ft.Params[1], *lt.Elem) {
			return ast.Type{}, fmt.Errorf("line %d: reduce()'s function must take (%s, %s) parameters", call.Line, typeString(initType), typeString(*lt.Elem))
		}
		if len(ft.Results) != 1 || !typeShapeEqual(ft.Results[0], initType) {
			return ast.Type{}, fmt.Errorf("line %d: reduce()'s function must return %s (the accumulator type)", call.Line, typeString(initType))
		}
		call.ArgType = lt
		call.HOFType = ft
		return initType, nil
	default:
		sig, ok := c.sigs[call.Callee]
		if !ok {
			return ast.Type{}, fmt.Errorf("line %d: %q cannot be used as a value", call.Line, call.Callee)
		}
		return c.checkCallSigAsValue(sc, call, sig)
	}
}

// unaryResultType validates op's operand type and returns its result type
// (cascade_spec.md §6): "!" needs bool, "-"/"~" need int (or float for
// "-"), and either way the operand must be non-nullable (narrow with `is
// not none` first — deferred to Step 5, see CLAUDE.md's "確定した設計判断").
func unaryResultType(op string, xt ast.Type, line int) (ast.Type, error) {
	if xt.Nullable {
		return ast.Type{}, fmt.Errorf("line %d: operator %q needs a non-nullable operand, got %s", line, op, typeString(xt))
	}
	switch op {
	case "!":
		if xt.Name != "bool" {
			return ast.Type{}, fmt.Errorf("line %d: unary ! requires bool, got %s", line, typeString(xt))
		}
		return xt, nil
	case "-":
		if xt.Name != "int" && xt.Name != "float" {
			return ast.Type{}, fmt.Errorf("line %d: unary - requires int or float, got %s", line, typeString(xt))
		}
		return xt, nil
	case "~":
		if xt.Name != "int" {
			return ast.Type{}, fmt.Errorf("line %d: unary ~ requires int, got %s", line, typeString(xt))
		}
		return xt, nil
	default:
		return ast.Type{}, fmt.Errorf("line %d: unsupported unary operator %q", line, op)
	}
}

// binaryResultType validates op's operand types and returns its result
// type (cascade_spec.md §6). Cascade never does implicit conversion, so
// both operands must already share the exact same type; "+" is the one
// operator whose AMIVM-IR instruction depends on that shared type (ADD for
// int/float, CONCAT for string — codegen reads ResultType.Name to tell
// them apart, mirroring Seed's identical `+` dispatch).
func binaryResultType(op string, xt, yt ast.Type, line int) (ast.Type, error) {
	// A pointer or a function value is always Nullable (see ast.Type's
	// doc) but can never be narrowed to satisfy the generic "narrow with
	// 'is not none' first" rejection below — narrowedVarInfo deliberately
	// excludes both (see its doc: *T is *T and func(...): R is
	// func(...): R regardless of nilness) — so telling the user to narrow
	// would send them chasing something structurally impossible. Each
	// gets its own accurate rejection instead. Pointer equality (`p1 ==
	// p2`, comparing addresses) is the one exception: Go allows it
	// directly, with no narrowing involved at all, so it's let through
	// before either check below would otherwise catch it.
	if (op == "==" || op == "!=") && xt.Ptr != nil && yt.Ptr != nil {
		if !typeShapeEqual(xt, yt) {
			return ast.Type{}, fmt.Errorf("line %d: operator %q: mismatched operand types %s and %s", line, op, typeString(xt), typeString(yt))
		}
		return ast.Type{Name: "bool"}, nil
	}
	if xt.Func != nil || yt.Func != nil {
		return ast.Type{}, fmt.Errorf("line %d: operator %q does not support function values (use 'is none'/'is not none' to check nullness)", line, op)
	}
	if xt.Ptr != nil || yt.Ptr != nil {
		return ast.Type{}, fmt.Errorf("line %d: operator %q does not support pointer values (use 'is none'/'is not none' to check nullness, or '*' to dereference)", line, op)
	}
	if xt.Nullable || yt.Nullable {
		return ast.Type{}, fmt.Errorf("line %d: operator %q needs non-nullable operands (narrow with 'is not none' first)", line, op)
	}
	if !typeShapeEqual(xt, yt) {
		return ast.Type{}, fmt.Errorf("line %d: operator %q: mismatched operand types %s and %s", line, op, typeString(xt), typeString(yt))
	}

	switch op {
	case "+":
		switch xt.Name {
		case "int", "float", "string":
			return xt, nil
		default:
			return ast.Type{}, fmt.Errorf("line %d: operator + does not support %s", line, typeString(xt))
		}
	case "-", "*", "/":
		if xt.Name != "int" && xt.Name != "float" {
			return ast.Type{}, fmt.Errorf("line %d: operator %q requires int or float operands, got %s", line, op, typeString(xt))
		}
		return xt, nil
	case "%":
		if xt.Name != "int" {
			return ast.Type{}, fmt.Errorf("line %d: operator %% requires int operands, got %s", line, typeString(xt))
		}
		return xt, nil
	case "&", "|", "^", "&^", "<<", ">>":
		if xt.Name != "int" {
			return ast.Type{}, fmt.Errorf("line %d: operator %q requires int operands, got %s", line, op, typeString(xt))
		}
		return xt, nil
	case "<", "<=", ">", ">=":
		if xt.Name != "int" && xt.Name != "float" && xt.Name != "string" {
			return ast.Type{}, fmt.Errorf("line %d: operator %q requires int, float, or string operands, got %s", line, op, typeString(xt))
		}
		return ast.Type{Name: "bool"}, nil
	case "==", "!=":
		// Func operands are already rejected above (before the generic
		// nullable check), and pointer operands are already accepted and
		// returned above too — neither can reach this case at all.
		return ast.Type{Name: "bool"}, nil
	case "&&", "||":
		if xt.Name != "bool" {
			return ast.Type{}, fmt.Errorf("line %d: operator %q requires bool operands, got %s", line, op, typeString(xt))
		}
		return ast.Type{Name: "bool"}, nil
	default:
		return ast.Type{}, fmt.Errorf("line %d: unsupported operator %q", line, op)
	}
}

// typeString formats t for error messages, e.g. "int", "string?", or
// "[]int" (the "?" always applies to the outermost type — see ast.Type's
// doc — so it's appended after any "[]" recursion, never before it). A
// pointer prints as "*T" and a function type as "func(T1, T2): R" — both
// with no trailing "?" of their own, since both are unconditionally
// nullable already (see ast.Type's doc); printing one anyway would be
// pure noise on every single pointer/func type, never actually
// distinguishing anything. Previously this fell through to the bare
// `base := t.Name` case for both (t.Name is "" for either), producing a
// bare "?" with no type information at all — a real bug caught while
// adding func-type support (see typeShapeEqual's doc for the sibling bug
// this mirrors).
func typeString(t ast.Type) string {
	if t.Elem != nil {
		base := "[]" + typeString(*t.Elem)
		if t.Nullable {
			return base + "?"
		}
		return base
	}
	if t.Ptr != nil {
		return "*" + typeString(*t.Ptr)
	}
	if t.Func != nil {
		return funcTypeString(*t.Func)
	}
	if t.Map != nil {
		base := "map<" + typeString(t.Map.Key) + ", " + typeString(t.Map.Value) + ">"
		if t.Nullable {
			return base + "?"
		}
		return base
	}
	if t.Nullable {
		return t.Name + "?"
	}
	return t.Name
}

// funcTypeString formats a function type's signature for error messages,
// e.g. "func(int, int): bool" or "func(string)" (no `: ...` at all when
// there's no return value, matching how the type itself is written —
// cascade_spec.md §2.2).
func funcTypeString(ft ast.FuncType) string {
	params := make([]string, len(ft.Params))
	for i, p := range ft.Params {
		params[i] = typeString(p)
	}
	s := "func(" + strings.Join(params, ", ") + ")"
	switch len(ft.Results) {
	case 0:
	case 1:
		s += ": " + typeString(ft.Results[0])
	default:
		results := make([]string, len(ft.Results))
		for i, r := range ft.Results {
			results[i] = typeString(r)
		}
		s += ": (" + strings.Join(results, ", ") + ")"
	}
	return s
}
