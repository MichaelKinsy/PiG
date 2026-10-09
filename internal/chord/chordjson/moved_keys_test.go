package chordjson

import (
	"slices"
	"testing"
)

// A JavaScript object keeps a key in place until it is deleted; a key set again follows the keys present, so
// MovedKeys finds the fewest keys whose delete-and-set produces the target order.
func TestMovedKeysFindsTheFewestDeleteAndReAdds(t *testing.T) {
	for _, tc := range []struct {
		current, target, want []string
	}{
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}, nil},
		{[]string{"a", "b", "c"}, []string{"b", "c", "a"}, []string{"a"}},
		{[]string{"a", "b", "c"}, []string{"a", "c", "b"}, []string{"b"}},
		{[]string{"a", "b", "c"}, []string{"c", "b", "a"}, []string{"b", "a"}},
		{[]string{"a", "b"}, []string{"b", "a"}, []string{"a"}},
		{[]string{"1", "a", "b"}, []string{"1", "b", "a"}, []string{"a"}},
	} {
		if got := MovedKeys(tc.current, tc.target); !slices.Equal(got, tc.want) {
			t.Errorf("MovedKeys(%q, %q) = %q, want %q", tc.current, tc.target, got, tc.want)
		}
	}
}
