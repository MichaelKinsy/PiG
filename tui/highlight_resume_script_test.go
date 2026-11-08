//go:build !pig_strip_syntax_highlight

package tui

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// streamedScriptPool is the pieces of JavaScript and TypeScript the streamed text below is joined from: the lines of a write
// call's file, and the constructs whose highlighting depends on text that arrives later (parameters that span lines,
// comments and object keys, tags and type arguments).
var streamedScriptPool = []string{
	"export function step1(input: number[]): number { return input.reduce((a, b) => a + b * 3, 1); }\n",
	"const g = (a, b) => a + b;\n", "const h = (a,\n  b,\n  c) =>\n  a;\n", "foo(a,\n  b);\n", "x = (\n", ") => 1;\n",
	"const o = {\n  key: 1,\n  // note\n  other: 2,\n};\n", "// a comment\n", "/* a block */\n", "/** doc **/\n", "/*\n * multi\n */\n",
	"class A {\n  m(x) {\n    return x;\n  }\n}\n", "const t: Array<string> = [];\n", "let z = a<b>c;\n", "const e = <div>text</div>;\n",
	"const s = `(${a}) =>`;\n", "\n", "\n\n\n", "if (a) {\n} else {\n}\n", "const n = 0x1f;\n",
}

// TestStreamedScriptHighlightEqualsAWholeRun: JavaScript and TypeScript, which resume from points the text checks keep, are
// streamed in steps of a few characters and return at every step the lines a whole run returns, and most steps resume.
func TestStreamedScriptHighlightEqualsAWholeRun(t *testing.T) {
	theme := ActiveTheme()
	steps, resumed := 0, 0
	for seed := range 6 {
		random := rand.New(rand.NewPCG(uint64(seed), 99))
		var text strings.Builder
		for range 12 + random.IntN(20) {
			if random.IntN(3) == 0 {
				text.WriteString(streamedScriptPool[random.IntN(len(streamedScriptPool))])
			} else {
				fmt.Fprintf(&text, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }\n", random.IntN(100), random.IntN(97), random.IntN(100))
			}
		}
		code := text.String()
		for _, lang := range []string{"typescript", "javascript"} {
			hlMu.Lock()
			resetHighlightCacheLocked()
			hlTheme = theme
			hlMu.Unlock()
			for end := 0; end < len(code); {
				end = min(len(code), end+1+random.IntN(24))
				for end < len(code) && !utf8.RuneStart(code[end]) {
					end++
				}
				if base, _ := hlFindResume(code[:end], lang, theme); base.prefix != "" {
					resumed++
				}
				steps++
				got := highlightCodeLines(code[:end], lang)
				want := highlightCodeUncached(highlightRegistry(), code[:end], lang, theme)
				if !slices.Equal(got, want) {
					t.Fatalf("%s seed %d at %d of %d bytes:\n got %q\nwant %q", lang, seed, end, len(code), got, want)
				}
			}
		}
	}
	t.Logf("%d steps, %d resumed", steps, resumed)
	if resumed < steps/2 {
		t.Fatalf("only %d of %d steps resumed", resumed, steps)
	}
}
