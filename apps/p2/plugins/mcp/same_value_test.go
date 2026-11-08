package mcp

import "testing"

// sameValue says whether a config value is the one already placed. Objects are not comparable: they are taken as changed, and
// asking must not panic.
func TestSameValueTakesObjectsAsChangedAndNeverPanics(t *testing.T) {
	object := map[string]any{"a": 1.0}
	array := []any{1.0}
	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"the same array", array, array, true},
		{"another array", array, []any{1.0}, false},
		{"the same object", object, object, false},
		{"an object and an array", object, array, false},
		{"equal strings", "x", "x", true},
		{"nil and nil", nil, nil, true},
		{"nil and an object", nil, object, false},
	}
	for _, each := range cases {
		if got := sameValue(each.a, each.b); got != each.want {
			t.Errorf("%s: sameValue = %v, want %v", each.name, got, each.want)
		}
	}
}
