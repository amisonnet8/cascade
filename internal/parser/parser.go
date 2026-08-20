// Package parser builds an ast.File from Cascade source code via
// hand-written recursive-descent parsing (see CLAUDE.md's "確定した設計判断"
// for why no parser generator is used).
//
// The grammar implemented here is intentionally a subset of
// cascade_spec.md: top-level `struct` declarations (§4.1) and func
// declarations, including an optional receiver clause `func (p: Point)
// name(...)` (§8.2), with a single or multi-value (`(T1, T2)`) result
// type (§8.1, §8.5); a block of statements covering `let`/`const`
// declarations (including the multi-value `let n1, n2 = call(...)`
// form), a call-expression statement (plain or `obj.method(...)`), a
// dereferencing assignment `*ptr = value` (§4.4), scalar/list-element/
// struct-field/compound assignment, `++`/`--`, `return`, `if`/`elif`/
// `else`, `while`, `for x in list`, `switch` (tagged and untagged), and
// `break`/`continue`; and a full operator-precedence expression grammar
// (§6, including unary `&`/`*` for address-of/dereference, §4.4),
// literals (including nullable-type `T?` declarations, pointer types
// `*T`, the `none` literal, list literals `[1, 2, 3]`/`[]`, and struct
// literals `Name{field: value, ...}`), variable references, function/
// method calls, field access `x.field`, list indexing `xs[i]`, and `is
// none`/`is not none` (§7) directly on an atom (the only place the spec
// actually uses it — as an `if`/`switch` condition). A struct literal is
// only recognized where `{` cannot instead open a block (see
// withStructLitForbidden/withStructLitAllowed), mirroring Go's identical
// restriction. Later development steps extend this grammar further
// (closures, channels/pipelines, ...) one feature at a time.
package parser

import (
	"fmt"
	"strconv"

	"github.com/amisonnet8/cascade/internal/ast"
	"github.com/amisonnet8/cascade/internal/lexer"
)

// Parse lexes and parses src into an *ast.File.
func Parse(src string) (*ast.File, error) {
	toks, err := lexer.Tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	return p.parseFile()
}

type parser struct {
	toks []lexer.Token
	pos  int
	// noStructLit disables parsing a bare `Name{...}` as a struct literal
	// (cascade_spec.md §4.1) — set while parsing an if/while/switch/for-in
	// condition, which is otherwise ambiguous with the construct's own
	// following '{' body (the same restriction Go places on composite
	// literals in these exact positions). Cleared again inside any
	// unambiguous bracket/paren context — see withStructLitAllowed.
	noStructLit bool
}

// withStructLitForbidden parses using fn with struct-literal parsing
// disabled — for an if/while/switch/for-in condition immediately followed
// by the construct's own '{' body.
func (p *parser) withStructLitForbidden(fn func() (ast.Expr, error)) (ast.Expr, error) {
	saved := p.noStructLit
	p.noStructLit = true
	x, err := fn()
	p.noStructLit = saved
	return x, err
}

// withStructLitAllowed parses using fn with struct-literal parsing
// re-enabled — for anywhere inside an unambiguous bracket/paren context
// (call arguments, a list literal's elements, a struct literal's own
// field values, a parenthesized sub-expression), even while nested inside
// a condition that itself forbids a bare struct literal.
func (p *parser) withStructLitAllowed(fn func() (ast.Expr, error)) (ast.Expr, error) {
	saved := p.noStructLit
	p.noStructLit = false
	x, err := fn()
	p.noStructLit = saved
	return x, err
}

func (p *parser) cur() lexer.Token {
	return p.toks[p.pos]
}

func (p *parser) advance() lexer.Token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) expect(kind lexer.Kind, what string) (lexer.Token, error) {
	if p.cur().Kind != kind {
		return lexer.Token{}, fmt.Errorf("line %d: expected %s, got %q", p.cur().Line, what, p.cur().Literal)
	}
	return p.advance(), nil
}

// skipNewlines consumes any number of (already-collapsed) Newline tokens.
func (p *parser) skipNewlines() {
	for p.cur().Kind == lexer.Newline {
		p.advance()
	}
}

// typeKeywords maps a scalar type keyword to its ast.Type name
// (cascade_spec.md §2.1). Extended with pointer/slice/map/func/struct/error
// forms as the steps that need them land.
var typeKeywords = map[lexer.Kind]string{
	lexer.KwInt:    "int",
	lexer.KwFloat:  "float",
	lexer.KwString: "string",
	lexer.KwBool:   "bool",
}

func (p *parser) parseFile() (*ast.File, error) {
	f := &ast.File{}
	p.skipNewlines()
	for p.cur().Kind != lexer.EOF {
		switch p.cur().Kind {
		case lexer.KwFunc:
			fn, err := p.parseFuncDecl()
			if err != nil {
				return nil, err
			}
			f.Funcs = append(f.Funcs, fn)
		case lexer.KwStruct:
			sd, err := p.parseStructDecl()
			if err != nil {
				return nil, err
			}
			f.Structs = append(f.Structs, sd)
		default:
			return nil, fmt.Errorf("line %d: expected 'func' or 'struct' at top level, got %q", p.cur().Line, p.cur().Literal)
		}
		p.skipNewlines()
	}
	return f, nil
}

// parseStructDecl parses a `struct` declaration (cascade_spec.md §4.1):
// `struct Name { field: Type ... }`, one field per line.
func (p *parser) parseStructDecl() (*ast.StructDecl, error) {
	kw, err := p.expect(lexer.KwStruct, "'struct'")
	if err != nil {
		return nil, err
	}
	name, err := p.expect(lexer.Ident, "struct name")
	if err != nil {
		return nil, err
	}
	sd := &ast.StructDecl{Name: name.Literal, Line: kw.Line}

	if _, err := p.expect(lexer.LBrace, "'{'"); err != nil {
		return nil, err
	}
	p.skipNewlines()
	for p.cur().Kind != lexer.RBrace {
		fname, err := p.expect(lexer.Ident, "field name")
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.Colon, "':'"); err != nil {
			return nil, err
		}
		typ, err := p.parseType()
		if err != nil {
			return nil, err
		}
		sd.Fields = append(sd.Fields, ast.StructField{Name: fname.Literal, Type: typ})
		p.skipNewlines()
	}
	if _, err := p.expect(lexer.RBrace, "'}'"); err != nil {
		return nil, err
	}
	return sd, nil
}

func (p *parser) parseFuncDecl() (*ast.FuncDecl, error) {
	kw, err := p.expect(lexer.KwFunc, "'func'")
	if err != nil {
		return nil, err
	}
	fn := &ast.FuncDecl{Line: kw.Line}

	if p.cur().Kind == lexer.LParen {
		recv, err := p.parseReceiver()
		if err != nil {
			return nil, err
		}
		fn.Receiver = &recv
	}

	name, err := p.expect(lexer.Ident, "function name")
	if err != nil {
		return nil, err
	}
	fn.Name = name.Literal

	if _, err := p.expect(lexer.LParen, "'('"); err != nil {
		return nil, err
	}
	for p.cur().Kind != lexer.RParen {
		if len(fn.Params) > 0 {
			if _, err := p.expect(lexer.Comma, "','"); err != nil {
				return nil, err
			}
		}
		param, err := p.parseParam()
		if err != nil {
			return nil, err
		}
		fn.Params = append(fn.Params, param)
	}
	if _, err := p.expect(lexer.RParen, "')'"); err != nil {
		return nil, err
	}

	if p.cur().Kind == lexer.Colon {
		p.advance()
		results, err := p.parseResultTypes()
		if err != nil {
			return nil, err
		}
		fn.Results = results
	}

	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	fn.Body = body
	return fn, nil
}

func (p *parser) parseParam() (ast.Param, error) {
	name, err := p.expect(lexer.Ident, "parameter name")
	if err != nil {
		return ast.Param{}, err
	}
	if _, err := p.expect(lexer.Colon, "':'"); err != nil {
		return ast.Param{}, err
	}
	typ, err := p.parseType()
	if err != nil {
		return ast.Param{}, err
	}
	return ast.Param{Type: typ, Name: name.Literal, Line: name.Line}, nil
}

// parseReceiver parses a receiver function's `(name: Type)` clause
// (cascade_spec.md §8.2), syntactically identical to a single parameter
// in parens.
func (p *parser) parseReceiver() (ast.Param, error) {
	if _, err := p.expect(lexer.LParen, "'('"); err != nil {
		return ast.Param{}, err
	}
	recv, err := p.parseParam()
	if err != nil {
		return ast.Param{}, err
	}
	if _, err := p.expect(lexer.RParen, "')'"); err != nil {
		return ast.Param{}, err
	}
	return recv, nil
}

// parseResultTypes parses a function's return-type list (cascade_spec.md
// §8.1, §8.5): a single type (`: int`) or, when immediately followed by
// '(', a comma-separated multi-value list (`: (int, int)`). The two
// forms are unambiguous — Cascade has no other use for a parenthesized
// type — so seeing '(' right after ':' always means the multi-value form.
func (p *parser) parseResultTypes() ([]ast.Type, error) {
	if p.cur().Kind != lexer.LParen {
		t, err := p.parseType()
		if err != nil {
			return nil, err
		}
		return []ast.Type{t}, nil
	}
	p.advance() // '('
	var types []ast.Type
	for p.cur().Kind != lexer.RParen {
		if len(types) > 0 {
			if _, err := p.expect(lexer.Comma, "','"); err != nil {
				return nil, err
			}
		}
		t, err := p.parseType()
		if err != nil {
			return nil, err
		}
		types = append(types, t)
	}
	if _, err := p.expect(lexer.RParen, "')'"); err != nil {
		return nil, err
	}
	return types, nil
}

// parseType parses a scalar or list type with an optional trailing
// nullable suffix (cascade_spec.md §2.1-§2.3: `int`, `int?`, `[]int`,
// `[]int?`, ...). The `?` is checked only once, here, after parseTypeBase
// has consumed every `[]` prefix — so it always binds to the outermost
// type (see ast.Type's doc for why `[]int?` must mean a nullable list,
// not a list of nullable int). Every non-scalar, non-list type form is
// parsed starting in the step that introduces it.
func (p *parser) parseType() (ast.Type, error) {
	t, err := p.parseTypeBase()
	if err != nil {
		return ast.Type{}, err
	}
	if p.cur().Kind == lexer.Question {
		p.advance()
		t.Nullable = true
	}
	return t, nil
}

// parseTypeBase parses a scalar type name or a `[]`-prefixed list type,
// recursively, with no nullable suffix of its own (see parseType).
func (p *parser) parseTypeBase() (ast.Type, error) {
	if p.cur().Kind == lexer.LBracket {
		p.advance()
		if _, err := p.expect(lexer.RBracket, "']'"); err != nil {
			return ast.Type{}, err
		}
		elem, err := p.parseTypeBase()
		if err != nil {
			return ast.Type{}, err
		}
		return ast.Type{Elem: &elem}, nil
	}
	if p.cur().Kind == lexer.Star {
		p.advance()
		elem, err := p.parseTypeBase()
		if err != nil {
			return ast.Type{}, err
		}
		// A pointer's zero value is always `none` (cascade_spec.md §2.2),
		// with no `?` written — see ast.Type's doc.
		return ast.Type{Ptr: &elem, Nullable: true}, nil
	}
	if name, ok := typeKeywords[p.cur().Kind]; ok {
		p.advance()
		return ast.Type{Name: name}, nil
	}
	if p.cur().Kind == lexer.Ident {
		// A struct type name (cascade_spec.md §4.1) — unlike int/float/
		// string/bool, struct names aren't reserved keywords, so they lex
		// as plain identifiers.
		name := p.advance()
		return ast.Type{Name: name.Literal}, nil
	}
	return ast.Type{}, fmt.Errorf("line %d: expected a type, got %q", p.cur().Line, p.cur().Literal)
}

func (p *parser) parseBlock() ([]ast.Stmt, error) {
	if _, err := p.expect(lexer.LBrace, "'{'"); err != nil {
		return nil, err
	}
	p.skipNewlines()

	var stmts []ast.Stmt
	for p.cur().Kind != lexer.RBrace {
		stmt, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, stmt)
		p.skipNewlines()
	}
	if _, err := p.expect(lexer.RBrace, "'}'"); err != nil {
		return nil, err
	}
	return stmts, nil
}

func (p *parser) parseStmt() (ast.Stmt, error) {
	switch p.cur().Kind {
	case lexer.KwReturn:
		return p.parseReturnStmt()
	case lexer.KwLet, lexer.KwConst:
		return p.parseLetDecl()
	case lexer.KwIf:
		return p.parseIfStmt()
	case lexer.KwWhile:
		return p.parseWhileStmt()
	case lexer.KwSwitch:
		return p.parseSwitchStmt()
	case lexer.KwFor:
		return p.parseForInStmt()
	case lexer.KwBreak:
		tok := p.advance()
		return &ast.BreakStmt{Line: tok.Line}, nil
	case lexer.KwContinue:
		tok := p.advance()
		return &ast.ContinueStmt{Line: tok.Line}, nil
	case lexer.Ident:
		return p.parseIdentStmt()
	case lexer.Star:
		return p.parseDerefAssignStmt()
	default:
		return nil, fmt.Errorf("line %d: unexpected token %q", p.cur().Line, p.cur().Literal)
	}
}

// parseIfStmt parses an if/elif/else chain. `elif`/`else` must follow the
// previous block's closing '}' on the same line (no newline in between):
// a newline there ends the if-statement, same as any other statement, so
// a lone `elif`/`else` on its own line is correctly a syntax error rather
// than silently chaining (mirrors Seed's identical if/elif/else parsing).
func (p *parser) parseIfStmt() (ast.Stmt, error) {
	kw := p.advance() // 'if'
	stmt := &ast.IfStmt{Line: kw.Line}

	clause, err := p.parseIfClause()
	if err != nil {
		return nil, err
	}
	stmt.Clauses = append(stmt.Clauses, clause)

	for p.cur().Kind == lexer.KwElif {
		p.advance()
		clause, err := p.parseIfClause()
		if err != nil {
			return nil, err
		}
		stmt.Clauses = append(stmt.Clauses, clause)
	}

	if p.cur().Kind == lexer.KwElse {
		p.advance()
		body, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		stmt.Else = body
	}

	return stmt, nil
}

func (p *parser) parseIfClause() (ast.IfClause, error) {
	cond, err := p.withStructLitForbidden(p.parseExpr)
	if err != nil {
		return ast.IfClause{}, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return ast.IfClause{}, err
	}
	return ast.IfClause{Cond: cond, Body: body}, nil
}

func (p *parser) parseWhileStmt() (ast.Stmt, error) {
	kw := p.advance() // 'while'
	cond, err := p.withStructLitForbidden(p.parseExpr)
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.WhileStmt{Cond: cond, Body: body, Line: kw.Line}, nil
}

// parseForInStmt parses `for x in List { ... }` (cascade_spec.md §7). The
// two-variable map form (`for k, v in m`) is parsed starting in Step 10,
// the step that introduces `map<K, V>`.
func (p *parser) parseForInStmt() (ast.Stmt, error) {
	kw := p.advance() // 'for'
	varName, err := p.expect(lexer.Ident, "loop variable name")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.KwIn, "'in'"); err != nil {
		return nil, err
	}
	list, err := p.withStructLitForbidden(p.parseExpr)
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.ForInStmt{VarName: varName.Literal, List: list, Body: body, Line: kw.Line}, nil
}

// parseSwitchStmt parses both switch forms (cascade_spec.md §7): tagged
// (`switch tag { case v1, v2: ... }`, immediately followed by '{') and
// untagged (`switch { case cond: ... }`). Only the tagged form allows a
// comma-separated case (the spec's untagged examples never show one —
// see ast.SwitchCase's doc).
func (p *parser) parseSwitchStmt() (ast.Stmt, error) {
	kw := p.advance() // 'switch'
	stmt := &ast.SwitchStmt{Line: kw.Line}

	if p.cur().Kind != lexer.LBrace {
		tag, err := p.withStructLitForbidden(p.parseExpr)
		if err != nil {
			return nil, err
		}
		stmt.Tag = tag
	}

	if _, err := p.expect(lexer.LBrace, "'{'"); err != nil {
		return nil, err
	}
	p.skipNewlines()

	for p.cur().Kind == lexer.KwCase {
		caseTok := p.advance()
		var values []ast.Expr
		for {
			v, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			values = append(values, v)
			if stmt.Tag == nil || p.cur().Kind != lexer.Comma {
				break
			}
			p.advance()
		}
		if _, err := p.expect(lexer.Colon, "':'"); err != nil {
			return nil, err
		}
		p.skipNewlines()
		body, err := p.parseCaseBody()
		if err != nil {
			return nil, err
		}
		stmt.Cases = append(stmt.Cases, ast.SwitchCase{Values: values, Body: body, Line: caseTok.Line})
	}

	if p.cur().Kind == lexer.KwDefault {
		p.advance()
		if _, err := p.expect(lexer.Colon, "':'"); err != nil {
			return nil, err
		}
		p.skipNewlines()
		body, err := p.parseCaseBody()
		if err != nil {
			return nil, err
		}
		stmt.Default = body
	}

	if _, err := p.expect(lexer.RBrace, "'}'"); err != nil {
		return nil, err
	}
	return stmt, nil
}

// parseCaseBody parses a `case`/`default` clause's statements. Unlike
// every other block, a case body is not wrapped in its own '{' '}' (see
// cascade_spec.md §7's examples) — it simply runs until the next `case`,
// `default`, or the switch's closing '}'.
func (p *parser) parseCaseBody() ([]ast.Stmt, error) {
	var stmts []ast.Stmt
	for p.cur().Kind != lexer.KwCase && p.cur().Kind != lexer.KwDefault && p.cur().Kind != lexer.RBrace {
		stmt, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, stmt)
		p.skipNewlines()
	}
	return stmts, nil
}

// parseLetDecl parses a `let`/`const` declaration (cascade_spec.md §4.2):
// `let name: Type`, `let name: Type = Init`, `let name = Init`, the
// `const` form (sema enforces that Init is required and no later
// assignment is allowed — see internal/sema), or, when a comma follows
// the first name, the multi-value form (§5, §8.5) `let n1, n2, ... =
// call(...)` — see parseMultiLetDecl.
func (p *parser) parseLetDecl() (ast.Stmt, error) {
	kw := p.advance() // 'let' or 'const'
	isConst := kw.Kind == lexer.KwConst

	name, err := p.expect(lexer.Ident, "variable name")
	if err != nil {
		return nil, err
	}

	if p.cur().Kind == lexer.Comma {
		return p.parseMultiLetDecl(kw, name, isConst)
	}

	decl := &ast.LetDecl{Const: isConst, Name: name.Literal, Line: kw.Line}

	if p.cur().Kind == lexer.Colon {
		p.advance()
		typ, err := p.parseType()
		if err != nil {
			return nil, err
		}
		decl.Type = typ
	}

	if p.cur().Kind == lexer.Assign {
		p.advance()
		init, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		decl.Init = init
	}

	return decl, nil
}

// parseMultiLetDecl parses the multi-value form of a `let`/`const`
// declaration (cascade_spec.md §5, §8.5) once parseLetDecl has already
// consumed the keyword and first name and seen the comma that follows
// it: `, name2, ... = call(...)`. There is no explicit-type form here —
// each name's type always comes from the callee's declared result types
// (resolved by sema.Check) — and the initializer must be a function
// call, the only expression shape that can produce multiple values.
func (p *parser) parseMultiLetDecl(kw, first lexer.Token, isConst bool) (ast.Stmt, error) {
	names := []string{first.Literal}
	for p.cur().Kind == lexer.Comma {
		p.advance()
		name, err := p.expect(lexer.Ident, "variable name")
		if err != nil {
			return nil, err
		}
		names = append(names, name.Literal)
	}
	if _, err := p.expect(lexer.Assign, "'='"); err != nil {
		return nil, err
	}
	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	call, ok := val.(*ast.CallExpr)
	if !ok {
		return nil, fmt.Errorf("line %d: multi-value 'let' requires a function call on the right-hand side", kw.Line)
	}
	return &ast.MultiLetDecl{Names: names, Const: isConst, Init: call, Line: kw.Line}, nil
}

// compoundAssignOps maps a compound-assignment token to its ast.Op string
// (cascade_spec.md §5).
var compoundAssignOps = map[lexer.Kind]string{
	lexer.PlusAssign:    "+",
	lexer.MinusAssign:   "-",
	lexer.StarAssign:    "*",
	lexer.SlashAssign:   "/",
	lexer.PercentAssign: "%",
}

// parseIdentStmt parses a statement starting with an identifier: a call
// expression (`f(...)`), a scalar or list-element assignment (`name =
// value` / `name[Index] = value`), a compound assignment (`name +=
// value` etc.), or `name++`/`name--` (cascade_spec.md §5).
func (p *parser) parseIdentStmt() (ast.Stmt, error) {
	name := p.advance() // Ident
	switch {
	case p.cur().Kind == lexer.LParen:
		call, err := p.parseCallExprFrom(name)
		if err != nil {
			return nil, err
		}
		return &ast.ExprStmt{X: call, Line: call.Line}, nil
	case p.cur().Kind == lexer.Dot:
		return p.parseFieldOrMethodStmt(name)
	case p.cur().Kind == lexer.LBracket:
		p.advance()
		index, err := p.withStructLitAllowed(p.parseExpr)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.RBracket, "']'"); err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.Assign, "'='"); err != nil {
			return nil, err
		}
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &ast.AssignStmt{Name: name.Literal, Index: index, Value: val, Line: name.Line}, nil
	case p.cur().Kind == lexer.Assign:
		p.advance()
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &ast.AssignStmt{Name: name.Literal, Value: val, Line: name.Line}, nil
	case compoundAssignOps[p.cur().Kind] != "":
		op := compoundAssignOps[p.cur().Kind]
		p.advance()
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &ast.CompoundAssignStmt{Name: name.Literal, Op: op, Value: val, Line: name.Line}, nil
	case p.cur().Kind == lexer.Inc || p.cur().Kind == lexer.Dec:
		opTok := p.advance()
		return &ast.IncDecStmt{Name: name.Literal, Op: opTok.Literal, Line: name.Line}, nil
	default:
		return nil, fmt.Errorf("line %d: expected '(', '=', a compound assignment, or '++'/'--' after %q", p.cur().Line, name.Literal)
	}
}

// parseFieldOrMethodStmt parses `name.field = value` (cascade_spec.md §5,
// §8.2's field write, valid whether name is a struct or a pointer to
// one) or `name.method(args)` used as its own statement (e.g.
// `pt.scale(2.0)`). Only a single level rooted at a plain identifier is
// supported — the spec's own examples never chain further (`a.b.c`).
func (p *parser) parseFieldOrMethodStmt(name lexer.Token) (ast.Stmt, error) {
	p.advance() // '.'
	fname, err := p.expect(lexer.Ident, "field or method name")
	if err != nil {
		return nil, err
	}
	if p.cur().Kind == lexer.LParen {
		call, err := p.parseCallExprFrom(fname)
		if err != nil {
			return nil, err
		}
		call.Receiver = &ast.Ident{Name: name.Literal, Line: name.Line}
		return &ast.ExprStmt{X: call, Line: call.Line}, nil
	}
	if _, err := p.expect(lexer.Assign, "'='"); err != nil {
		return nil, err
	}
	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &ast.AssignStmt{Name: name.Literal, Field: fname.Literal, Value: val, Line: name.Line}, nil
}

// parseDerefAssignStmt parses `*Ptr = Value` (cascade_spec.md §5), the
// one assignment form that isn't rooted at a plain identifier — its
// target is reached via parseUnary's existing "*" (dereference) handling
// (see unaryOpNames), which parseStmt dispatches to on seeing a leading
// '*'.
func (p *parser) parseDerefAssignStmt() (ast.Stmt, error) {
	expr, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	deref, ok := expr.(*ast.UnaryExpr)
	if !ok || deref.Op != "*" {
		return nil, fmt.Errorf("line %d: expected a dereference assignment '*ptr = value'", ast.ExprLine(expr))
	}
	if _, err := p.expect(lexer.Assign, "'='"); err != nil {
		return nil, err
	}
	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &ast.DerefAssignStmt{Ptr: deref.X, Value: val, Line: ast.ExprLine(expr)}, nil
}

func (p *parser) parseReturnStmt() (ast.Stmt, error) {
	kw := p.advance() // 'return'
	if p.cur().Kind == lexer.Newline || p.cur().Kind == lexer.RBrace {
		return &ast.ReturnStmt{Line: kw.Line}, nil
	}
	var results []ast.Expr
	for {
		x, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		results = append(results, x)
		if p.cur().Kind != lexer.Comma {
			break
		}
		p.advance()
	}
	return &ast.ReturnStmt{Results: results, Line: kw.Line}, nil
}

// parseExpr parses a full expression, following cascade_spec.md §6's
// precedence table (lowest to highest so far): ||, &&, ==/!=, </<=/>/>=,
// | (and ^ as XOR), & (and &^), <</>>, +/-, */%, unary !/-/~, then
// primaries. Postfix "?" (§8.6, Step 11), "."/"[]" (Steps 6/8), and the
// "|>" pipeline operator (Step 12) extend parsePrimary/parseExpr further
// in their own steps.
func (p *parser) parseExpr() (ast.Expr, error) {
	return p.parseOr()
}

// binOpNames maps a binary/logical operator token to its ast.BinaryExpr
// Op string.
var binOpNames = map[lexer.Kind]string{
	lexer.OrOr:    "||",
	lexer.AndAnd:  "&&",
	lexer.Eq:      "==",
	lexer.Neq:     "!=",
	lexer.Lt:      "<",
	lexer.Lte:     "<=",
	lexer.Gt:      ">",
	lexer.Gte:     ">=",
	lexer.Pipe:    "|",
	lexer.Caret:   "^",
	lexer.Amp:     "&",
	lexer.AndNot:  "&^",
	lexer.Shl:     "<<",
	lexer.Shr:     ">>",
	lexer.Plus:    "+",
	lexer.Minus:   "-",
	lexer.Star:    "*",
	lexer.Slash:   "/",
	lexer.Percent: "%",
}

// parseBinaryLevel implements one precedence level: it parses one operand
// via next, then folds in `next (op next)*` left-associatively for any of
// the given token kinds.
func (p *parser) parseBinaryLevel(next func() (ast.Expr, error), kinds ...lexer.Kind) (ast.Expr, error) {
	left, err := next()
	if err != nil {
		return nil, err
	}
	for kindIn(p.cur().Kind, kinds) {
		opTok := p.advance()
		right, err := next()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Op: binOpNames[opTok.Kind], X: left, Y: right, Line: opTok.Line}
	}
	return left, nil
}

func kindIn(k lexer.Kind, kinds []lexer.Kind) bool {
	for _, want := range kinds {
		if k == want {
			return true
		}
	}
	return false
}

func (p *parser) parseOr() (ast.Expr, error) {
	return p.parseBinaryLevel(p.parseAnd, lexer.OrOr)
}

func (p *parser) parseAnd() (ast.Expr, error) {
	return p.parseBinaryLevel(p.parseEquality, lexer.AndAnd)
}

func (p *parser) parseEquality() (ast.Expr, error) {
	return p.parseBinaryLevel(p.parseComparison, lexer.Eq, lexer.Neq)
}

func (p *parser) parseComparison() (ast.Expr, error) {
	return p.parseBinaryLevel(p.parseBitOr, lexer.Lt, lexer.Lte, lexer.Gt, lexer.Gte)
}

// parseBitOr folds "|" and "^" (bitwise XOR — binary only; unary bit-flip
// is "~", parsed in parseUnary) at the same precedence (§6 priority 7).
func (p *parser) parseBitOr() (ast.Expr, error) {
	return p.parseBinaryLevel(p.parseBitAnd, lexer.Pipe, lexer.Caret)
}

// parseBitAnd folds "&" and "&^" at the same precedence (§6 priority 6).
func (p *parser) parseBitAnd() (ast.Expr, error) {
	return p.parseBinaryLevel(p.parseShift, lexer.Amp, lexer.AndNot)
}

func (p *parser) parseShift() (ast.Expr, error) {
	return p.parseBinaryLevel(p.parseAdditive, lexer.Shl, lexer.Shr)
}

func (p *parser) parseAdditive() (ast.Expr, error) {
	return p.parseBinaryLevel(p.parseMultiplicative, lexer.Plus, lexer.Minus)
}

func (p *parser) parseMultiplicative() (ast.Expr, error) {
	return p.parseBinaryLevel(p.parseUnary, lexer.Star, lexer.Slash, lexer.Percent)
}

// unaryOpNames maps a prefix unary operator token to its ast.UnaryExpr Op
// string (§6 priority 2). "*" (dereference) and "&" (address-of) reuse
// the same tokens parseMultiplicative/parseBitAnd consume as *infix*
// operators — there's no ambiguity because parseUnary only ever looks at
// the current token where an *operand* is expected (the start of a
// unary/primary expression), never after a complete one, exactly
// mirroring how Go and C resolve the same lexical overload by position.
var unaryOpNames = map[lexer.Kind]string{
	lexer.Not:   "!",
	lexer.Minus: "-",
	lexer.Tilde: "~",
	lexer.Star:  "*",
	lexer.Amp:   "&",
}

func (p *parser) parseUnary() (ast.Expr, error) {
	if op, ok := unaryOpNames[p.cur().Kind]; ok {
		opTok := p.advance()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &ast.UnaryExpr{Op: op, X: x, Line: opTok.Line}, nil
	}
	return p.parsePrimary()
}

// parsePrimary parses a primary atom, then any number of `[index]` and
// `.field`/`.method(...)` suffixes (cascade_spec.md §5's list indexing,
// §4.1/§8.2's field/method access, §6 priority 1), and finally wraps the
// result in `is none`/`is not none` (§7) if one directly follows — the
// tightest binding possible, matching every spec example (always a bare
// identifier immediately followed by `is`).
func (p *parser) parsePrimary() (ast.Expr, error) {
	x, err := p.parsePrimaryAtom()
	if err != nil {
		return nil, err
	}
	for {
		switch p.cur().Kind {
		case lexer.LBracket:
			x, err = p.parseIndexSuffix(x)
		case lexer.Dot:
			x, err = p.parseSelectorSuffix(x)
		default:
			if p.cur().Kind == lexer.KwIs {
				return p.parseNullCheck(x)
			}
			return x, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// parseIndexSuffix parses the `[Index]` part of a list-index expression
// whose target x has already been parsed.
func (p *parser) parseIndexSuffix(x ast.Expr) (ast.Expr, error) {
	lb := p.advance() // '['
	index, err := p.withStructLitAllowed(p.parseExpr)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RBracket, "']'"); err != nil {
		return nil, err
	}
	return &ast.IndexExpr{X: x, Index: index, Line: lb.Line}, nil
}

// parseSelectorSuffix parses the `.field` or `.method(args...)` part of a
// field-access/method-call expression whose target x has already been
// parsed (cascade_spec.md §4.1/§8.2). A method call reuses ast.CallExpr
// via its Receiver field rather than a parallel expression type — see
// CallExpr's doc for the rationale.
func (p *parser) parseSelectorSuffix(x ast.Expr) (ast.Expr, error) {
	p.advance() // '.'
	name, err := p.expect(lexer.Ident, "field or method name")
	if err != nil {
		return nil, err
	}
	if p.cur().Kind == lexer.LParen {
		call, err := p.parseCallExprFrom(name)
		if err != nil {
			return nil, err
		}
		call.Receiver = x
		return call, nil
	}
	return &ast.FieldExpr{X: x, Field: name.Literal, Line: name.Line}, nil
}

// parseNullCheck parses the `is none`/`is not none` suffix onto an
// already-parsed atom x.
func (p *parser) parseNullCheck(x ast.Expr) (ast.Expr, error) {
	isTok := p.advance() // 'is'
	not := false
	if p.cur().Kind == lexer.KwNot {
		p.advance()
		not = true
	}
	if _, err := p.expect(lexer.KwNone, "'none'"); err != nil {
		return nil, err
	}
	return &ast.NullCheckExpr{X: x, Not: not, Line: isTok.Line}, nil
}

// parsePrimaryAtom parses a literal, identifier, call, or parenthesized
// expression (§6 priority 1's grouping form; ".", "[]", and postfix "?"
// join in later steps).
func (p *parser) parsePrimaryAtom() (ast.Expr, error) {
	tok := p.cur()
	switch tok.Kind {
	case lexer.LParen:
		p.advance()
		x, err := p.withStructLitAllowed(p.parseExpr)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.RParen, "')'"); err != nil {
			return nil, err
		}
		return x, nil
	case lexer.String:
		p.advance()
		return &ast.StringLit{Value: tok.Literal, Line: tok.Line}, nil
	case lexer.Int:
		p.advance()
		v, err := strconv.ParseInt(tok.Literal, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid integer literal %q", tok.Line, tok.Literal)
		}
		return &ast.IntLit{Value: v, Line: tok.Line}, nil
	case lexer.Float:
		p.advance()
		v, err := strconv.ParseFloat(tok.Literal, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid float literal %q", tok.Line, tok.Literal)
		}
		return &ast.FloatLit{Value: v, Line: tok.Line}, nil
	case lexer.KwTrue:
		p.advance()
		return &ast.BoolLit{Value: true, Line: tok.Line}, nil
	case lexer.KwFalse:
		p.advance()
		return &ast.BoolLit{Value: false, Line: tok.Line}, nil
	case lexer.KwNone:
		p.advance()
		return &ast.NoneLit{Line: tok.Line}, nil
	case lexer.LBracket:
		return p.parseListLit()
	case lexer.KwString, lexer.KwInt, lexer.KwFloat, lexer.KwBool:
		// A type keyword can also name a builtin conversion call, e.g.
		// string(x) (cascade_spec.md §13) — it's still a reserved
		// keyword (not an Ident), so it needs its own case here rather
		// than falling through to the Ident case below.
		name := p.advance()
		if p.cur().Kind == lexer.LParen {
			return p.parseCallExprFrom(name)
		}
		return nil, fmt.Errorf("line %d: unexpected token %q", name.Line, name.Literal)
	case lexer.Ident:
		name := p.advance()
		if p.cur().Kind == lexer.LParen {
			return p.parseCallExprFrom(name)
		}
		if p.cur().Kind == lexer.LBrace && !p.noStructLit {
			return p.parseStructLit(name)
		}
		return &ast.Ident{Name: name.Literal, Line: name.Line}, nil
	default:
		return nil, fmt.Errorf("line %d: unexpected token %q", tok.Line, tok.Literal)
	}
}

// parseListLit parses a list literal, e.g. `[1, 2, 3]` or `[]`
// (cascade_spec.md §3). Its element type isn't determined here — see
// ast.ListLit's doc.
func (p *parser) parseListLit() (ast.Expr, error) {
	kw := p.advance() // '['
	lit := &ast.ListLit{Line: kw.Line}
	for p.cur().Kind != lexer.RBracket {
		if len(lit.Elems) > 0 {
			if _, err := p.expect(lexer.Comma, "','"); err != nil {
				return nil, err
			}
		}
		elem, err := p.withStructLitAllowed(p.parseExpr)
		if err != nil {
			return nil, err
		}
		lit.Elems = append(lit.Elems, elem)
	}
	if _, err := p.expect(lexer.RBracket, "']'"); err != nil {
		return nil, err
	}
	return lit, nil
}

// parseCallExprFrom parses the `(args...)` part of a call expression whose
// callee identifier has already been consumed.
func (p *parser) parseCallExprFrom(name lexer.Token) (*ast.CallExpr, error) {
	if _, err := p.expect(lexer.LParen, "'('"); err != nil {
		return nil, err
	}
	call := &ast.CallExpr{Callee: name.Literal, Line: name.Line}
	for p.cur().Kind != lexer.RParen {
		if len(call.Args) > 0 {
			if _, err := p.expect(lexer.Comma, "','"); err != nil {
				return nil, err
			}
		}
		arg, err := p.withStructLitAllowed(p.parseExpr)
		if err != nil {
			return nil, err
		}
		call.Args = append(call.Args, arg)
	}
	if _, err := p.expect(lexer.RParen, "')'"); err != nil {
		return nil, err
	}
	return call, nil
}

// parseStructLit parses a struct literal `Name{field: value, ...}`
// (cascade_spec.md §4.1) whose type-name identifier has already been
// consumed. Every field must be given explicitly — see ast.StructLit's
// doc for why there is no partial/defaulted-field form.
func (p *parser) parseStructLit(name lexer.Token) (ast.Expr, error) {
	if _, err := p.expect(lexer.LBrace, "'{'"); err != nil {
		return nil, err
	}
	lit := &ast.StructLit{TypeName: name.Literal, Line: name.Line}
	for p.cur().Kind != lexer.RBrace {
		if len(lit.Fields) > 0 {
			if _, err := p.expect(lexer.Comma, "','"); err != nil {
				return nil, err
			}
		}
		fname, err := p.expect(lexer.Ident, "field name")
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.Colon, "':'"); err != nil {
			return nil, err
		}
		val, err := p.withStructLitAllowed(p.parseExpr)
		if err != nil {
			return nil, err
		}
		lit.Fields = append(lit.Fields, ast.StructFieldInit{Name: fname.Literal, Value: val})
	}
	if _, err := p.expect(lexer.RBrace, "'}'"); err != nil {
		return nil, err
	}
	return lit, nil
}
