package sema_test

import (
	"strings"
	"testing"

	"github.com/amisonnet8/cascade/internal/parser"
	"github.com/amisonnet8/cascade/internal/sema"
)

func check(t *testing.T, src string) error {
	t.Helper()
	f, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return sema.Check(f)
}

func TestCheck_ValidProgram(t *testing.T) {
	src := `
func main(): int {
	let greeting: string = "hi"
	print(greeting)

	let name: string? = "Cascade"
	name = none

	const version: string = "0.1.0"

	let count = 3
	let pi: float = 3.14
	let ready: bool = true
	let empty: int?

	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_ValidOperators(t *testing.T) {
	src := `
func main(): int {
	let sum = 2 + 3 * 4
	let grouped = (2 + 3) * 4
	let ratio = 7 / 2
	let remainder = 7 % 2
	let negated = -sum

	let isBig = sum > 5
	let isEqual = sum == 14
	let combined = isBig && !isEqual || isEqual

	let greeting = "Hello, " + "Cascade" + "!"
	print(greeting)
	print(string(sum) + string(grouped) + string(ratio) + string(remainder) + string(negated))
	print(string(combined))

	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_ValidBitwiseAndShiftOperators(t *testing.T) {
	src := `
func main(): int {
	let a = 12
	let b = 10
	let and = a & b
	let or = a | b
	let xor = a ^ b
	let andNot = a &^ b
	let inverted = ~a
	let shiftedLeft = a << 2
	let shiftedRight = a >> 2
	print(string(and) + string(or) + string(xor) + string(andNot) + string(inverted) + string(shiftedLeft) + string(shiftedRight))
	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_BitwiseAndShiftErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "bitwise and requires int operands",
			src: `
func main(): int {
	let x = 1.0 & 2.0
	return 0
}
`,
			wantErr: "operator \"&\" requires int operands",
		},
		{
			name: "bitwise or rejects bool",
			src: `
func main(): int {
	let x = true | false
	return 0
}
`,
			wantErr: "operator \"|\" requires int operands",
		},
		{
			name: "shift left requires int operands",
			src: `
func main(): int {
	let x = "a" << "b"
	return 0
}
`,
			wantErr: "operator \"<<\" requires int operands",
		},
		{
			name: "and-not requires int operands",
			src: `
func main(): int {
	let x = 1.5 &^ 2.5
	return 0
}
`,
			wantErr: "operator \"&^\" requires int operands",
		},
		{
			name: "unary bitwise not requires int",
			src: `
func main(): int {
	let x = ~1.5
	return 0
}
`,
			wantErr: "unary ~ requires int",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, tt.src)
			if err == nil {
				t.Fatalf("Check() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check() = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCheck_OperatorErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "mismatched operand types",
			src: `
func main(): int {
	let x = 1 + 1.0
	return 0
}
`,
			wantErr: "mismatched operand types int and float",
		},
		{
			name: "modulo requires int",
			src: `
func main(): int {
	let x = 1.0 % 2.0
	return 0
}
`,
			wantErr: "operator % requires int operands",
		},
		{
			name: "logical and requires bool",
			src: `
func main(): int {
	let x = 1 && 2
	return 0
}
`,
			wantErr: "operator \"&&\" requires bool operands",
		},
		{
			name: "comparison rejects bool",
			src: `
func main(): int {
	let x = true < false
	return 0
}
`,
			wantErr: "operator \"<\" requires int, float, or string operands",
		},
		{
			name: "unary minus requires numeric operand",
			src: `
func main(): int {
	let x = -"oops"
	return 0
}
`,
			wantErr: "unary - requires int or float",
		},
		{
			name: "unary not requires bool operand",
			src: `
func main(): int {
	let x = !1
	return 0
}
`,
			wantErr: "unary ! requires bool",
		},
		{
			name: "binary operator rejects a nullable operand",
			src: `
func main(): int {
	let x: int? = 1
	let y = x + 1
	return 0
}
`,
			wantErr: "needs non-nullable operands",
		},
		{
			name: "string() rejects a string argument",
			src: `
func main(): int {
	let x = string("already a string")
	return 0
}
`,
			wantErr: "string() does not support string",
		},
		{
			name: "+ does not support bool",
			src: `
func main(): int {
	let x = true + false
	return 0
}
`,
			wantErr: "operator + does not support bool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, tt.src)
			if err == nil {
				t.Fatalf("Check() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check() = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCheck_Errors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "no main",
			src: `
func notMain(): int {
	return 0
}
`,
			wantErr: "no 'main' function found",
		},
		{
			name: "main takes a parameter",
			src: `
func main(x: int): int {
	return 0
}
`,
			wantErr: "must take no parameters",
		},
		{
			name: "main returns wrong type",
			src: `
func main(): string {
	return "0"
}
`,
			wantErr: "must return int",
		},
		{
			name: "reserved internal name",
			src: `
func cascade_main(): int {
	return 0
}
func main(): int {
	return 0
}
`,
			wantErr: "reserved name",
		},
		{
			name: "duplicate main",
			src: `
func main(): int {
	return 0
}
func main(): int {
	return 0
}
`,
			wantErr: "duplicate function \"main\"",
		},
		{
			name: "assign to const",
			src: `
func main(): int {
	const x: int = 1
	x = 2
	return 0
}
`,
			wantErr: "cannot assign to \"x\" (declared const)",
		},
		{
			name: "const without initializer",
			src: `
func main(): int {
	const x: int
	return 0
}
`,
			wantErr: "requires an initializer",
		},
		{
			name: "none into non-nullable type",
			src: `
func main(): int {
	let x: int = none
	return 0
}
`,
			wantErr: "cannot assign 'none' to non-nullable type int",
		},
		{
			name: "none without an explicit type cannot be inferred",
			src: `
func main(): int {
	let x = none
	return 0
}
`,
			wantErr: "cannot infer a type from 'none'",
		},
		{
			name: "undefined name on assignment",
			src: `
func main(): int {
	y = 1
	return 0
}
`,
			wantErr: "undefined name \"y\"",
		},
		{
			name: "redeclaration in the same scope",
			src: `
func main(): int {
	let x: int = 1
	let x: int = 2
	return 0
}
`,
			wantErr: "already declared in this scope",
		},
		{
			name: "type mismatch on assignment",
			src: `
func main(): int {
	let x: int = 1
	x = "oops"
	return 0
}
`,
			wantErr: "cannot assign string to int",
		},
		{
			name: "print rejects a nullable string (no narrowing yet)",
			src: `
func main(): int {
	let x: string? = "hi"
	print(x)
	return 0
}
`,
			wantErr: "print expects string, got string?",
		},
		{
			name: "return count mismatch",
			src: `
func main(): int {
	return
}
`,
			wantErr: "expected 1 return value(s), got 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, tt.src)
			if err == nil {
				t.Fatalf("Check() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check() = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCheck_ValidControlFlow(t *testing.T) {
	src := `
func main(): int {
	let x = 15
	if x < 10 {
		print("small")
	} elif x < 20 {
		print("medium")
	} else {
		print("large")
	}

	let i = 0
	while i < 10 {
		i += 1
		if i % 2 == 0 {
			continue
		}
		if i > 7 {
			break
		}
	}

	let day = 3
	switch day {
	case 1, 7:
		print("weekend")
	case 2, 3, 4, 5, 6:
		print("weekday")
	default:
		print("invalid")
	}

	switch {
	case day >= 5:
		print("late")
	default:
		print("early")
	}

	let maybeName: string? = "Cascade"
	if maybeName is none {
		return 1
	}
	print("name=" + maybeName)

	let maybeAge: int? = 30
	if maybeAge is not none {
		print("age=" + string(maybeAge))
	} else {
		print("no age")
	}

	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_ControlFlowErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "if condition must be bool",
			src: `
func main(): int {
	if 1 {
		return 0
	}
	return 0
}
`,
			wantErr: "if condition must be bool, got int",
		},
		{
			name: "while condition must be bool",
			src: `
func main(): int {
	while 1 {
		return 0
	}
	return 0
}
`,
			wantErr: "while condition must be bool, got int",
		},
		{
			name: "break outside a loop or switch",
			src: `
func main(): int {
	break
	return 0
}
`,
			wantErr: "break outside of a loop or switch",
		},
		{
			name: "continue outside a loop",
			src: `
func main(): int {
	continue
	return 0
}
`,
			wantErr: "continue outside of a loop",
		},
		{
			name: "continue inside a switch but outside any loop is still invalid",
			src: `
func main(): int {
	switch {
	case true:
		continue
	}
	return 0
}
`,
			wantErr: "continue outside of a loop",
		},
		{
			name: "tagged switch case type must match the tag",
			src: `
func main(): int {
	switch 1 {
	case "a":
		return 0
	}
	return 0
}
`,
			wantErr: "case value type string does not match switch tag type int",
		},
		{
			name: "untagged switch case must be bool",
			src: `
func main(): int {
	switch {
	case 1:
		return 0
	}
	return 0
}
`,
			wantErr: "case condition must be bool, got int",
		},
		{
			name: "is none requires a nullable type",
			src: `
func main(): int {
	let x = 1
	if x is none {
		return 0
	}
	return 0
}
`,
			wantErr: "'is none' requires a nullable type, got int",
		},
		{
			name: "variable declared inside an if body does not leak out",
			src: `
func main(): int {
	if true {
		let y = 1
	}
	let z = y
	return 0
}
`,
			wantErr: "undefined name \"y\"",
		},
		{
			name: "narrowing does not apply without an unconditional exit",
			src: `
func main(): int {
	let x: string? = "hi"
	if x is none {
		print("none")
	}
	print(x)
	return 0
}
`,
			wantErr: "print expects string, got string?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, tt.src)
			if err == nil {
				t.Fatalf("Check() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check() = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCheck_ValidLists(t *testing.T) {
	src := `
func main(): int {
	let xs: []int = [1, 2, 3]
	xs[0] = 100
	print(string(xs[0]))

	let ys = append(xs, 4)
	print(string(len(xs)) + string(len(ys)))

	let sum = 0
	for x in ys {
		sum += x
	}

	let rs = range(1, 6)
	for r in rs {
		sum += r
	}

	let empty: []int
	print(string(len(empty)))

	let names: []string = []
	print(string(len(names)))

	print(string(sum))
	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_ListErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "list literal element type mismatch",
			src: `
func main(): int {
	let xs: []int = [1, "two", 3]
	return 0
}
`,
			wantErr: "cannot assign string to int",
		},
		{
			name: "cannot infer type from an empty list literal",
			src: `
func main(): int {
	let xs = []
	return 0
}
`,
			wantErr: "cannot infer a type from an empty list literal",
		},
		{
			name: "inferred list element types must match",
			src: `
func main(): int {
	let xs = [1, 2.0]
	return 0
}
`,
			wantErr: "cannot assign float to int",
		},
		{
			name: "indexing a non-list is rejected",
			src: `
func main(): int {
	let x = 1
	let y = x[0]
	return 0
}
`,
			wantErr: "cannot index into int",
		},
		{
			name: "list index must be int",
			src: `
func main(): int {
	let xs = [1, 2, 3]
	let y = xs["0"]
	return 0
}
`,
			wantErr: "list index must be int, got string",
		},
		{
			name: "index-assignment element type mismatch",
			src: `
func main(): int {
	let xs = [1, 2, 3]
	xs[0] = "oops"
	return 0
}
`,
			wantErr: "cannot assign string to int",
		},
		{
			name: "for-in over a non-list is rejected",
			src: `
func main(): int {
	for x in 1 {
		print(string(x))
	}
	return 0
}
`,
			wantErr: "for-in requires a list, map, or channel, got int",
		},
		{
			name: "append requires a list first argument",
			src: `
func main(): int {
	let x = append(1, 2)
	return 0
}
`,
			wantErr: "append() expects a list as its first argument",
		},
		{
			name: "append value must match element type",
			src: `
func main(): int {
	let xs = [1, 2, 3]
	let ys = append(xs, "four")
	return 0
}
`,
			wantErr: "cannot assign string to int",
		},
		{
			name: "range requires int arguments",
			src: `
func main(): int {
	let rs = range(1, "6")
	return 0
}
`,
			wantErr: "range() requires int arguments, got string",
		},
		{
			name: "len does not support int",
			src: `
func main(): int {
	let n = len(1)
	return 0
}
`,
			wantErr: "len() does not support int",
		},
		{
			name: "cannot assign a list literal to a non-list type",
			src: `
func main(): int {
	let x: int = [1, 2, 3]
	return 0
}
`,
			wantErr: "cannot assign a list literal to non-list type int",
		},
		{
			name: "nullable value cannot narrow implicitly into a non-nullable target",
			src: `
func main(): int {
	let x: int? = 5
	let y: int = x
	return 0
}
`,
			wantErr: "cannot assign int? to non-nullable int",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, tt.src)
			if err == nil {
				t.Fatalf("Check() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check() = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCheck_ValidFunctions(t *testing.T) {
	src := `
func add(a: int, b: int): int {
	return a + b
}

func log(message: string) {
	print(message)
}

func divmod(a: int, b: int): (int, int) {
	return a / b, a % b
}

func greet(name: string?): string {
	if name is none {
		return "hello stranger"
	}
	return "hello " + name
}

func factorial(n: int): int {
	if n <= 1 {
		return 1
	}
	return n * factorial(n - 1)
}

func main(): int {
	let sum = add(3, 4)
	log("hi")

	let q, r = divmod(17, 5)
	let _, r2 = divmod(20, 6)

	print(greet(none))
	print(greet("Cascade"))
	print(string(sum) + string(q) + string(r) + string(r2))
	print(string(factorial(5)))
	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_ValidStructsAndPointers(t *testing.T) {
	src := `
struct Point {
	x: float
	y: float
}

struct Line {
	start: Point
	end: Point
}

func (p: Point) sum(): float {
	return p.x + p.y
}

func (p: *Point) scale(factor: float) {
	p.x = p.x * factor
	p.y = p.y * factor
}

func origin(): Point {
	return Point{x: 0.0, y: 0.0}
}

func main(): int {
	let pt: Point = Point{x: 3.0, y: 4.0}
	print(string(pt.sum()))

	pt.scale(2.0)
	print(string(pt.x))

	let p: *Point = &pt
	print(string(p.sum()))
	p.scale(0.5)

	let q: *Point = none
	if q is none {
		print("empty")
	}
	q = &pt
	if q is not none {
		print("set")
	}

	let ln: Line
	ln.start = origin()
	ln.end = Point{x: 1.0, y: 1.0}
	print(string(ln.end.x))

	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

// TestCheck_ValidAddressOfFieldAndIndex regression-tests isAddressable's
// widening (amivm's ADDR instruction gained an optional "point" operand —
// see isAddressable's doc): &p.x (a single-level field access on a bare
// variable) and &xs[0] (a single-level list-index access on a bare
// variable) are both now valid unary `&` operands, as is auto-address-of
// for a pointer-receiver method called through either shape.
func TestCheck_ValidAddressOfFieldAndIndex(t *testing.T) {
	src := `
struct Point {
	x: int
	y: int
}

func (p: *Point) scale(factor: int) {
	p.x = p.x * factor
	p.y = p.y * factor
}

func main(): int {
	let pt: Point = Point{x: 1, y: 2}
	let px: *int = &pt.x
	print(string(*px))

	let xs: []int = [10, 20, 30]
	let pe: *int = &xs[1]
	print(string(*pe))

	pt.scale(3)
	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_AddressOfMapIndexIsRejected(t *testing.T) {
	// Unlike a list index, a map index can never be addressed — amivm's
	// ADDR point-as-index form is slice/array-only (a map element is
	// never addressable in Go either) — see isAddressable's doc.
	src := `
func main(): int {
	let m: map<string, int> = {"a": 1}
	let p: *int = &m["a"]
	return 0
}
`
	err := check(t, src)
	if err == nil {
		t.Fatalf("Check() = nil, want error")
	}
	if !strings.Contains(err.Error(), "cannot take the address of this expression") {
		t.Fatalf("Check() = %q, want error containing %q", err.Error(), "cannot take the address of this expression")
	}
}

func TestCheck_StructPointerErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "duplicate struct name",
			src: `
struct Point { x: int }
struct Point { y: int }
func main(): int {
	return 0
}
`,
			wantErr: "duplicate struct \"Point\"",
		},
		{
			name: "duplicate field name",
			src: `
struct Point {
	x: int
	x: int
}
func main(): int {
	return 0
}
`,
			wantErr: "duplicate field \"x\"",
		},
		{
			name: "unknown field type",
			src: `
struct Foo { x: Bar }
func main(): int {
	return 0
}
`,
			wantErr: "unknown type \"Bar\"",
		},
		{
			name: "nullable non-pointer field type is rejected",
			src: `
struct Foo { x: int? }
func main(): int {
	return 0
}
`,
			wantErr: "nullable non-pointer field types are not supported yet",
		},
		{
			name: "struct literal missing a field",
			src: `
struct Point {
	x: int
	y: int
}
func main(): int {
	let p = Point{x: 1}
	return 0
}
`,
			wantErr: "missing field \"y\"",
		},
		{
			name: "struct literal unknown field",
			src: `
struct Point {
	x: int
	y: int
}
func main(): int {
	let p = Point{x: 1, y: 2, z: 3}
	return 0
}
`,
			wantErr: "struct \"Point\" has no field \"z\"",
		},
		{
			name: "field access on a non-struct",
			src: `
func main(): int {
	let x = 1
	let y = x.field
	return 0
}
`,
			wantErr: "cannot access field \"field\" on non-struct type int",
		},
		{
			name: "unknown field on a known struct",
			src: `
struct Point {
	x: int
	y: int
}
func main(): int {
	let p = Point{x: 1, y: 2}
	let z = p.z
	return 0
}
`,
			wantErr: "struct \"Point\" has no field \"z\"",
		},
		{
			name: "receiver type must name a known struct",
			src: `
func (p: Missing) foo(): int {
	return 0
}
func main(): int {
	return 0
}
`,
			wantErr: "unknown struct type \"Missing\" in receiver",
		},
		{
			name: "duplicate method name on the same struct",
			src: `
struct Point { x: int }
func (p: Point) foo(): int {
	return 0
}
func (p: Point) foo(): int {
	return 1
}
func main(): int {
	return 0
}
`,
			wantErr: "duplicate method \"foo\"",
		},
		{
			name: "call to an undeclared method",
			src: `
struct Point { x: int }
func main(): int {
	let p = Point{x: 1}
	p.missing()
	return 0
}
`,
			wantErr: "struct \"Point\" has no method \"missing\"",
		},
		{
			name: "pointer-receiver method requires an addressable receiver",
			src: `
struct Point {
	x: int
}
func (p: *Point) foo(): int {
	return p.x
}
func main(): int {
	let ok = Point{x: 1}.foo()
	return 0
}
`,
			wantErr: "cannot take the address of this expression",
		},
		{
			name: "dereferencing a non-pointer is rejected",
			src: `
func main(): int {
	let x = 1
	*x = 2
	return 0
}
`,
			wantErr: "cannot dereference non-pointer type int",
		},
		{
			name: "address-of a non-addressable expression is rejected",
			src: `
func f(): int {
	return 1
}
func main(): int {
	let p = &f()
	return 0
}
`,
			wantErr: "cannot take the address of this expression",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, tt.src)
			if err == nil {
				t.Fatalf("Check() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check() = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCheck_FunctionErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "call to undefined function",
			src: `
func main(): int {
	foo(1)
	return 0
}
`,
			wantErr: "unsupported call to \"foo\"",
		},
		{
			name: "wrong argument count",
			src: `
func add(a: int, b: int): int {
	return a + b
}
func main(): int {
	let x = add(1)
	return 0
}
`,
			wantErr: "add() expects 2 argument(s), got 1",
		},
		{
			name: "wrong argument type",
			src: `
func add(a: int, b: int): int {
	return a + b
}
func main(): int {
	let x = add(1, "two")
	return 0
}
`,
			wantErr: "cannot assign string to int",
		},
		{
			name: "void function used as a value",
			src: `
func log(message: string) {
	print(message)
}
func main(): int {
	let x = log("hi")
	return 0
}
`,
			wantErr: "log() returns no value",
		},
		{
			name: "multi-value function used as a single value",
			src: `
func divmod(a: int, b: int): (int, int) {
	return a / b, a % b
}
func main(): int {
	let x = divmod(1, 2)
	return 0
}
`,
			wantErr: "divmod() returns multiple values",
		},
		{
			name: "multi-value let name/result count mismatch",
			src: `
func divmod(a: int, b: int): (int, int) {
	return a / b, a % b
}
func main(): int {
	let q, r, s = divmod(1, 2)
	return 0
}
`,
			wantErr: "divmod() returns 2 value(s), but 3 name(s) given",
		},
		{
			name: "duplicate function name",
			src: `
func f(): int {
	return 0
}
func f(): int {
	return 1
}
func main(): int {
	return 0
}
`,
			wantErr: "duplicate function \"f\"",
		},
		{
			name: "duplicate parameter name",
			src: `
func f(a: int, a: int): int {
	return a
}
func main(): int {
	return 0
}
`,
			wantErr: "duplicate parameter name \"a\"",
		},
		{
			name: "redefining a builtin function name is rejected",
			src: `
func len(x: int): int {
	return x
}
func main(): int {
	return 0
}
`,
			wantErr: "\"len\" is a builtin function name and cannot be redefined",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, tt.src)
			if err == nil {
				t.Fatalf("Check() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check() = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCheck_ValidPointerEqualityComparison(t *testing.T) {
	// Regression test: pointer equality (comparing addresses) is valid Go
	// and was wrongly rejected before this fix — a pointer is always
	// Nullable (ast.Type's doc), and binaryResultType used to reject any
	// nullable operand outright with a "narrow first" message that isn't
	// even actionable for a pointer (narrowedVarInfo deliberately excludes
	// pointers from narrowing — see its doc). Found while adding an
	// analogous guard for function-value comparison (Step 9).
	src := `
struct Point {
	x: int
	y: int
}
func main(): int {
	let p: Point = Point{x: 1, y: 2}
	let a: *Point = &p
	let b: *Point = &p
	let same = a == b
	let different = a != b
	print(string(same) + string(different))
	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_ValidClosuresAndHigherOrderFunctions(t *testing.T) {
	src := `
func makeAdder(base: int): func(int): int {
	return func(n: int): int {
		return base + n
	}
}

func applyTwice(f: func(int): int, x: int): int {
	return f(f(x))
}

func main(): int {
	let add5 = makeAdder(5)
	print(string(add5(10)))

	let count = 0
	let inc = func(): int {
		count += 1
		return count
	}
	print(string(inc()))
	print(string(inc()))

	print(string(applyTwice(add5, 1)))

	let numbers: []int = [1, 2, 3, 4, 5, 6]
	let evens = filter(numbers, func(n: int): bool {
		return n % 2 == 0
	})
	let doubled = map(numbers, func(n: int): int {
		return n * 2
	})
	let total = reduce(numbers, 0, func(acc: int, n: int): int {
		return acc + n
	})
	print(string(len(evens)) + string(len(doubled)) + string(total))

	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_ClosureErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "closure literal cannot itself contain another closure literal",
			src: `
func main(): int {
	let f = func(): int {
		let g = func(): int {
			return 1
		}
		return g()
	}
	return 0
}
`,
			wantErr: "cannot itself contain another closure literal",
		},
		{
			name: "closure parameter type mismatch",
			src: `
func main(): int {
	let f: func(int): int = func(n: string): string {
		return n
	}
	return 0
}
`,
			wantErr: "cannot assign",
		},
		{
			name: "calling a non-function local variable",
			src: `
func main(): int {
	let x = 5
	x(1)
	return 0
}
`,
			wantErr: "is not callable",
		},
		{
			name: "break inside a closure cannot reach an enclosing loop",
			src: `
func main(): int {
	while true {
		let f = func(): int {
			break
			return 0
		}
	}
	return 0
}
`,
			wantErr: "break outside of a loop or switch",
		},
		{
			name: "closure calling itself by name is undefined (no named self-reference)",
			src: `
func main(): int {
	let f = func(n: int): int {
		return f(n)
	}
	return 0
}
`,
			wantErr: "\"f\" cannot be used as a value",
		},
		{
			name: "filter requires a bool-returning function",
			src: `
func main(): int {
	let xs = [1, 2, 3]
	let ys = filter(xs, func(n: int): int {
		return n
	})
	return 0
}
`,
			wantErr: "filter()'s function must return bool",
		},
		{
			name: "filter requires the predicate's parameter to match the element type",
			src: `
func main(): int {
	let xs = [1, 2, 3]
	let ys = filter(xs, func(n: string): bool {
		return true
	})
	return 0
}
`,
			wantErr: "filter()'s function must take exactly one int parameter",
		},
		{
			name: "map requires a function argument",
			src: `
func main(): int {
	let xs = [1, 2, 3]
	let ys = map(xs, 5)
	return 0
}
`,
			wantErr: "map() expects a function as its last argument",
		},
		{
			name: "reduce's function must return the accumulator type",
			src: `
func main(): int {
	let xs = [1, 2, 3]
	let total = reduce(xs, 0, func(acc: int, n: int): string {
		return "oops"
	})
	return 0
}
`,
			wantErr: "reduce()'s function must return int",
		},
		{
			name: "reduce's function must take (accumulator, element) parameters",
			src: `
func main(): int {
	let xs = [1, 2, 3]
	let total = reduce(xs, 0, func(n: int): int {
		return n
	})
	return 0
}
`,
			wantErr: "reduce()'s function must take (int, int) parameters",
		},
		{
			name: "function values cannot be compared with ==",
			src: `
func main(): int {
	let f: func(): int = func(): int { return 1 }
	let g: func(): int = func(): int { return 2 }
	let same = f == g
	return 0
}
`,
			wantErr: "does not support function values",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, tt.src)
			if err == nil {
				t.Fatalf("Check() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check() = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCheck_ValidMaps(t *testing.T) {
	src := `
func main(): int {
	let counts: map<string, int> = {"a": 1, "b": 2}
	counts["c"] = 3
	print(string(len(counts)))

	let a = counts["a"]
	if a is not none {
		print(string(a))
	}

	delete(counts, "b")

	let total = 0
	for k, v in counts {
		total += v
		print(k)
	}
	print(string(total))

	let empty: map<string, int>
	empty["x"] = 1
	print(string(len(empty)))

	let scores = {"alice": 90, "bob": 75}
	print(string(len(scores)))

	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_MapErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "map key type must be scalar",
			src: `
struct Point { x: int }
func main(): int {
	let m: map<Point, int>
	return 0
}
`,
			wantErr: "map key type must be a non-nullable scalar",
		},
		{
			name: "map key type cannot be nullable",
			src: `
func main(): int {
	let m: map<string?, int>
	return 0
}
`,
			wantErr: "map key type must be a non-nullable scalar",
		},
		{
			name: "map value type cannot itself be nullable",
			src: `
func main(): int {
	let m: map<string, int?>
	return 0
}
`,
			wantErr: "map value type cannot itself be nullable",
		},
		{
			name: "map literal key type mismatch",
			src: `
func main(): int {
	let m: map<string, int> = {"a": 1, 2: 3}
	return 0
}
`,
			wantErr: "cannot assign",
		},
		{
			name: "cannot infer type from an empty map literal",
			src: `
func main(): int {
	let m = {}
	return 0
}
`,
			wantErr: "cannot infer a type from an empty map literal",
		},
		{
			name: "map index key type mismatch",
			src: `
func main(): int {
	let m: map<string, int> = {"a": 1}
	let v = m[1]
	return 0
}
`,
			wantErr: "map key must be string",
		},
		{
			name: "map index-assignment key type mismatch",
			src: `
func main(): int {
	let m: map<string, int> = {"a": 1}
	m[1] = 5
	return 0
}
`,
			wantErr: "map key must be string",
		},
		{
			name: "map index-assignment value type mismatch",
			src: `
func main(): int {
	let m: map<string, int> = {"a": 1}
	m["b"] = "oops"
	return 0
}
`,
			wantErr: "cannot assign",
		},
		{
			name: "delete requires a map first argument",
			src: `
func main(): int {
	delete(5, "a")
	return 0
}
`,
			wantErr: "delete() expects a map as its first argument",
		},
		{
			name: "delete key type mismatch",
			src: `
func main(): int {
	let m: map<string, int> = {"a": 1}
	delete(m, 5)
	return 0
}
`,
			wantErr: "delete() key must be string",
		},
		{
			name: "single-variable for-in over a map is rejected",
			src: `
func main(): int {
	let m: map<string, int> = {"a": 1}
	for k in m {
		print(k)
	}
	return 0
}
`,
			wantErr: "for-in requires a list",
		},
		{
			name: "two-variable for-in over a list is rejected",
			src: `
func main(): int {
	let xs = [1, 2, 3]
	for k, v in xs {
		print(string(v))
	}
	return 0
}
`,
			wantErr: "for-in with two variables requires a map",
		},
		{
			name: "indexing into a non-map, non-list type",
			src: `
func main(): int {
	let x = 5
	let v = x["a"]
	return 0
}
`,
			wantErr: "cannot index into int",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, tt.src)
			if err == nil {
				t.Fatalf("Check() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check() = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCheck_ValidErrorHandling(t *testing.T) {
	src := `
func divide(a: int, b: int): (int, error?) {
	if b == 0 {
		return 0, error("division by zero")
	}
	return a / b, none
}

func loadAndDouble(a: int, b: int): (int, error?) {
	let result = divide(a, b)?
	return result * 2, none
}

func saveResult(n: int): (int, error?) {
	return n, none
}

func process(n: int): (int, error?) {
	saveResult(n)?
	return n * 10, none
}

func mustBePositive(n: int): error? {
	if n < 0 {
		return error("must be positive")
	}
	return none
}

func main(): int {
	let q, err = divide(10, 2)
	if err is not none {
		print("failed: " + err.message)
		return 1
	}
	print(string(q))

	let doubled, err2 = loadAndDouble(20, 4)
	if err2 is not none {
		return 1
	}
	print(string(doubled))

	let processed, err3 = process(7)
	if err3 is not none {
		return 1
	}
	print(string(processed))

	let e = mustBePositive(-5)
	if e is not none {
		print(e.message)
	}

	let validate = func(n: int): (int, error?) {
		if n < 0 {
			return 0, error("negative")
		}
		return n, none
	}
	let v, verr = validate(5)
	if verr is not none {
		print(verr.message)
	} else {
		print(string(v))
	}

	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_ValidNullableReturnType(t *testing.T) {
	// Regression test: nullable return types (any type, not just error?)
	// are now supported — Step 7/9 deferred this, Step 11 implements it.
	src := `
func maybeGreeting(happy: bool): string? {
	if happy {
		return "hello"
	}
	return none
}
func main(): int {
	let g = maybeGreeting(true)
	if g is not none {
		print(g)
	}
	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_ErrorHandlingErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "error() expects exactly 1 argument",
			src: `
func main(): int {
	let e = error()
	return 0
}
`,
			wantErr: "error() expects exactly 1 argument",
		},
		{
			name: "error() argument must be a string",
			src: `
func main(): int {
	let e = error(5)
	return 0
}
`,
			wantErr: "cannot assign",
		},
		{
			name: "field access on error works via the built-in message field",
			src: `
func f(): (int, error?) {
	return 0, error("boom")
}
func main(): int {
	let v, err = f()
	if err is not none {
		print(err.field_that_does_not_exist)
	}
	return 0
}
`,
			wantErr: "struct \"error\" has no field \"field_that_does_not_exist\"",
		},
		{
			name: "'?' requires a (T, error?)-shaped call",
			src: `
func f(): int {
	return 0
}
func main(): int {
	let y = f()?
	return 0
}
`,
			wantErr: "'?' requires a call returning (T, error?)",
		},
		{
			name: "'?' requires the enclosing function to also end in error?",
			src: `
func f(): (int, error?) {
	return 0, none
}
func main(): int {
	let y = f()?
	return 0
}
`,
			wantErr: "'?' can only be used inside a function or closure whose own return type ends in error?",
		},
		{
			name: "'?' rejects a nullable success type",
			src: `
func f(): (string?, error?) {
	return none, none
}
func g(): (string?, error?) {
	let y = f()?
	return y, none
}
func main(): int {
	return 0
}
`,
			wantErr: "'?' does not support a nullable success type yet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, tt.src)
			if err == nil {
				t.Fatalf("Check() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check() = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCheck_ValidPipeline(t *testing.T) {
	src := `
source numbers(output: chan<int>) {
	for n in range(1, 4) {
		send(output, n)
	}
}

stage double(input: chan<int>, output: chan<int>) {
	for n in input {
		send(output, n * 2)
	}
}

sink printAll(input: chan<int>) {
	for n in input {
		print(string(n))
	}
}

func main(): int {
	numbers |> double |> printAll
	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_PipelineErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "source must take exactly 1 parameter",
			src: `
source numbers(a: chan<int>, b: chan<int>) {
	send(a, 1)
}
func main(): int {
	return 0
}
`,
			wantErr: "source \"numbers\" must take exactly 1 parameter",
		},
		{
			name: "stage must take exactly 2 parameters",
			src: `
stage double(input: chan<int>) {
	for n in input {
		print(string(n))
	}
}
func main(): int {
	return 0
}
`,
			wantErr: "stage \"double\" must take exactly 2 parameters",
		},
		{
			name: "sink must take exactly 1 parameter",
			src: `
sink printAll(input: chan<int>, extra: chan<int>) {
	print("x")
}
func main(): int {
	return 0
}
`,
			wantErr: "sink \"printAll\" must take exactly 1 parameter",
		},
		{
			name: "source/stage/sink parameters must be a channel type",
			src: `
source numbers(output: int) {
	print("x")
}
func main(): int {
	return 0
}
`,
			wantErr: "source/stage/sink parameters must have a channel type",
		},
		{
			name: "channel types cannot be used outside a source/stage/sink parameter",
			src: `
func f(c: chan<int>): int {
	return 0
}
func main(): int {
	return 0
}
`,
			wantErr: "channel types (chan<T>) can only be used as a source/stage/sink parameter",
		},
		{
			name: "pipeline must begin with a source",
			src: `
stage double(input: chan<int>, output: chan<int>) {
	for n in input {
		send(output, n * 2)
	}
}
sink printAll(input: chan<int>) {
	for n in input {
		print(string(n))
	}
}
func main(): int {
	double |> printAll
	return 0
}
`,
			wantErr: "a pipeline must begin with a source",
		},
		{
			name: "pipeline used as a statement must end with a sink",
			src: `
source numbers(output: chan<int>) {
	send(output, 1)
}
stage double(input: chan<int>, output: chan<int>) {
	for n in input {
		send(output, n * 2)
	}
}
func main(): int {
	numbers |> double
	return 0
}
`,
			wantErr: "a pipeline used as a statement must end with a sink",
		},
		{
			name: "pipeline type mismatch between adjacent stages",
			src: `
source numbers(output: chan<int>) {
	send(output, 1)
}
sink printStrings(input: chan<string>) {
	for s in input {
		print(s)
	}
}
func main(): int {
	numbers |> printStrings
	return 0
}
`,
			wantErr: "pipeline type mismatch",
		},
		{
			name: "collect used as a bare statement is rejected (it only makes sense as a value)",
			src: `
source numbers(output: chan<int>) {
	send(output, 1)
}
func main(): int {
	numbers |> collect
	return 0
}
`,
			wantErr: "a pipeline used as a statement must end with a sink",
		},
		{
			name: "undefined source/stage/sink in a pipeline",
			src: `
source numbers(output: chan<int>) {
	send(output, 1)
}
func main(): int {
	numbers |> nope
	return 0
}
`,
			wantErr: "undefined source/stage/sink \"nope\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, tt.src)
			if err == nil {
				t.Fatalf("Check() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check() = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCheck_ValidCollect(t *testing.T) {
	src := `
source numbers(output: chan<int>) {
	send(output, 1)
}
stage double(input: chan<int>, output: chan<int>) {
	for n in input {
		send(output, n * 2)
	}
}
func main(): int {
	let results: []int? = numbers |> double |> collect
	if results is none {
		return 1
	}
	for r in results {
		print(string(r))
	}
	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_ValidAbort(t *testing.T) {
	src := `
source numbers(output: chan<int>) {
	send(output, -1)
}
stage validate(input: chan<int>, output: chan<int>) {
	for n in input {
		if n < 0 {
			abort("negative: " + string(n))
		}
		send(output, n)
	}
}
sink printAll(input: chan<int>) {
	for n in input {
		print(string(n))
	}
}
func main(): int {
	numbers |> validate |> printAll
	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_ValidMerge(t *testing.T) {
	src := `
source numsA(output: chan<int>) {
	send(output, 1)
}
source numsB(output: chan<int>) {
	send(output, 2)
}
func main(): int {
	let combined: chan<int> = merge(numsA, numsB)
	for v in combined {
		print(string(v))
	}
	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_CollectAbortMergeErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "collect used as a value must be a valid element-carrying chain",
			src: `
stage double(input: chan<int>, output: chan<int>) {
	for n in input {
		send(output, n * 2)
	}
}
func main(): int {
	let results: []int? = double |> collect
	return 0
}
`,
			wantErr: "a pipeline must begin with a source",
		},
		{
			name: "a sink used in value position is rejected in favor of collect",
			src: `
source numbers(output: chan<int>) {
	send(output, 1)
}
sink printAll(input: chan<int>) {
	for n in input {
		print(string(n))
	}
}
func main(): int {
	let x: []int? = numbers |> printAll
	return 0
}
`,
			wantErr: "a pipeline used as a value must end with 'collect'",
		},
		{
			name: "abort outside a stage body is rejected",
			src: `
func main(): int {
	abort("oops")
	return 0
}
`,
			wantErr: "abort() can only be called inside a source/stage/sink body",
		},
		{
			name: "abort requires exactly 1 argument",
			src: `
source numbers(output: chan<int>) {
	abort()
}
func main(): int {
	return 0
}
`,
			wantErr: "abort() expects exactly 1 argument",
		},
		{
			name: "merge requires exactly 2 arguments",
			src: `
source numsA(output: chan<int>) {
	send(output, 1)
}
func main(): int {
	let combined: chan<int> = merge(numsA)
	return 0
}
`,
			wantErr: "merge() expects exactly 2 arguments",
		},
		{
			name: "merge arguments must name declared sources",
			src: `
stage double(input: chan<int>, output: chan<int>) {
	for n in input {
		send(output, n * 2)
	}
}
func main(): int {
	let x: int = 1
	let combined: chan<int> = merge(x, double)
	return 0
}
`,
			wantErr: "merge() argument 1 must name a declared source",
		},
		{
			name: "merge requires both sources to carry the same type",
			src: `
source numsA(output: chan<int>) {
	send(output, 1)
}
source strs(output: chan<string>) {
	send(output, "x")
}
func main(): int {
	let combined: chan<int> = merge(numsA, strs)
	return 0
}
`,
			wantErr: "merge() requires both sources to carry the same type",
		},
		{
			name: "a channel-typed let without a merge initializer is rejected",
			src: `
source numsA(output: chan<int>) {
	send(output, 1)
}
func main(): int {
	let x: chan<int> = numsA
	return 0
}
`,
			wantErr: "a channel-typed 'let' is only supported with a merge(...) initializer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, tt.src)
			if err == nil {
				t.Fatalf("Check() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check() = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestCheck_NullableListNarrowingPreservesElemType is a regression test
// for a bug found while building Step 13's own worked example
// (cascade_spec.md §15's collect + narrowing combination):
// narrowedVarInfo used to reconstruct a narrowed variable's type as
// ast.Type{Name: orig.Type.Name, Nullable: false}, which silently
// dropped Elem for a nullable *list* (Name is empty for a list type) —
// producing a broken, shapeless type once narrowed, so using the
// narrowed list afterward (indexing, for-in, passing to another
// function) failed as if it had no type at all.
func TestCheck_NullableListNarrowingPreservesElemType(t *testing.T) {
	src := `
func main(): int {
	let xs: []int? = [1, 2, 3]
	if xs is none {
		return 1
	}
	for x in xs {
		print(string(x))
	}
	return 0
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_ValidTopLevelLet(t *testing.T) {
	src := `
let counter: int = 10
const greeting: string = "hi"
pub let shared: int? = none

func main(): int {
	print(string(counter))
	print(greeting)
	if shared is not none {
		print(string(shared))
	}
	counter = counter + 1
	return bump()
}

func bump(): int {
	return counter
}
`
	if err := check(t, src); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

func TestCheck_TopLevelLetErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "duplicate top-level let",
			src: `
let x: int = 1
let x: int = 2
func main(): int {
	return 0
}
`,
			wantErr: "already declared in this scope",
		},
		{
			name: "top-level const requires an initializer",
			src: `
const x: int
func main(): int {
	return 0
}
`,
			wantErr: "requires an initializer",
		},
		{
			name: "assigning to a top-level const is rejected",
			src: `
const x: int = 1
func main(): int {
	x = 2
	return 0
}
`,
			wantErr: "cannot assign to \"x\" (declared const)",
		},
		{
			name: "a top-level let collides with a function name",
			src: `
func helper(): int {
	return 0
}
let helper: int = 1
func main(): int {
	return 0
}
`,
			wantErr: "already declared as a function",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, tt.src)
			if err == nil {
				t.Fatalf("Check() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Check() = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}
