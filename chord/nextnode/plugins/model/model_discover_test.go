package model

import "testing"

// Not upstream's: matchesGlob's tokens (`**/` optional folders, `**`, `*`, `?`, literals) match as the RegExp they build does.
func TestMatchesGlobStarsStayInASegmentDoubleStarsCrossThemQuestionMarksMatchOneUnit(t *testing.T) {
	for _, row := range []struct {
		path, pattern string
		want          bool
	}{
		{"a/b", "a/*", true},
		{"a/b/c", "a/*", false},
		{"a/b/c", "a/**", true},
		{"a", "a/**", false},
		{"a/p1", "a/**/p?", true},
		{"a/x/y/p1", "a/**/p?", true},
		{"a/p12", "a/**/p?", false},
		{"a.b", "a.b", true},
		{"axb", "a.b", false},
		{"a/😀", "a/?", false},
		{"a/\n/b", "a/**/b", false},
		{".", ".", true},
	} {
		if got := matchesGlob(row.path, row.pattern); got != row.want {
			t.Errorf("matchesGlob(%q, %q) = %v, want %v", row.path, row.pattern, got, row.want)
		}
	}
}
