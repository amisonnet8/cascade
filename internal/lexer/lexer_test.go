package lexer

import (
	"strings"
	"testing"
)

// lexOneString tokenizes src (expected to be exactly one string literal,
// e.g. `"a\nb"`) and returns its decoded value.
func lexOneString(t *testing.T, src string) string {
	t.Helper()
	toks, err := Tokenize(src)
	if err != nil {
		t.Fatalf("Tokenize(%q) = %v, want nil error", src, err)
	}
	if len(toks) < 1 || toks[0].Kind != String {
		t.Fatalf("Tokenize(%q) = %v, want a single String token first", src, toks)
	}
	return toks[0].Literal
}

// TestLexString_EscapeSequences follows Go's own double-quoted string
// escape grammar exactly (cascade_spec.md's string literals conform to
// AMIVM and Go — see amivm_spec.md's string-literal note and CLAUDE.md's
// "確定した設計判断"): the lexer decodes each escape into the actual
// byte/rune it represents, so ast.StringLit.Value always holds the real
// string value (codegen's strconv.Quote re-escapes it when emitting IR).
func TestLexString_EscapeSequences(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"named newline", `"a\nb"`, "a\nb"},
		{"named tab", `"a\tb"`, "a\tb"},
		{"named carriage return", `"a\rb"`, "a\rb"},
		{"named alert", `"\a"`, "\a"},
		{"named backspace", `"\b"`, "\b"},
		{"named form feed", `"\f"`, "\f"},
		{"named vertical tab", `"\v"`, "\v"},
		{"escaped backslash", `"a\\b"`, "a\\b"},
		{"escaped double quote", `"say \"hi\""`, `say "hi"`},
		{"hex byte escape", `"\x41\x42"`, "AB"},
		{"unicode 4-hex escape", `"あ"`, "あ"},
		{"unicode 8-hex escape", `"\U0001F600"`, "😀"},
		{"octal escape", `"\101\102"`, "AB"},
		{"max octal escape", `"\377"`, "\xff"},
		{"mixed escapes and literal text", `"line1\nline2\ttabbed あ"`, "line1\nline2\ttabbed あ"},
		{"literal unescaped unicode still works", `"あいう"`, "あいう"},
		{"empty string", `""`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lexOneString(t, tt.src)
			if got != tt.want {
				t.Fatalf("Tokenize(%s) = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

func TestLexString_EscapeErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{"unknown escape letter", `"\q"`, "unknown escape sequence"},
		{"backslash at end of string", `"abc\`, "unterminated string literal"},
		{"backslash immediately before newline", "\"abc\\\nb\"", "unterminated string literal"},
		{"hex escape cut short by closing quote", `"\x4"`, "invalid escape sequence digit"},
		{"hex escape cut short by EOF", `"\x4`, "unterminated string literal"},
		{"invalid hex digit", `"\x4g"`, "invalid escape sequence digit"},
		{"unicode-4 escape cut short by closing quote", `"\u304"`, "invalid escape sequence digit"},
		{"unicode-8 escape cut short by closing quote", `"\U0001F60"`, "invalid escape sequence digit"},
		{"octal escape value exceeds 255", `"\400"`, "exceeds 255"},
		{"invalid octal digit", `"\128"`, "invalid escape sequence digit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Tokenize(tt.src)
			if err == nil {
				t.Fatalf("Tokenize(%s) = nil error, want error containing %q", tt.src, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Tokenize(%s) = %q, want error containing %q", tt.src, err.Error(), tt.wantErr)
			}
		})
	}
}

// lexOneInt tokenizes src (expected to be exactly one integer literal)
// and returns its raw Literal text — the lexer keeps base prefixes and
// '_' separators verbatim (parseIntLiteral, in the parser package, is
// what strips them and picks the base — see its doc for why).
func lexOneInt(t *testing.T, src string) string {
	t.Helper()
	toks, err := Tokenize(src)
	if err != nil {
		t.Fatalf("Tokenize(%q) = %v, want nil error", src, err)
	}
	if len(toks) < 1 || toks[0].Kind != Int {
		t.Fatalf("Tokenize(%q) = %v, want a single Int token first", src, toks)
	}
	return toks[0].Literal
}

// TestLexNumber_IntLiteralForms covers the base prefixes and digit
// separators cascade_spec.md §3.2 adds on top of plain decimal
// (mirroring amivm_spec.md §6, itself Go 1.13+'s native integer literal
// grammar). The lexer only recognizes and passes these through — legacy
// leading-zero decimals like "0755" keep meaning decimal 755, exactly as
// before this change (see parseIntLiteral's doc for why that matters).
func TestLexNumber_IntLiteralForms(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"plain decimal", "1234", "1234"},
		{"bare zero", "0", "0"},
		{"legacy leading-zero decimal unaffected", "0755", "0755"},
		{"decimal digit separators", "1_000_000", "1_000_000"},
		{"hex lowercase prefix", "0x1a", "0x1a"},
		{"hex uppercase prefix", "0X1A", "0X1A"},
		{"hex with separators", "0xFF_FF", "0xFF_FF"},
		{"hex separator right after prefix", "0x_1A", "0x_1A"},
		{"octal lowercase prefix", "0o17", "0o17"},
		{"octal uppercase prefix", "0O17", "0O17"},
		{"binary lowercase prefix", "0b101", "0b101"},
		{"binary uppercase prefix", "0B101", "0B101"},
		{"binary with separators", "0b1010_0101", "0b1010_0101"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lexOneInt(t, tt.src)
			if got != tt.want {
				t.Fatalf("Tokenize(%s) literal = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

func TestLexNumber_IntLiteralErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{"trailing separator", "1_", "cannot appear at the end"},
		{"doubled separator", "1__0", "cannot appear twice in a row"},
		{"hex prefix with no digits", "0x", "expected at least one digit"},
		{"hex prefix with only a separator", "0x_", "expected at least one digit after '_'"},
		{"doubled separator right after hex prefix", "0x__1", "cannot appear at the start"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Tokenize(tt.src)
			if err == nil {
				t.Fatalf("Tokenize(%s) = nil error, want error containing %q", tt.src, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Tokenize(%s) = %q, want error containing %q", tt.src, err.Error(), tt.wantErr)
			}
		})
	}
}
