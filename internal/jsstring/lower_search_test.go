package jsstring

import (
	"strings"
	"testing"
)

// LowerContains and LowerEquals are the searches of ToLower's result, whatever the strings hold.
func TestLowerSearchesAgreeWithToLower(t *testing.T) {
	values := []string{"", "a", "Model", "MODEL", "model", "gpt-4O-mini", "Claude Opus 4.5", "fixture/model-0001", "ΣΑΣ", "İstanbul", "straße", "ǅ", "日本語", "a\u0000B"}
	for _, s := range values {
		for _, needle := range values {
			lower := ToLower(needle)
			if got, want := LowerContains(s, lower), strings.Contains(ToLower(s), lower); got != want {
				t.Errorf("LowerContains(%q, %q) = %v, want %v", s, lower, got, want)
			}
			if got, want := LowerEquals(s, lower), ToLower(s) == lower; got != want {
				t.Errorf("LowerEquals(%q, %q) = %v, want %v", s, lower, got, want)
			}
		}
	}
	for _, s := range values {
		for end := range len(s) + 1 {
			for start := range end + 1 {
				lower := ToLower(s[start:end])
				if !LowerContains(s, lower) && strings.Contains(ToLower(s), lower) {
					t.Errorf("LowerContains(%q, %q) = false", s, lower)
				}
			}
		}
	}
}
