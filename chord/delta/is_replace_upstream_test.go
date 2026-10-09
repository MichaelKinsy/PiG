package delta

import "testing"

// Pi chord/src/delta.ts isReplace: only the "r" verb replaces the complete value; IsBase is true exactly when a batch begins with one.
func TestIsReplaceUpstream(t *testing.T) {
	for _, tc := range []struct {
		op   Op
		want bool
	}{
		{Op{"r", 1}, true},
		{Op{"s", Path{"a"}, 1}, false},
		{Op{"d", Path{"a"}}, false},
		{Op{"m", Path{}, []any{1, 0}}, false},
		{Op{}, false},
		{Op{1}, false},
	} {
		if got := IsReplace(tc.op); got != tc.want {
			t.Errorf("IsReplace(%v) = %v, want %v", tc.op, got, tc.want)
		}
	}
	if !IsBase([]Op{{"r", 1}, {"s", Path{"a"}, 2}}) || IsBase([]Op{{"s", Path{"a"}, 2}, {"r", 1}}) || IsBase(nil) {
		t.Fatal("IsBase must follow the first operation only")
	}
}
