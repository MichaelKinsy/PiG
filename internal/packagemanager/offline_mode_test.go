package packagemanager

import "testing"

// package-manager.ts isOfflineModeEnabled: PI_OFFLINE is "1", or "true"/"yes" after String.prototype.toLowerCase. Whitespace padding and a long s (U+017F,
// which Unicode simple folding maps to s but toLowerCase leaves alone) do not match.
func TestIsOfflineModeEnabledMatchesPiExactly(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"1", true}, {"true", true}, {"TRUE", true}, {"Yes", true}, {"yes", true}, {" 1", false}, {"\ttrue", false}, {"yes\n", false},
		{"0", false}, {"no", false}, {"ye\u017f", false}, {"TRUE\u017f", false},
	} {
		t.Setenv("PIG_OFFLINE", "")
		t.Setenv("PI_OFFLINE", tc.value)
		if got := IsOfflineModeEnabled(); got != tc.want {
			t.Errorf("PI_OFFLINE=%q: IsOfflineModeEnabled() = %v, want %v", tc.value, got, tc.want)
		}
	}
}
