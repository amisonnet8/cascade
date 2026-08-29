package parser

import (
	"testing"

	"github.com/amisonnet8/cascade/internal/ast"
)

// TestParseIntLiteral_BasesAndSeparators locks down parseIntLiteral's
// base selection and separator stripping (cascade_spec.md §3.2). The key
// case to protect is "0755": a legacy leading-zero decimal must still
// mean decimal 755, not octal (which Go's own base-0 strconv.ParseInt
// would produce — see parseIntLiteral's doc for why that path isn't
// used).
func TestParseIntLiteral_BasesAndSeparators(t *testing.T) {
	tests := []struct {
		name string
		lit  string
		want int64
	}{
		{"plain decimal", "1234", 1234},
		{"bare zero", "0", 0},
		{"legacy leading-zero decimal stays decimal", "0755", 755},
		{"decimal with digit separators", "1_000_000", 1000000},
		{"hex lowercase", "0x1a", 26},
		{"hex uppercase prefix and digits", "0X1A", 26},
		{"hex with separators", "0xFF_FF", 65535},
		{"hex separator right after prefix", "0x_1A", 26},
		{"octal lowercase", "0o17", 15},
		{"octal uppercase", "0O17", 15},
		{"binary lowercase", "0b101", 5},
		{"binary uppercase", "0B101", 5},
		{"binary with separators", "0b1010_0101", 165},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseIntLiteral(tt.lit)
			if err != nil {
				t.Fatalf("parseIntLiteral(%q) error = %v, want nil", tt.lit, err)
			}
			if got != tt.want {
				t.Fatalf("parseIntLiteral(%q) = %d, want %d", tt.lit, got, tt.want)
			}
		})
	}
}

// TestParse_IntLiteralEndToEnd exercises the same forms through the full
// parser (not just parseIntLiteral directly), confirming the Int token
// produced by the lexer round-trips into the right ast.IntLit.Value.
func TestParse_IntLiteralEndToEnd(t *testing.T) {
	src := "func main(): int {\n" +
		"    let hex: int = 0x1A\n" +
		"    let oct: int = 0o17\n" +
		"    let bin: int = 0b101\n" +
		"    let million: int = 1_000_000\n" +
		"    let legacy: int = 0755\n" +
		"    return 0\n" +
		"}\n"
	file, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil", err)
	}
	want := map[string]int64{
		"hex": 26, "oct": 15, "bin": 5, "million": 1000000, "legacy": 755,
	}
	if len(file.Funcs) != 1 {
		t.Fatalf("Parse() produced %d funcs, want 1", len(file.Funcs))
	}
	got := map[string]int64{}
	for _, stmt := range file.Funcs[0].Body {
		let, ok := stmt.(*ast.LetDecl)
		if !ok {
			continue
		}
		lit, ok := let.Init.(*ast.IntLit)
		if !ok {
			continue
		}
		got[let.Name] = lit.Value
	}
	for name, wantVal := range want {
		gotVal, ok := got[name]
		if !ok {
			t.Errorf("let %s not found among parsed IntLit inits", name)
			continue
		}
		if gotVal != wantVal {
			t.Errorf("let %s = %d, want %d", name, gotVal, wantVal)
		}
	}
}
