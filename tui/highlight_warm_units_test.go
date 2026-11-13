package tui

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

// warmedSource is TypeScript with every kind of text a cut can fall inside: multi-byte characters, emoji, a lone surrogate (WTF-8, as
// the streaming argument parser keeps one), escapes in strings and comments, and a language construct that spans lines.
func warmedSource(lines int) string {
	var b strings.Builder
	for n := range lines {
		switch n % 6 {
		case 0:
			fmt.Fprintf(&b, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, 0); }\n", n, n)
		case 1:
			fmt.Fprintf(&b, "const label%d = \"héllo wörld 日本語 😀 \\u00e9 \\n \\\" \x1b[31m\"; // note %d é\n", n, n)
		case 2:
			fmt.Fprintf(&b, "/* a comment over\n   two lines with \xed\xa0\x80 and 😀 */ const lone%d = `tpl ${n} \xed\xb8\x80`;\n", n)
		case 3:
			fmt.Fprintf(&b, "const fn%d = (\n  a: number,\n  b: string,\n) => a + b.length; // 日本\n", n)
		case 4:
			fmt.Fprintf(&b, "class C%d<T> extends Base { private x = %d; method(): void { this.x++; } }\n", n, n)
		default:
			fmt.Fprintf(&b, "const re%d = /ab+c\\/[é]/gi; const o%d = { key: 'v', other: %d };\n", n, n, n)
		}
	}
	return b.String()
}

// Warm-ups of the same text in two languages keep their states apart, and the text that follows continues each of them.
func TestWarmHighlightKeepsTheStatesOfTwoLanguagesApart(t *testing.T) {
	orig := ActiveTheme().Name
	defer SetTheme(orig)
	SetTheme("dark")

	code := warmedSource(90)
	hlMu.Lock()
	resetHighlightCacheLocked()
	hlMu.Unlock()
	langs := []string{"typescript", "javascript", "typescript"}
	for end := 700; end < len(code); end += 733 {
		WarmHighlight(code[:end], langs[(end/733)%len(langs)])
	}
	for _, lang := range []string{"typescript", "javascript"} {
		want := highlightCodeUncached(highlightRegistry(), code, lang, ActiveTheme())
		if got := HighlightCode(code, lang); !slices.Equal(got, want) {
			t.Errorf("%s: the lines differ from a whole run (%d lines, want %d)", lang, len(got), len(want))
		}
	}
}

// A warm-up that runs while a frame highlights the same text hands the units from one run to the other without sharing them: the
// lines are those of a whole run under the race detector.
func TestWarmHighlightAndHighlightCodeOfOneTextDoNotShareUnits(t *testing.T) {
	orig := ActiveTheme().Name
	defer SetTheme(orig)
	SetTheme("dark")

	code := warmedSource(150)
	hlMu.Lock()
	resetHighlightCacheLocked()
	hlMu.Unlock()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for end := 300; end < len(code); end += 301 {
			WarmHighlight(code[:end], "typescript")
		}
	}()
	go func() {
		defer wg.Done()
		for end := 500; end < len(code); end += 997 {
			HighlightCode(code[:end], "typescript")
		}
	}()
	wg.Wait()
	want := highlightCodeUncached(highlightRegistry(), code, "typescript", ActiveTheme())
	if got := HighlightCode(code, "typescript"); !slices.Equal(got, want) {
		t.Errorf("the lines differ from a whole run (%d lines, want %d)", len(got), len(want))
	}
}
