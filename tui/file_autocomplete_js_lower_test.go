package tui

import "testing"

// autocomplete.ts:739-751 scoreEntry lowercases with toLowerCase: İ (U+0130) becomes i + U+0307, so the query "i\u0307x"
// is a prefix of "İx.go". Go's strings.ToLower gives a plain i and scores no match.
func TestScoreEntryLowercasesAsJavaScriptDoes(t *testing.T) {
	if got := scoreEntry("src/İx.go", "i\u0307x", false); got != 80 {
		t.Fatalf("scoreEntry(İx.go, i+U+0307+x) = %d, want 80 (prefix match)", got)
	}
	if got := scoreEntry("İdir/a.go", "i\u0307dir", false); got != 30 {
		t.Fatalf("scoreEntry(İdir/a.go, i+U+0307+dir) = %d, want 30 (path match)", got)
	}
}
