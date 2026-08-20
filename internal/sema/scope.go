package sema

import "github.com/amisonnet8/cascade/internal/ast"

// varInfo is what sema tracks about one declared name: its type (used to
// check every later read/assignment against it) and whether it was
// declared `const` (cascade_spec.md §4.2 — `let` is reassignable, `const`
// is not).
type varInfo struct {
	Type  ast.Type
	Const bool
}

// scope resolves names to varInfo within one nesting level. Every block
// (cascade_spec.md §10) gets its own scope once Step 5 introduces nested
// blocks (if/while/for); Steps 1-2 only ever have the single top-level
// scope a function body starts with, but declareLocal/lookup are already
// scope-chain-aware so that step needs no rework here.
type scope struct {
	parent *scope
	vars   map[string]varInfo
}

func newScope(parent *scope) *scope {
	return &scope{parent: parent, vars: map[string]varInfo{}}
}

// declareLocal declares name in s itself (not an outer scope). It returns
// false without modifying s if name is already declared in this exact
// scope — cascade_spec.md §10 forbids that, but shadowing a name from an
// outer scope is fine (a fresh inner scope's map simply doesn't have it
// yet).
func (s *scope) declareLocal(name string, info varInfo) bool {
	if _, exists := s.vars[name]; exists {
		return false
	}
	s.vars[name] = info
	return true
}

// lookup resolves name by walking outward from s through parent scopes.
func (s *scope) lookup(name string) (varInfo, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if info, ok := cur.vars[name]; ok {
			return info, true
		}
	}
	return varInfo{}, false
}
