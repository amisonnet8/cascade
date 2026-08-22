// Package lexer tokenizes Cascade source text (.cas) per
// cascade_spec.md §1: newlines are significant statement terminators, and
// "//" starts a line comment.
//
// Only the punctuation/operators Steps 1-8 need are lexed so far — "(",
// ")", "{", "}", "[", "]" (list literals/indexing/types, §2.2/§3/§5), "."
// (field/method access, §4.1/§8.2), ":", ",", "?" (nullable-type suffix,
// §2.3), "=" (plain assignment, §5), §6's arithmetic/comparison/logical
// operators ("+" "-" "*" "/" "%" "==" "!=" "<" "<=" ">" ">=" "&&" "||"
// "!"), §6's bitwise/shift operators ("&" "|" "^" "&^" "~" "<<" ">>"), and
// §5's compound-assignment/inc-dec set ("+=" "-=" "*=" "/=" "%=" "++"
// "--"). "&"/"*" double as unary address-of/dereference (§6) — the parser
// disambiguates by position, not the lexer (see parser's unaryOpNames).
// Postfix "?" (Step 11) and "|>" (Step 12, cascade_spec.md §9.2's
// pipeline-connect operator) are now lexed too. The full keyword set
// (§14) is already reserved from the start (see token.go), since adding a
// keyword later is free but an identifier silently becoming a keyword out
// from under existing code is not.
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

// lexNumber scans an integer or floating-point literal (cascade_spec.md
// §3). Unary minus is not part of the literal itself — `-1234` lexes as
// Minus followed by Int/Float, the same as any other unary expression
// (§6) — but that operator isn't lexed until Step 3.
func (l *Lexer) lexNumber(line int) (Token, error) {
	start := l.pos
	for l.pos < len(l.src) && isDigit(l.peekRune()) {
		l.pos++
	}
	isFloat := false
	if l.peekRune() == '.' && isDigit(l.peekRuneAt(1)) {
		isFloat = true
		l.pos++ // consume '.'
		for l.pos < len(l.src) && isDigit(l.peekRune()) {
			l.pos++
		}
	}
	lit := string(l.src[start:l.pos])
	if isFloat {
		return Token{Kind: Float, Literal: lit, Line: line}, nil
	}
	return Token{Kind: Int, Literal: lit, Line: line}, nil
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
		if r == '\\' {
			if err := l.lexStringEscape(&b, line); err != nil {
				return Token{}, err
			}
			continue
		}
		b.WriteRune(r)
		l.pos++
	}
}

// lexStringEscape decodes one backslash escape sequence inside a string
// literal into b, following Go's own double-quoted string escape grammar
// exactly (\a \b \f \n \r \t \v \\ \" — plus \xHH, \ooo, \uXXXX, \UXXXXXXXX
// for a raw byte or Unicode code point) — matching amivm_spec.md's own
// string-literal note, which defers individual escape validity entirely
// to Go's own re-parse of the generated source (see CLAUDE.md's "確定し
// た設計判断"). Decoding the escape here, at lex time, into the actual
// byte/rune it represents (rather than keeping the literal "\n" two-
// character sequence around) means ast.StringLit.Value always holds the
// real string value — codegen's existing strconv.Quote(v.Value) already
// re-escapes it correctly when emitting the AMIVM-IR literal, with no
// changes needed downstream of the lexer. l.pos is positioned at the
// backslash on entry; it consumes the backslash and everything the
// escape itself spans.
func (l *Lexer) lexStringEscape(b *strings.Builder, line int) error {
	l.pos++ // consume '\'
	if l.pos >= len(l.src) || l.peekRune() == '\n' {
		return fmt.Errorf("line %d: unterminated string literal", line)
	}
	r := l.peekRune()
	l.pos++
	switch r {
	case 'a':
		b.WriteByte('\a')
	case 'b':
		b.WriteByte('\b')
	case 'f':
		b.WriteByte('\f')
	case 'n':
		b.WriteByte('\n')
	case 'r':
		b.WriteByte('\r')
	case 't':
		b.WriteByte('\t')
	case 'v':
		b.WriteByte('\v')
	case '\\':
		b.WriteByte('\\')
	case '"':
		b.WriteByte('"')
	case 'x':
		v, err := l.readEscapeDigits(2, 16, line)
		if err != nil {
			return err
		}
		b.WriteByte(byte(v))
	case 'u':
		v, err := l.readEscapeDigits(4, 16, line)
		if err != nil {
			return err
		}
		b.WriteRune(rune(v))
	case 'U':
		v, err := l.readEscapeDigits(8, 16, line)
		if err != nil {
			return err
		}
		b.WriteRune(rune(v))
	default:
		if r < '0' || r > '7' {
			return fmt.Errorf("line %d: unknown escape sequence '\\%c'", line, r)
		}
		// \ooo: 3 octal digits total — r is the first (most significant),
		// readEscapeDigits reads the remaining two.
		rest, err := l.readEscapeDigits(2, 8, line)
		if err != nil {
			return err
		}
		v := int(r-'0')*64 + rest
		if v > 255 {
			return fmt.Errorf("line %d: octal escape value \\%c%02o exceeds 255 (max is \\377)", line, r, rest)
		}
		b.WriteByte(byte(v))
	}
	return nil
}

// readEscapeDigits reads exactly n more digits in the given base (16 for
// \x/\u/\U, 8 for \ooo — the escape's own leading character, if any, is
// already consumed by the caller) and returns their combined value.
func (l *Lexer) readEscapeDigits(n, base int, line int) (int, error) {
	v := 0
	for i := 0; i < n; i++ {
		if l.pos >= len(l.src) || l.peekRune() == '\n' {
			return 0, fmt.Errorf("line %d: unterminated string literal", line)
		}
		r := l.peekRune()
		d, ok := digitValue(r)
		if !ok || d >= base {
			return 0, fmt.Errorf("line %d: invalid escape sequence digit %q", line, r)
		}
		v = v*base + d
		l.pos++
	}
	return v, nil
}

func digitValue(r rune) (int, bool) {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0'), true
	case r >= 'a' && r <= 'f':
		return int(r-'a') + 10, true
	case r >= 'A' && r <= 'F':
		return int(r-'A') + 10, true
	default:
		return 0, false
	}
}

// lexOperator scans one punctuation/operator token. See the package doc
// for which tokens from cascade_spec.md §6 are recognized so far.
func (l *Lexer) lexOperator(line int) (Token, error) {
	r := l.advanceRune()

	// two matches a possible second rune to distinguish a two-character
	// operator (e.g. "==") from its one-character prefix (e.g. "=").
	two := func(next rune, twoKind, oneKind Kind) Token {
		if l.peekRune() == next {
			l.pos++
			return Token{Kind: twoKind, Literal: string(r) + string(next), Line: line}
		}
		return Token{Kind: oneKind, Literal: string(r), Line: line}
	}

	switch r {
	case '(':
		return Token{Kind: LParen, Literal: "(", Line: line}, nil
	case ')':
		return Token{Kind: RParen, Literal: ")", Line: line}, nil
	case '{':
		return Token{Kind: LBrace, Literal: "{", Line: line}, nil
	case '}':
		return Token{Kind: RBrace, Literal: "}", Line: line}, nil
	case '[':
		return Token{Kind: LBracket, Literal: "[", Line: line}, nil
	case ']':
		return Token{Kind: RBracket, Literal: "]", Line: line}, nil
	case ':':
		return Token{Kind: Colon, Literal: ":", Line: line}, nil
	case '.':
		return Token{Kind: Dot, Literal: ".", Line: line}, nil
	case ',':
		return Token{Kind: Comma, Literal: ",", Line: line}, nil
	case '?':
		return Token{Kind: Question, Literal: "?", Line: line}, nil
	case '+':
		if l.peekRune() == '+' {
			l.pos++
			return Token{Kind: Inc, Literal: "++", Line: line}, nil
		}
		return two('=', PlusAssign, Plus), nil
	case '-':
		if l.peekRune() == '-' {
			l.pos++
			return Token{Kind: Dec, Literal: "--", Line: line}, nil
		}
		return two('=', MinusAssign, Minus), nil
	case '*':
		return two('=', StarAssign, Star), nil
	case '/':
		return two('=', SlashAssign, Slash), nil
	case '%':
		return two('=', PercentAssign, Percent), nil
	case '=':
		return two('=', Eq, Assign), nil
	case '!':
		return two('=', Neq, Not), nil
	case '<':
		if l.peekRune() == '=' {
			l.pos++
			return Token{Kind: Lte, Literal: "<=", Line: line}, nil
		}
		if l.peekRune() == '<' {
			l.pos++
			return Token{Kind: Shl, Literal: "<<", Line: line}, nil
		}
		return Token{Kind: Lt, Literal: "<", Line: line}, nil
	case '>':
		if l.peekRune() == '=' {
			l.pos++
			return Token{Kind: Gte, Literal: ">=", Line: line}, nil
		}
		if l.peekRune() == '>' {
			l.pos++
			return Token{Kind: Shr, Literal: ">>", Line: line}, nil
		}
		return Token{Kind: Gt, Literal: ">", Line: line}, nil
	case '&':
		if l.peekRune() == '&' {
			l.pos++
			return Token{Kind: AndAnd, Literal: "&&", Line: line}, nil
		}
		if l.peekRune() == '^' {
			l.pos++
			return Token{Kind: AndNot, Literal: "&^", Line: line}, nil
		}
		return Token{Kind: Amp, Literal: "&", Line: line}, nil
	case '|':
		if l.peekRune() == '|' {
			l.pos++
			return Token{Kind: OrOr, Literal: "||", Line: line}, nil
		}
		if l.peekRune() == '>' {
			l.pos++
			return Token{Kind: PipeArrow, Literal: "|>", Line: line}, nil
		}
		return Token{Kind: Pipe, Literal: "|", Line: line}, nil
	case '^':
		return Token{Kind: Caret, Literal: "^", Line: line}, nil
	case '~':
		return Token{Kind: Tilde, Literal: "~", Line: line}, nil
	}
	return Token{}, fmt.Errorf("line %d: unexpected character %q", line, r)
}
