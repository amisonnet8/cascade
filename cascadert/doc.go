// Package cascadert is the Cascade runtime: Go implementations of builtins
// that amivm's generated code calls via CALL (e.g. string<->number parsing).
// It cannot live under internal/ because generated code (a separate module)
// imports it; see CLAUDE.md "独自のGoランタイムを呼ぶ".
package cascadert
