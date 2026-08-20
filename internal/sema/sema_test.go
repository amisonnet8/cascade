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
			wantErr: "for-in requires a list, got int",
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
		{
			name: "nullable return types are not supported yet",
			src: `
func f(): int? {
	return 1
}
func main(): int {
	return 0
}
`,
			wantErr: "nullable return types are not supported yet",
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
