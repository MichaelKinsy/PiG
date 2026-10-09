package delta

import "testing"

func TestLcsMatchesBoundsCells(t *testing.T) {
	a := make([]any, 10)
	b := make([]any, 10)
	if _, ok := lcsMatches(a, b, func(l, r JsonValue) bool { return true }, 99); ok {
		t.Fatal("expected bound exceeded")
	}
	if _, ok := lcsMatches(a, b, func(l, r JsonValue) bool { return true }, 100); !ok {
		t.Fatal("expected within bound")
	}
}
