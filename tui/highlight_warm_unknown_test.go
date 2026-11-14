package tui

import (
	"strings"
	"testing"
)

// A language highlight.js lacks leaves no state, which tells the caller to stop warming.
func TestWarmHighlightReportsALanguageThatCannotResume(t *testing.T) {
	if WarmHighlight(strings.Repeat("text\n", 100), "no-such-language") {
		t.Fatal("WarmHighlight reported a resume state for a language highlight.js lacks")
	}
	if WarmHighlight(strings.Repeat("text\n", 100), "") {
		t.Fatal("WarmHighlight reported a resume state for no language")
	}
}
