// Package ast defines the abstract syntax tree for Cascade programs, the
// shared vocabulary between parser, sema, and codegen.
package ast

// Type is a Cascade type reference: a bare scalar or struct name
// (cascade_spec.md §2.1/§4.1: int/float/string/bool/StructName — Name
// set, Elem and Ptr nil), a list type (§2.2's `[]T`, Elem set to T), or a
// pointer type (§2.2's `*T`, Ptr set to T), either optionally nullable
// (§2.3) — except a pointer, which is *always* nullable regardless of
// whether `?` was written (§2.2's own zero value for `*T` is `none`; see
// CLAUDE.md's "確定した設計判断" for why a pointer's nullability is
// represented via Go's native nil rather than a companion isset flag,
// unlike every other nullable type). Map/func/error forms are added as
// the steps that need them land (see CLAUDE.md's implementation step
// plan).
//
// `?` always binds to the outermost type built so far — `[]int?` parses
// as `([]int)?` (a nullable list), not `[](int?)` (a list of nullable
// int) — matching the only concrete spec usage of a nullable list type,
// `collect`'s `[]string?` result (§9.3); see CLAUDE.md's "確定した設計判断"
// for why the alternative reading was rejected. A list's own element type
// is therefore never itself nullable through this grammar.
type Type struct {
	Name     string
	Nullable bool
	Elem     *Type
	Ptr      *Type
}

// Param is a single function parameter.
type Param struct {
	Type Type
	Name string
	Line int
}

// FuncDecl is a top-level function declaration (cascade_spec.md §8.1), or,
// when Receiver is non-nil, a receiver function (§8.2) — `func (p: Point)
// magnitude(): float { ... }`. Results is empty when the function has no
// return value, and may hold more than one entry for a multi-value return
// (§8.5) — modeling that shape from the start avoids reworking every
// caller once Step 7 adds it.
type FuncDecl struct {
	Receiver *Param // non-nil for a receiver function (§8.2)
	Name     string
	Params   []Param
	Results  []Type
	Body     []Stmt
	Line     int
}

// StructField is one field in a struct declaration (cascade_spec.md §4.1).
type StructField struct {
	Name string
	Type Type
}

// StructDecl is a top-level `struct` declaration (cascade_spec.md §4.1).
type StructDecl struct {
	Name   string
	Fields []StructField
	Line   int
}

// File is the root node of a parsed Cascade source file.
type File struct {
	Structs []*StructDecl
	Funcs   []*FuncDecl
}

// Stmt is implemented by every statement node.
type Stmt interface{ stmtNode() }

// ExprStmt is a statement consisting of a single expression (currently
// only a call expression is valid here, e.g. print(...)).
type ExprStmt struct {
	X    Expr
	Line int
}

func (*ExprStmt) stmtNode() {}

// ReturnStmt is a `return` statement. Results is empty for a bare
// `return`, and may hold more than one comma-separated expression for a
// multi-value return (cascade_spec.md §8.5).
type ReturnStmt struct {
	Results []Expr
	Line    int
}

func (*ReturnStmt) stmtNode() {}

// LetDecl is a `let`/`const` declaration (cascade_spec.md §4.2): `let name:
// Type`, `let name: Type = Init`, `let name = Init` (Type is the zero Type
// when omitted — inferred from Init), or the `const` form (Init required,
// no reassignment; enforced by sema, not represented here).
//
// ResolvedType is filled in by sema.Check with Type itself (if given) or
// the type inferred from Init (if not), so codegen never has to re-derive
// it — the same ast-annotation pattern Seed's BinaryExpr.ResultType uses
// (see seed_implementation_notes.md §7).
type LetDecl struct {
	Name         string
	Type         Type
	Const        bool
	Init         Expr // nil when omitted
	Line         int
	ResolvedType Type
}

func (*LetDecl) stmtNode() {}

// MultiLetDecl is a multi-value `let`/`const` declaration (cascade_spec.md
// §5, §8.5): `let n1, n2, ... = call(...)`, receiving every result of a
// multi-value function call in one statement. Any Name may be "_" to
// discard that result (§5) — sema does not declare a variable for it.
// Unlike LetDecl, there is no explicit-type form: each name's type is
// always the corresponding declared result type of Init's callee.
//
// ResolvedTypes is filled in by sema.Check, one entry per Name in the
// same order (the zero Type for a discarded "_"), so codegen doesn't
// have to re-derive them.
type MultiLetDecl struct {
	Names         []string
	Const         bool
	Init          *CallExpr
	Line          int
	ResolvedTypes []Type
}

func (*MultiLetDecl) stmtNode() {}

// AssignStmt is a scalar assignment (cascade_spec.md §5): `name = Value`;
// `name[Index] = Value`, a single list-element assignment, when Index is
// non-nil; or `name.Field = Value`, a struct-field assignment (§4.1,
// §8.2 — name may be a struct or a pointer to one, written identically),
// when Field is non-empty. Index and Field are never both set. The
// pointer-dereference form (`*ptr = Value`) is its own statement,
// DerefAssignStmt, since its target isn't name-rooted. The map-element
// form is added once maps land (Step 10).
type AssignStmt struct {
	Name  string
	Index Expr   // non-nil for `name[Index] = value`
	Field string // non-empty for `name.Field = value`
	Value Expr
	Line  int
}

func (*AssignStmt) stmtNode() {}

// DerefAssignStmt is `*Ptr = Value` (cascade_spec.md §5): assignment
// through a pointer dereference. Ptr is typically a plain identifier, but
// nothing here requires that.
type DerefAssignStmt struct {
	Ptr   Expr
	Value Expr
	Line  int
}

func (*DerefAssignStmt) stmtNode() {}

// CompoundAssignStmt is a compound assignment (cascade_spec.md §5): `name
// op= Value` where op is one of + - * / %. Like `++`/`--`, this only
// exists as a statement, never inside an expression.
type CompoundAssignStmt struct {
	Name  string
	Op    string // "+", "-", "*", "/", "%"
	Value Expr
	Line  int
}

func (*CompoundAssignStmt) stmtNode() {}

// IncDecStmt is a postfix `name++` or `name--` statement (cascade_spec.md
// §5).
type IncDecStmt struct {
	Name string
	Op   string // "++" or "--"
	Line int
}

func (*IncDecStmt) stmtNode() {}

// IfClause is one `if`/`elif` condition-and-body pair.
type IfClause struct {
	Cond Expr
	Body []Stmt
}

// IfStmt is an if/elif/else chain (cascade_spec.md §7). Clauses holds the
// `if` clause followed by zero or more `elif` clauses, in source order.
// Else is nil when there is no `else` clause.
type IfStmt struct {
	Clauses []IfClause
	Else    []Stmt
	Line    int
}

func (*IfStmt) stmtNode() {}

// WhileStmt is a `while` loop (cascade_spec.md §7).
type WhileStmt struct {
	Cond Expr
	Body []Stmt
	Line int
}

func (*WhileStmt) stmtNode() {}

// BreakStmt exits the innermost enclosing loop or switch (cascade_spec.md
// §7).
type BreakStmt struct {
	Line int
}

func (*BreakStmt) stmtNode() {}

// ContinueStmt advances the innermost enclosing loop to its next
// iteration, skipping over any enclosing switch (cascade_spec.md §7:
// "`switch`自体はループではない").
type ContinueStmt struct {
	Line int
}

func (*ContinueStmt) stmtNode() {}

// ForInStmt is `for x in List { ... }` over a list (cascade_spec.md §7).
// The two-variable map form (`for k, v in m`) is added once maps land
// (Step 10).
//
// ElemType is filled in by sema.Check with the list's element type, so
// codegen doesn't have to re-derive it (the same ast-annotation pattern
// LetDecl.ResolvedType uses).
type ForInStmt struct {
	VarName  string
	List     Expr
	Body     []Stmt
	Line     int
	ElemType Type
}

func (*ForInStmt) stmtNode() {}

// SwitchCase is one `case` clause (cascade_spec.md §7). Values holds one
// or more comma-separated candidate values for a tagged switch (compared
// for equality against SwitchStmt.Tag), or exactly one boolean condition
// for an untagged switch (the spec's own examples never show a
// comma-separated untagged case, so the parser only allows one there).
type SwitchCase struct {
	Values []Expr
	Body   []Stmt
	Line   int
}

// SwitchStmt is a `switch` statement (cascade_spec.md §7), tagged (Tag
// non-nil, each case's Values compared for equality against it) or
// untagged (Tag nil, each case's Values[0] is itself a boolean condition,
// evaluated top to bottom). Default is nil when there is no `default`
// clause.
type SwitchStmt struct {
	Tag     Expr
	Cases   []SwitchCase
	Default []Stmt
	Line    int
}

func (*SwitchStmt) stmtNode() {}

// Expr is implemented by every expression node.
type Expr interface{ exprNode() }

// Ident is a reference to a declared name.
type Ident struct {
	Name string
	Line int
}

func (*Ident) exprNode() {}

// StringLit is a string literal.
type StringLit struct {
	Value string
	Line  int
}

func (*StringLit) exprNode() {}

// IntLit is an integer literal.
type IntLit struct {
	Value int64
	Line  int
}

func (*IntLit) exprNode() {}

// FloatLit is a floating-point literal.
type FloatLit struct {
	Value float64
	Line  int
}

func (*FloatLit) exprNode() {}

// BoolLit is a `true`/`false` literal.
type BoolLit struct {
	Value bool
	Line  int
}

func (*BoolLit) exprNode() {}

// NoneLit is the `none` literal (cascade_spec.md §2.3). Its type is
// whatever nullable type it's being assigned into — it has no type of its
// own, so sema rejects it wherever that context is missing (e.g. `let x =
// none` with no explicit `: T?`).
type NoneLit struct {
	Line int
}

func (*NoneLit) exprNode() {}

// ListLit is a list literal, e.g. `[1, 2, 3]` or `[]` (cascade_spec.md
// §3). Its element type is determined entirely by the assignment context
// (a LetDecl's declared type, an AssignStmt's target, ...) or, for a
// non-empty literal with no declared type, inferred from Elems[0] — not
// by the literal itself. Like ast.NoneLit, it is therefore never resolved
// through the general exprType path; sema special-cases it wherever a
// value is checked against a target type (see checkAssignable/inferType).
type ListLit struct {
	Elems []Expr
	Line  int
}

func (*ListLit) exprNode() {}

// IndexExpr is a list element reference, e.g. `xs[0]` (cascade_spec.md
// §5). ResultType is filled in by sema.Check with the list's element
// type, so codegen doesn't have to re-derive it.
type IndexExpr struct {
	X          Expr
	Index      Expr
	Line       int
	ResultType Type
}

func (*IndexExpr) exprNode() {}

// CallExpr is a function call, e.g. print("hello"), or, when Receiver is
// non-nil, a method call (cascade_spec.md §8.2) — `p.magnitude()` parses
// as CallExpr{Receiver: p, Callee: "magnitude"}. Reusing CallExpr for
// both (rather than a parallel MethodCallExpr type) means every place
// that already resolves/compiles a call — as a statement, a value, or a
// MultiLetDecl's initializer — only needs one extra branch (does Receiver
// name a struct method or does Callee name a plain function?) instead of
// duplicating all of checkCallExprValue/checkExprStmt/genCallValue/etc.
//
// ArgType is filled in by sema.Check only for the builtins that need an
// argument's resolved type to pick their AMIVM-IR (cascade_spec.md §13):
// string() (Args[0]'s scalar type, to choose the right strconv function)
// and append() (Args[0]'s own list type, needed for SLMAKE/ASET/AGET).
// This mirrors BinaryExpr/UnaryExpr's ResultType — the same pattern
// Seed's identical CallExpr.ArgType uses — so codegen never re-derives a
// type it already has. It's the zero Type for every other call.
//
// RecvStruct/RecvNeedsAddr/RecvNeedsDeref are filled in by sema.Check
// only when Receiver is non-nil (cascade_spec.md §8.2): RecvStruct is the
// resolved struct name, used to build the method's qualified top-level
// function name (StructName_Method — see CLAUDE.md's "確定した設計判断").
// RecvNeedsAddr/RecvNeedsDeref record whether Receiver itself must be
// automatically address-of'd or dereferenced before the call, when its
// own value/pointer kind doesn't already match the method's declared
// receiver kind — at most one of the two is ever true.
type CallExpr struct {
	Receiver       Expr // non-nil for obj.method(...); nil for a plain call
	Callee         string
	Args           []Expr
	Line           int
	ArgType        Type
	RecvStruct     string
	RecvNeedsAddr  bool
	RecvNeedsDeref bool
}

func (*CallExpr) exprNode() {}

// StructFieldInit is one `name: value` pair in a struct literal.
type StructFieldInit struct {
	Name  string
	Value Expr
}

// StructLit is a struct literal, e.g. `Point{x: 1.0, y: 2.0}`
// (cascade_spec.md §4.1). Every field must be given explicitly — there is
// no partial/defaulted-field form (see CLAUDE.md's "確定した設計判断").
type StructLit struct {
	TypeName string
	Fields   []StructFieldInit
	Line     int
}

func (*StructLit) exprNode() {}

// FieldExpr is a struct field read, e.g. `p.x` (cascade_spec.md §4.1,
// §8.2 — X may be a struct or a pointer to one, read identically; Go's
// own auto-deref on field access means codegen needs no special case for
// either). ResultType is filled in by sema.Check with the field's
// declared type, so codegen doesn't have to re-derive it.
type FieldExpr struct {
	X          Expr
	Field      string
	Line       int
	ResultType Type
}

func (*FieldExpr) exprNode() {}

// UnaryExpr is a prefix unary operator (cascade_spec.md §6): "!", "-",
// "~", "&" (address-of — X must be addressable, cascade_spec.md §6; see
// CLAUDE.md's "確定した設計判断"), or "*" (dereference). ResultType is
// filled in by sema.Check; codegen relies on it to pick the right
// AMIVM-IR type for the temporary holding the result (the same
// ast-annotation pattern LetDecl.ResolvedType uses).
type UnaryExpr struct {
	Op         string
	X          Expr
	Line       int
	ResultType Type
}

func (*UnaryExpr) exprNode() {}

// BinaryExpr is a binary operator expression (cascade_spec.md §6): the
// arithmetic, comparison, logical, and bitwise/shift operators.
// ResultType is filled in by sema.Check; codegen relies on it both to
// pick the right AMIVM-IR type for the temporary holding the result, and
// to disambiguate "+" (ADD for int/float, CONCAT for string).
type BinaryExpr struct {
	Op         string
	X, Y       Expr
	Line       int
	ResultType Type
}

func (*BinaryExpr) exprNode() {}

// NullCheckExpr is `X is none` (Not=false) or `X is not none` (Not=true),
// cascade_spec.md §7. It binds at parsePrimary's precedence (tighter than
// any operator), directly wrapping the atom it applies to — the spec only
// ever shows it applied to a bare identifier, which sema.Check enforces
// (see CLAUDE.md's "確定した設計判断" for why only this shape is supported).
type NullCheckExpr struct {
	X    Expr
	Not  bool
	Line int
}

func (*NullCheckExpr) exprNode() {}

// StmtLine returns the source line a statement node was parsed from.
func StmtLine(s Stmt) int {
	switch v := s.(type) {
	case *ExprStmt:
		return v.Line
	case *ReturnStmt:
		return v.Line
	case *LetDecl:
		return v.Line
	case *MultiLetDecl:
		return v.Line
	case *AssignStmt:
		return v.Line
	case *DerefAssignStmt:
		return v.Line
	case *CompoundAssignStmt:
		return v.Line
	case *IncDecStmt:
		return v.Line
	case *IfStmt:
		return v.Line
	case *WhileStmt:
		return v.Line
	case *BreakStmt:
		return v.Line
	case *ContinueStmt:
		return v.Line
	case *SwitchStmt:
		return v.Line
	case *ForInStmt:
		return v.Line
	default:
		return 0
	}
}

// ExprLine returns the source line an expression node was parsed from.
func ExprLine(e Expr) int {
	switch v := e.(type) {
	case *Ident:
		return v.Line
	case *StringLit:
		return v.Line
	case *IntLit:
		return v.Line
	case *FloatLit:
		return v.Line
	case *BoolLit:
		return v.Line
	case *NoneLit:
		return v.Line
	case *ListLit:
		return v.Line
	case *IndexExpr:
		return v.Line
	case *CallExpr:
		return v.Line
	case *StructLit:
		return v.Line
	case *FieldExpr:
		return v.Line
	case *UnaryExpr:
		return v.Line
	case *BinaryExpr:
		return v.Line
	case *NullCheckExpr:
		return v.Line
	default:
		return 0
	}
}
