package cascadert

// Keys returns m's keys as a slice, in Go's own randomized map-iteration
// order (cascade_spec.md §7's "順序は不定" for `for k, v in m`). AMIVM-IR
// has no instruction to enumerate a map's entries (MPTYPE/MPMAKE/MSET/
// MGET only declare, construct, and do single-key access — see CLAUDE.md
// "確定した設計判断"), so the two-variable for-in form calls this once
// to get the keys, then MGETs each value by key in an ordinary counted
// loop.
func Keys[K comparable, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
