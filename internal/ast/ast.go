// Package ast defines the abstract syntax tree for Cascade programs, the
// shared vocabulary between parser, sema, and codegen.
package ast

// Type is a Cascade type reference. Only a bare scalar name (cascade_spec.md
// §2.1: int/float/string/bool) plus the nullable suffix (§2.3) is
// represented so far; pointer/slice/map/func/struct/error forms are added
// as the steps that need them land (see CLAUDE.md's implementation step
// plan).
type Type struct {
	Name     string
	Nullable bool
}

// Param is a single function parameter.
type Param struct {
	Type Type
	Name string
}

// FuncDecl is a top-level function declaration (cascade_spec.md §8.1).
// Results is empty when the function has no return value, and may hold
// more than one entry for a multi-value return (§8.5) — modeling that
// shape from the start avoids reworking every caller once Step 7 adds it.
type FuncDecl struct {
	Name    string
	Params  []Param
	Results []Type
	Body    []Stmt
	Line    int
}

// File is the root node of a parsed Cascade source file.
type File struct {
	Funcs []*FuncDecl
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

// AssignStmt is a scalar assignment (cascade_spec.md §5): `name = Value`.
// The struct-field/list-element/pointer-deref/map-element forms are added
// once the steps that introduce those types land.
type AssignStmt struct {
	Name  string
	Value Expr
	Line  int
}

func (*AssignStmt) stmtNode() {}

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

// CallExpr is a function call, e.g. print("hello").
//
// ArgType is filled in by sema.Check only for the overloaded builtin
// string() (cascade_spec.md §13), which accepts several argument types
// and needs different AMIVM-IR per one. It holds Args[0]'s resolved type,
// so codegen can pick the right instruction without re-deriving a type on
// its own (mirroring BinaryExpr/UnaryExpr's ResultType — the same pattern
// Seed's identical CallExpr.ArgType uses). It's the zero Type for every
// other call.
type CallExpr struct {
	Callee  string
	Args    []Expr
	Line    int
	ArgType Type
}

func (*CallExpr) exprNode() {}

// UnaryExpr is a prefix unary operator (cascade_spec.md §6): "!" or "-" so
// far — "*"/"&"/"~" are added once pointers (Step 8) and bitwise ops
// (Step 4) land. ResultType is filled in by sema.Check; codegen relies on
// it to pick the right AMIVM-IR type for the temporary holding the result
// (the same ast-annotation pattern LetDecl.ResolvedType uses).
type UnaryExpr struct {
	Op         string
	X          Expr
	Line       int
	ResultType Type
}

func (*UnaryExpr) exprNode() {}

// BinaryExpr is a binary operator expression (cascade_spec.md §6): the
// arithmetic ("+" "-" "*" "/" "%"), comparison ("==" "!=" "<" "<=" ">"
// ">="), and logical ("&&" "||") operators so far — bitwise/shift land in
// Step 4. ResultType is filled in by sema.Check; codegen relies on it both
// to pick the right AMIVM-IR type for the temporary holding the result,
// and to disambiguate "+" (ADD for int/float, CONCAT for string).
type BinaryExpr struct {
	Op         string
	X, Y       Expr
	Line       int
	ResultType Type
}

func (*BinaryExpr) exprNode() {}

// StmtLine returns the source line a statement node was parsed from.
func StmtLine(s Stmt) int {
	switch v := s.(type) {
	case *ExprStmt:
		return v.Line
	case *ReturnStmt:
		return v.Line
	case *LetDecl:
		return v.Line
	case *AssignStmt:
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
	case *CallExpr:
		return v.Line
	case *UnaryExpr:
		return v.Line
	case *BinaryExpr:
		return v.Line
	default:
		return 0
	}
}
