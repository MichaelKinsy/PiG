package host

import "testing"

// Pi's `result === draft.v` (cell.ts) compares objects by identity and never throws; Go's == panics on maps and slices.
func TestSameDraftComparesObjectsByIdentity(t *testing.T) {
	object := map[string]any{"a": 1.0}
	array := []any{1.0, 2.0}
	for _, tc := range []struct {
		name          string
		result, draft any
		want          bool
	}{
		{"the same object", object, object, true},
		{"an equal but different object", map[string]any{"a": 1.0}, object, false},
		{"the same array", array, array, true},
		{"a shorter view of the array", array[:1], array, false},
		{"the same primitive", 1.0, 1.0, true},
		{"different types", "1", 1.0, false},
		{"undefined and an object", nil, object, false},
	} {
		if got := sameDraft(tc.result, tc.draft); got != tc.want {
			t.Errorf("%s: sameDraft = %v, want %v", tc.name, got, tc.want)
		}
	}
}
