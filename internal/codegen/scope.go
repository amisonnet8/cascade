package codegen

import "github.com/amisonnet8/cascade/internal/ast"

// varRef is how a declared Cascade variable is represented in generated
// AMIVM-IR. ValOp always holds its current value; SetOp additionally holds
// a companion bool — "does this variable currently hold a value, or is it
// none?" — when Type is nullable (cascade_spec.md §2.3). SetOp is empty
// for a non-nullable Type: it can never be none, so no flag is needed at
// all (see CLAUDE.md's "オープンな設計課題" §1 and
// seed_implementation_notes.md §3's comma-ok pattern, which the same idea
// is modeled on).
//
// A plain read of ValOp is always correct on its own, with no need to
// check SetOp first: Go's zero value for int/float64/string/bool happens
// to equal Cascade's own base value (cascade_spec.md §2.1), the same
// coincidence Seed's isset scheme relies on.
type varRef struct {
	Type  ast.Type
	ValOp string
	SetOp string
}

// scope resolves Cascade variable names to their varRef within one
// function body. Every block (cascade_spec.md §10) gets its own scope
// once Step 5 introduces nested blocks (if/while/for) — pushing a scope
// there keeps shadowing safe even though every declared Go variable still
// ends up hoisted into one flat function namespace (see codegen.go's
// package doc and seed_implementation_notes.md §1-2). Steps 1-2 only ever
// use the single root scope a function body starts with.
type scope struct {
	parent *scope
	vars   map[string]varRef
}

func newScope(parent *scope) *scope {
	return &scope{parent: parent, vars: map[string]varRef{}}
}

func (s *scope) declare(name string, ref varRef) {
	s.vars[name] = ref
}

func (s *scope) lookup(name string) (varRef, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if ref, ok := cur.vars[name]; ok {
			return ref, true
		}
	}
	return varRef{}, false
}
