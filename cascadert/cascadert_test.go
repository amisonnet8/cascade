package cascadert

import (
	"sort"
	"testing"
)

func TestKeys(t *testing.T) {
	m := map[string]int{"a": 1, "b": 2, "c": 3}
	got := Keys(m)
	sort.Strings(got)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("Keys() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Keys() = %v, want %v", got, want)
		}
	}
}

func TestKeys_Empty(t *testing.T) {
	m := map[string]int{}
	got := Keys(m)
	if len(got) != 0 {
		t.Fatalf("Keys() = %v, want empty", got)
	}
}

// TestKeys_NamedMapType regression-tests that Keys infers K/V correctly
// against a *named* map type (the shape amivm's MPTYPE always generates,
// e.g. `type StrIntMap map[string]int` — never a bare `map[string]int`
// parameter), not just a literal map type.
func TestKeys_NamedMapType(t *testing.T) {
	type StrIntMap map[string]int
	m := StrIntMap{"x": 1}
	got := Keys(m)
	if len(got) != 1 || got[0] != "x" {
		t.Fatalf("Keys() = %v, want [x]", got)
	}
}
