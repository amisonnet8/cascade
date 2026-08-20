// Package parser builds an ast.File from Cascade source code via
// hand-written recursive-descent parsing (see CLAUDE.md's "確定した設計判断"
// for why no parser generator is used).
//
// The grammar implemented here is intentionally a small subset of
// cascade_spec.md: a top-level func declaration with a scalar parameter
// list and an optional single scalar return type, a block of statements
// limited to a call-expression statement and return, and expressions
// limited to string/int literals, identifiers, and calls — just enough for
// Step 1 (bootstrap). Later development steps extend this grammar one
// feature at a time; expressions in particular grow into a full
// operator-precedence (Pratt) parser once Step 3 lands (cascade_spec.md
// §6's precedence table).
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

// parseType parses a scalar type name (cascade_spec.md §2.1). The nullable
// suffix (§2.3) and every non-scalar type form are parsed starting in the
// step that introduces them.
func (p *parser) parseType() (ast.Type, error) {
	name, ok := typeKeywords[p.cur().Kind]
	if !ok {
		return ast.Type{}, fmt.Errorf("line %d: expected a type, got %q", p.cur().Line, p.cur().Literal)
	}
	p.advance()
	return ast.Type{Name: name}, nil
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
	case lexer.Ident:
		return p.parseIdentStmt()
	default:
		return nil, fmt.Errorf("line %d: unexpected token %q", p.cur().Line, p.cur().Literal)
	}
}

// parseIdentStmt parses a statement starting with an identifier. Only a
// call expression (`f(...)`) is valid here so far; assignment lands in
// Step 2.
func (p *parser) parseIdentStmt() (ast.Stmt, error) {
	name := p.advance() // Ident
	if p.cur().Kind != lexer.LParen {
		return nil, fmt.Errorf("line %d: expected '(' after %q", p.cur().Line, name.Literal)
	}
	call, err := p.parseCallExprFrom(name)
	if err != nil {
		return nil, err
	}
	return &ast.ExprStmt{X: call, Line: call.Line}, nil
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

// parseExpr parses one expression. Only literals, identifiers, and calls
// are supported so far; the full operator-precedence grammar (§6) lands in
// Step 3.
func (p *parser) parseExpr() (ast.Expr, error) {
	tok := p.cur()
	switch tok.Kind {
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
