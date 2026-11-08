package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// WarmHighlight leaves the state a longer text resumes from, and HighlightCode of the longer text returns the lines a run over all of it
// returns: for the files a write card streams, whatever the steps and whatever follows the step in the text.
func TestWarmHighlightContinuesToTheLinesOfAWholeRun(t *testing.T) {
	orig := ActiveTheme().Name
	defer SetTheme(orig)
	SetTheme("dark")

	var source strings.Builder
	for n := 0; source.Len() < 40<<10; n++ {
		fmt.Fprintf(&source, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }\n// note %d /* nested */\n", n, n%97, n, n)
	}
	cases := []struct{ lang, code string }{
		{"typescript", source.String()},
		{"typescript", strings.TrimSuffix(source.String(), "\n") + "\n/* an unterminated comment\n"},
		{"python", strings.ReplaceAll(source.String(), "export function", "def")},
		{"go", strings.ReplaceAll(source.String(), "export function", "func")},
	}
	for _, c := range cases {
		hlMu.Lock()
		resetHighlightCacheLocked()
		hlMu.Unlock()
		for _, end := range []int{4 << 10, 9 << 10, 17 << 10, 30 << 10} {
			if !WarmHighlight(c.code[:end], c.lang) {
				t.Fatalf("%s: no state to resume from after %d bytes", c.lang, end)
			}
		}
		want := highlightCodeUncached(highlightRegistry(), c.code, c.lang, ActiveTheme())
		if got := HighlightCode(c.code, c.lang); !slices.Equal(got, want) {
			t.Errorf("%s: the resumed lines differ from a whole run (%d lines, want %d)", c.lang, len(got), len(want))
		}
	}
}

// A language highlight.js lacks leaves no state, which tells the caller to stop warming.
func TestWarmHighlightReportsALanguageThatCannotResume(t *testing.T) {
	if WarmHighlight(strings.Repeat("text\n", 100), "no-such-language") {
		t.Fatal("WarmHighlight reported a resume state for a language highlight.js lacks")
	}
	if WarmHighlight(strings.Repeat("text\n", 100), "") {
		t.Fatal("WarmHighlight reported a resume state for no language")
	}
}
