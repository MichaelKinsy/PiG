package tui

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// markedFencesSource is the fences rule of marked's block rules (rules.ts:60-110), the specification that the fence tokenizer is tested against; the tokenizer reads a fence without it.
const markedFencesSource = "^ {0,3}(`{3,}(?=[^`\\n]*(?:\\n|$))|~{3,})([^\\n]*)(?:\\n|$)(?:|([\\s\\S]*?)(?:\\n|$))(?: {0,3}\\1[~`]* *(?=\\n|$)|$)"

var markedBlockFences = newAnchoredMarkedRegexp(markedFencesSource, false)

// The fence tokenizer reads a fence without marked's pattern. This test requires its raw text, language and code to be those that the pattern's groups give, on sources made of fence lines of every shape: openers of both characters and several lengths with and without indentation and info strings, closers that are shorter, longer, indented, followed by text, spaces or the other fence character, and characters outside the BMP.
func TestMarkedFenceTokenizerEqualsItsPatternGroups(t *testing.T) {
	lexer := (&markedInlineState{}).blockLexer()
	lines := []string{"```", "````", "`````", "~~~", "~~~~", "~~~~~", " ```", "  ```", "   ```", "    ```", " ~~~", "   ~~~", "    ~~~", "``", "~~", "```go", "``` go ", "~~~ go", "~~~ a`b", "``` a`b", "```a```", "~~~~ x ~~~~", "```\\*", "```é", "~~~😀", "```go\t", "``` ~~~", "~~~ ```", "```~~~", "~~~```", "```   ", "~~~  ", "  ``` ", "```x", "~~~x", "```` ```", "~~~~ ~~~", "code", "  code", "    code", "x ```", "😀", "é😀", "a\r", "\r", "\u2028", "", " ", "```\r", "~~~\r", "`", "~"}
	check := func(src string) {
		runes := []rune(src)
		match := markedBlockFences.exec(runes)
		token, n := lexer.fence(runes)
		if (token != nil) != (match != nil) {
			t.Fatalf("source %q: token %v, pattern match %v", src, token != nil, match != nil)
		}
		if token == nil {
			return
		}
		raw := matchString(match)
		info, _ := markedGroup(match, 2)
		content, _ := markedGroup(match, 3)
		lang := ""
		if info != "" {
			lang = markedUnescapePunctuation(widthx.JSTrim(info))
		}
		if n != match.Length || token.raw != raw || token.lang != lang || token.text != markedIndentCodeCompensation(raw, content) {
			t.Fatalf("source %q: fence %d %q %q %q, pattern %d %q %q %q", src, n, token.raw, token.lang, token.text, match.Length, raw, lang, markedIndentCodeCompensation(raw, content))
		}
	}
	rng := rand.New(rand.NewPCG(11, 12))
	for _, a := range lines {
		for _, b := range lines {
			for _, sep := range []string{"\n", "\n\n", ""} {
				check(a + sep + b)
				check(a + "\n" + b + sep)
				for _, c := range lines[:20] {
					check(a + "\n" + b + "\n" + c)
				}
			}
		}
	}
	for range 200000 {
		var b strings.Builder
		for n := 1 + rng.IntN(7); n > 0; n-- {
			b.WriteString(lines[rng.IntN(len(lines))])
			if n > 1 || rng.IntN(2) == 0 {
				b.WriteString("\n")
			}
		}
		check(b.String())
	}
	markedBlockSources(check)
}
