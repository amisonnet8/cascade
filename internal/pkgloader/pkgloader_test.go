package pkgloader_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amisonnet8/cascade/internal/codegen"
	"github.com/amisonnet8/cascade/internal/pkgloader"
	"github.com/amisonnet8/cascade/internal/sema"
)

// writeFiles materializes files (a relative path -> source content map)
// under a fresh temp directory and returns that directory's path.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}
	return root
}

// loadAndGenerate runs the full front end (pkgloader.Load, sema.Check,
// codegen.Generate) against root, failing the test on any error — mirrors
// codegen_test.go's own `generate` helper, extended to start from a
// package root instead of a single already-parsed string.
func loadAndGenerate(t *testing.T, root string) string {
	t.Helper()
	f, err := pkgloader.Load(root)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if err := sema.Check(f); err != nil {
		t.Fatalf("sema error: %v", err)
	}
	ir, err := codegen.Generate(f)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	return ir
}

func TestLoad_SingleFile(t *testing.T) {
	root := writeFiles(t, map[string]string{
		"main.cas": `
func main(): int {
	print("hello")
	return 0
}
`,
	})
	// A single .cas file path (not its directory) compiles just that
	// file — see Load's own doc for why this differs from a literal
	// reading of cascade_spec.md §11.1.
	ir := loadAndGenerate(t, filepath.Join(root, "main.cas"))
	if !strings.Contains(ir, "FUNC\t!cascade_main\t:\t^int") {
		t.Fatalf("expected a normal single-package program; got:\n%s", ir)
	}
}

func TestLoad_MultiFileSamePackage(t *testing.T) {
	root := writeFiles(t, map[string]string{
		"main.cas": `
func main(): int {
	let r: Reading = Reading{sensorId: 1, value: 2.5}
	print(string(r.value))
	return 0
}
`,
		"reading.cas": `
struct Reading {
	sensorId: int
	value: float
}

func (r: Reading) value2(): float {
	return r.value
}
`,
	})
	ir := loadAndGenerate(t, root)
	if !strings.Contains(ir, "STTYPE\t^Reading\n") {
		t.Fatalf("expected 'Reading' from reading.cas to be visible to main.cas without any import (cascade_spec.md §11.1); got:\n%s", ir)
	}
}

func TestLoad_ImportPubAndPrefixing(t *testing.T) {
	root := writeFiles(t, map[string]string{
		"main.cas": `
import mathutil "./mathutil"

func main(): int {
	let r = mathutil.Clamp(150, 0, 100)
	print(string(r))
	let v: mathutil.Vector = mathutil.Vector{x: 3.0, y: 4.0}
	print(string(v.magnitudeSquared()))
	print(mathutil.Version)
	return 0
}
`,
		"mathutil/mathutil.cas": `
pub const Version: string = "1.0"

pub func Clamp(value: int, min: int, max: int): int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func helper(): int {
	return 0
}

pub struct Vector {
	x: float
	y: float
}

pub func (v: Vector) magnitudeSquared(): float {
	return v.x * v.x + v.y * v.y
}
`,
	})
	ir := loadAndGenerate(t, root)

	// Every one of mathutil's own top-level declarations must be
	// prefixed (cascade_spec.md §11.6), including the non-pub `helper`
	// (prefixing avoids cross-package name collisions regardless of
	// pub-ness — only cross-package *visibility* depends on pub).
	for _, want := range []string{
		"STTYPE\t^mathutil_Vector\n",
		"GVAR\t@mathutil_Version\t^string\n",
		"FUNC\t!mathutil_Clamp\t",
		"FUNC\t!mathutil_helper\t",
		"FUNC\t!mathutil_Vector_magnitudeSquared\t",
	} {
		if !strings.Contains(ir, want) {
			t.Fatalf("expected IR to contain %q; got:\n%s", want, ir)
		}
	}
	// The root package's own main must stay unprefixed (§11.6: "ルート
	// パッケージの識別子には接頭辞を付けない").
	if !strings.Contains(ir, "FUNC\t!cascade_main\t:\t^int") {
		t.Fatalf("expected the root package's main to stay unprefixed; got:\n%s", ir)
	}
	if !strings.Contains(ir, "CALL\t:\t!cascade_init\n") {
		t.Fatalf("expected !main to call the synthesized !cascade_init before !cascade_main; got:\n%s", ir)
	}
}

func TestLoad_Errors(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{
			name: "non-pub function is invisible outside its own package",
			files: map[string]string{
				"main.cas": `
import helper "./helper"
func main(): int {
	let x = helper.secret()
	print(string(x))
	return 0
}
`,
				"helper/helper.cas": `
func secret(): int {
	return 42
}
`,
			},
			wantErr: "not declared 'pub'",
		},
		{
			name: "circular import is rejected",
			files: map[string]string{
				"main.cas": `
import a "./a"
func main(): int {
	print(string(a.fromA()))
	return 0
}
`,
				"a/a.cas": `
import b "../b"
pub func fromA(): int {
	return 1
}
`,
				"b/b.cas": `
import a "../a"
pub func fromB(): int {
	return 2
}
`,
			},
			wantErr: "circular import",
		},
		{
			name: "main in a non-root package is rejected",
			files: map[string]string{
				"main.cas": `
import sub "./sub"
func main(): int {
	return 0
}
`,
				"sub/sub.cas": `
pub func main(): int {
	return 0
}
`,
			},
			wantErr: "'main' can only be declared in the root package",
		},
		{
			name: "an undeclared import qualifier is rejected",
			files: map[string]string{
				"main.cas": `
func main(): int {
	let x = nope.Something()
	print(string(x))
	return 0
}
`,
			},
			wantErr: "undefined name",
		},
		{
			name: "an unknown import path fails to load",
			files: map[string]string{
				"main.cas": `
import nope "./does-not-exist"
func main(): int {
	return 0
}
`,
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeFiles(t, tt.files)
			f, loadErr := pkgloader.Load(root)
			var err error = loadErr
			if err == nil {
				err = sema.Check(f)
			}
			if err == nil {
				t.Fatalf("expected an error, got none")
			}
			if tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoad_TopLevelLetVisibleEverywhere(t *testing.T) {
	root := writeFiles(t, map[string]string{
		"main.cas": `
let counter: int = 10

func main(): int {
	print(string(counter))
	print(string(bump()))
	return 0
}

func bump(): int {
	return counter + 1
}
`,
	})
	ir := loadAndGenerate(t, root)
	if !strings.Contains(ir, "GVAR\t@counter\t^int\n") {
		t.Fatalf("expected a top-level let to compile to GVAR; got:\n%s", ir)
	}
	if !strings.Contains(ir, "FUNC\t!cascade_init\t:\n") || !strings.Contains(ir, "SET\t@counter\t10\n") {
		t.Fatalf("expected !cascade_init to initialize the global; got:\n%s", ir)
	}
	bumpIR := ir[strings.Index(ir, "FUNC\t!bump"):]
	if !strings.Contains(bumpIR, "@counter") {
		t.Fatalf("expected bump() (a different function than the one declaring counter) to see the top-level let; got:\n%s", bumpIR)
	}
}
