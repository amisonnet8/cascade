// Package lexer tokenizes Cascade source text (.cascade) per
// cascade_spec.md §1: newlines are significant statement terminators, and
// "//" starts a line comment.
//
// Only the punctuation Step 1 (bootstrap) needs is lexed as an operator so
// far — "(", ")", "{", "}", ":", "," — since cascade_spec.md §6's full
// operator set belongs to Step 3 (arithmetic/comparison/logical) and Step 4
// (bitwise/shift). The full keyword set (§14) is already reserved from the
// start (see token.go), since adding a keyword later is free but an
// identifier silently becoming a keyword out from under existing code is
// not.
package lexer

import (
	"fmt"
	"strings"
)

// Lexer tokenizes Cascade source code.
type Lexer struct {
	src  []rune
	pos  int
	line int
}

// New creates a Lexer over src.
func New(src string) *Lexer {
	return &Lexer{src: []rune(src), pos: 0, line: 1}
}

// Tokenize scans the whole source and returns its token stream, ending
// with a single EOF token.
func Tokenize(src string) ([]Token, error) {
	l := New(src)
	var toks []Token
	for {
		tok, err := l.next()
		if err != nil {
			return nil, err
		}
		toks = append(toks, tok)
		if tok.Kind == EOF {
			return toks, nil
		}
	}
}

func (l *Lexer) peekRune() rune {
	if l.pos >= len(l.src) {
		return 0
	}
	return l.src[l.pos]
}

func (l *Lexer) peekRuneAt(offset int) rune {
	if l.pos+offset >= len(l.src) {
		return 0
	}
	return l.src[l.pos+offset]
}

func (l *Lexer) advanceRune() rune {
	r := l.src[l.pos]
	l.pos++
	if r == '\n' {
		l.line++
	}
	return r
}

func (l *Lexer) next() (Token, error) {
	l.skipSpacesAndComments()

	line := l.line
	if l.pos >= len(l.src) {
		return Token{Kind: EOF, Line: line}, nil
	}

	r := l.peekRune()

	if r == '\n' {
		l.skipNewlines()
		return Token{Kind: Newline, Line: line}, nil
	}

	switch {
	case isIdentStart(r):
		return l.lexIdent(line), nil
	case isDigit(r):
		return l.lexNumber(line)
	case r == '"':
		return l.lexString(line)
	}

	return l.lexOperator(line)
}

// skipSpacesAndComments skips spaces, tabs, and // comments, but leaves
// newlines in place: they are significant statement terminators.
func (l *Lexer) skipSpacesAndComments() {
	for l.pos < len(l.src) {
		r := l.peekRune()
		switch {
		case r == ' ' || r == '\t' || r == '\r':
			l.pos++
		case r == '/' && l.peekRuneAt(1) == '/':
			for l.pos < len(l.src) && l.peekRune() != '\n' {
				l.pos++
			}
		default:
			return
		}
	}
}

// skipNewlines consumes one or more newlines (interleaved with
// whitespace/comments), collapsing them into a single Newline token.
func (l *Lexer) skipNewlines() {
	for {
		l.skipSpacesAndComments()
		if l.pos < len(l.src) && l.peekRune() == '\n' {
			l.advanceRune()
			continue
		}
		return
	}
}

func isIdentStart(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || isDigit(r)
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func (l *Lexer) lexIdent(line int) Token {
	start := l.pos
	for l.pos < len(l.src) && isIdentPart(l.peekRune()) {
		l.pos++
	}
	lit := string(l.src[start:l.pos])
	if kw, ok := keywords[lit]; ok {
		return Token{Kind: kw, Literal: lit, Line: line}
	}
	return Token{Kind: Ident, Literal: lit, Line: line}
}

// lexNumber scans an integer literal. Float literals (cascade_spec.md §3)
// are lexed starting in the step that introduces the float type.
func (l *Lexer) lexNumber(line int) (Token, error) {
	start := l.pos
	for l.pos < len(l.src) && isDigit(l.peekRune()) {
		l.pos++
	}
	return Token{Kind: Int, Literal: string(l.src[start:l.pos]), Line: line}, nil
}

func (l *Lexer) lexString(line int) (Token, error) {
	l.pos++ // consume opening '"'
	var b strings.Builder
	for {
		if l.pos >= len(l.src) {
			return Token{}, fmt.Errorf("line %d: unterminated string literal", line)
		}
		r := l.peekRune()
		if r == '"' {
			l.pos++
			return Token{Kind: String, Literal: b.String(), Line: line}, nil
		}
		if r == '\n' {
			return Token{}, fmt.Errorf("line %d: unterminated string literal", line)
		}
		b.WriteRune(r)
		l.pos++
	}
}

// lexOperator scans one punctuation token. See the package doc for which
// tokens from cascade_spec.md §6 are recognized so far.
func (l *Lexer) lexOperator(line int) (Token, error) {
	r := l.advanceRune()
	switch r {
	case '(':
		return Token{Kind: LParen, Literal: "(", Line: line}, nil
	case ')':
		return Token{Kind: RParen, Literal: ")", Line: line}, nil
	case '{':
		return Token{Kind: LBrace, Literal: "{", Line: line}, nil
	case '}':
		return Token{Kind: RBrace, Literal: "}", Line: line}, nil
	case ':':
		return Token{Kind: Colon, Literal: ":", Line: line}, nil
	case ',':
		return Token{Kind: Comma, Literal: ",", Line: line}, nil
	}
	return Token{}, fmt.Errorf("line %d: unexpected character %q", line, r)
}
