// Package parser builds an ast.File from Cascade source code via
// hand-written recursive-descent parsing (see CLAUDE.md's "確定した設計判断"
// for why no parser generator is used).
//
// The grammar implemented here is intentionally a small subset of
// cascade_spec.md: a top-level func declaration with a scalar parameter
// list and an optional single scalar return type, a block of statements
// limited to `let`/`const` declarations, a call-expression statement,
// scalar assignment, and return, and expressions limited to literals
// (including nullable-type `T?` declarations and the `none` literal),
// identifiers, and calls — enough for Steps 1-2 (bootstrap; variables,
// scalar types, and null semantics). Later development steps extend this
// grammar one feature at a time; expressions in particular grow into a
// full operator-precedence (Pratt) parser once Step 3 lands (cascade_spec.md
// §6's precedence table), and `is none`/`is not none` (§7) is parsed
// starting in Step 5, the only place the spec actually uses it (as an
// `if` condition).
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
		default:
			return nil, fmt.Errorf("line %d: expected 'func' at top level, got %q", p.cur().Line, p.cur().Literal)
		}
		p.skipNewlines()
	}
	return f, nil
}

func (p *parser) parseFuncDecl() (*ast.FuncDecl, error) {
	kw, err := p.expect(lexer.KwFunc, "'func'")
	if err != nil {
		return nil, err
	}
	fn := &ast.FuncDecl{Line: kw.Line}

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
		typ, err := p.parseType()
		if err != nil {
			return nil, err
		}
		fn.Results = []ast.Type{typ}
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
	return ast.Param{Type: typ, Name: name.Literal}, nil
}

// parseType parses a scalar type name with an optional nullable suffix
// (cascade_spec.md §2.1, §2.3: `int`, `int?`, ...). Every non-scalar type
// form is parsed starting in the step that introduces it.
func (p *parser) parseType() (ast.Type, error) {
	name, ok := typeKeywords[p.cur().Kind]
	if !ok {
		return ast.Type{}, fmt.Errorf("line %d: expected a type, got %q", p.cur().Line, p.cur().Literal)
	}
	p.advance()
	t := ast.Type{Name: name}
	if p.cur().Kind == lexer.Question {
		p.advance()
		t.Nullable = true
	}
	return t, nil
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
	case lexer.Ident:
		return p.parseIdentStmt()
	default:
		return nil, fmt.Errorf("line %d: unexpected token %q", p.cur().Line, p.cur().Literal)
	}
}

// parseLetDecl parses a `let`/`const` declaration (cascade_spec.md §4.2):
// `let name: Type`, `let name: Type = Init`, `let name = Init`, or the
// `const` form (sema enforces that Init is required and no later
// assignment is allowed — see internal/sema).
func (p *parser) parseLetDecl() (ast.Stmt, error) {
	kw := p.advance() // 'let' or 'const'
	decl := &ast.LetDecl{Const: kw.Kind == lexer.KwConst, Line: kw.Line}

	name, err := p.expect(lexer.Ident, "variable name")
	if err != nil {
		return nil, err
	}
	decl.Name = name.Literal

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

// parseIdentStmt parses a statement starting with an identifier: a call
// expression (`f(...)`) or a scalar assignment (`name = value`,
// cascade_spec.md §5).
func (p *parser) parseIdentStmt() (ast.Stmt, error) {
	name := p.advance() // Ident
	switch p.cur().Kind {
	case lexer.LParen:
		call, err := p.parseCallExprFrom(name)
		if err != nil {
			return nil, err
		}
		return &ast.ExprStmt{X: call, Line: call.Line}, nil
	case lexer.Assign:
		p.advance()
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &ast.AssignStmt{Name: name.Literal, Value: val, Line: name.Line}, nil
	default:
		return nil, fmt.Errorf("line %d: expected '(' or '=' after %q", p.cur().Line, name.Literal)
	}
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
// string (§6 priority 2). "*"/"&" (pointers) join once Step 8 lands.
var unaryOpNames = map[lexer.Kind]string{
	lexer.Not:   "!",
	lexer.Minus: "-",
	lexer.Tilde: "~",
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

// parsePrimary parses a literal, identifier, call, or parenthesized
// expression (§6 priority 1's grouping form; ".", "[]", and postfix "?"
// join in later steps).
func (p *parser) parsePrimary() (ast.Expr, error) {
	tok := p.cur()
	switch tok.Kind {
	case lexer.LParen:
		p.advance()
		x, err := p.parseExpr()
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
		return &ast.Ident{Name: name.Literal, Line: name.Line}, nil
	default:
		return nil, fmt.Errorf("line %d: unexpected token %q", tok.Line, tok.Literal)
	}
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
		arg, err := p.parseExpr()
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
