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

// CallExpr is a function call, e.g. print("hello").
type CallExpr struct {
	Callee string
	Args   []Expr
	Line   int
}

func (*CallExpr) exprNode() {}
