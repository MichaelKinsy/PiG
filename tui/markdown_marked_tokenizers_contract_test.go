package tui

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// markedBlockSources are sources of up to six lines drawn from the lines that start, continue and end the blocks the tokenizers of marked's block lexer read. The hr lines "    ---", "     ***", "-_-", "- - - x" and "***---" are the indentation and spacing boundaries of the hr rule that the other lines rarely reach.
func markedBlockSources(each func(src string)) {
	lines := []string{"", " ", "  ", "\t", "a", "ab c", "# h", "#", "####### h", "#h", " ## h ##", "---", "***", "___", "- - -", "    ---", "     ***", "-_-", "- - - x", "***---", "-", "=", "===", " ===  ", "--", "> q", ">", " > q", "- item", "1. item", "1) i", "* x", "+ y",
		"```", "```go", "``` a`b", "~~~", "~~~ x", "````", " ```", "  ~~~", "    code", "\tcode", "   x", "<div>", "</div>", "<div", "<!--", "<!-->", "<!--->", "-->", "<pre>", "</pre>", "<script>", "</script>", "<?x", "?>", "<![CDATA[", "]]>", "<!A", "<a href=\"x\">", "<a b='c' d>", "<br/>", "<x-y>", "</x>",
		"[a]: u", "[a]:", "[a]: <u v>", "[a]: u \"t\"", "[a]: u 't", "\"t\"", "'t'", "(t)", "[ ]: u", "[a\\]]: u", "[a]: u t", "[a]:\n", "| a |", "|-|", "a|b", "--|--", ":-:|-:", "| - |", "|", "$$", "\\[", "x\\"}
	rng := rand.New(rand.NewPCG(5, 6))
	each("")
	for _, l := range lines {
		each(l)
		each(l + "\n")
		each(l + "\n\n")
		for _, m := range lines {
			each(l + "\n" + m)
			each(l + "\n" + m + "\n")
		}
	}
	for range 300000 {
		var b strings.Builder
		for n := 1 + rng.IntN(6); n > 0; n-- {
			b.WriteString(lines[rng.IntN(len(lines))])
			if n > 1 || rng.IntN(2) == 0 {
				b.WriteString("\n")
			}
		}
		each(b.String())
	}
}

// The block tokenizers filter a source with a cheap test before they run marked's pattern. These tests require each to find a token exactly where the pattern matches, and as much of the source as the pattern does.
func TestMarkedBlockTokenizersEqualTheirPatterns(t *testing.T) {
	lexer := (&markedInlineState{}).blockLexer()
	type tokenizer struct {
		name    string
		run     func(src []rune) (*markedBlock, int)
		pattern *markedRegexp
		// exact reports that a match always yields a token of the match's length.
		exact bool
	}
	tokenizers := []tokenizer{
		{"code", markedCode, markedBlockCode, false},
		{"fences", lexer.fence, markedBlockFences, true},
		{"heading", lexer.heading, markedBlockHeading, false},
		{"hr", markedHr, markedBlockHr, false},
		{"html", markedHTMLBlock, markedBlockHTML, false},
		{"def", markedDef, markedBlockDef, false},
		{"lheading", lexer.lheading, markedBlockLheading, false},
	}
	check := func(src string) {
		runes := []rune(src)
		for _, tk := range tokenizers {
			// The source has its tabs expanded before the lexer reads it, so the code rule's tab alternative never applies.
			if tk.name == "code" && strings.Contains(src, "\t") {
				continue
			}
			match := tk.pattern.exec(runes)
			token, n := tk.run(runes)
			switch {
			case tk.name == "def" || tk.name == "html":
				// These tokenizers add conditions of their own that the pattern does not state.
				if token != nil && match == nil {
					t.Fatalf("%s: token for %q, no pattern match", tk.name, src)
				}
			case (token != nil) != (match != nil):
				t.Fatalf("%s of %q: token %v, pattern match %v", tk.name, src, token != nil, match != nil)
			}
			if token != nil && match != nil && tk.exact && n != match.Length {
				t.Fatalf("%s of %q: %d characters, the pattern matches %d", tk.name, src, n, match.Length)
			}
			if token != nil && token.raw == "" && n != 0 {
				t.Fatalf("%s of %q: empty raw, %d characters", tk.name, src, n)
			}
		}
	}
	markedRuleSources(t, check)
	markedBlockSources(check)
}

func dumpMarkedBlocks(b *strings.Builder, tokens []*markedBlock) {
	for _, token := range tokens {
		fmt.Fprintf(b, "(%s %q %q %q %d %v %d %v %v %v %v %d %q", token.kind, token.raw, token.text, token.lang, token.depth, token.pending, token.offset, token.ordered, token.loose, token.task, token.checked, token.start, token.tag)
		if token.entry != nil {
			fmt.Fprintf(b, " entry=%q", token.entry.src)
		}
		for _, cell := range token.header {
			fmt.Fprintf(b, " h=%q", cell.src)
		}
		for _, row := range token.rows {
			for _, cell := range row {
				fmt.Fprintf(b, " c=%q", cell.src)
			}
			b.WriteString(" /")
		}
		dumpMarkedBlocks(b, token.tokens)
		dumpMarkedBlocks(b, token.items)
		b.WriteString(")")
	}
}

// lexMarkedBlocks is the tokens and the inline queue of src lexed with the lexer of state.
func lexMarkedBlocks(state *markedInlineState, src string) string {
	lexer := state.blockLexer()
	var b strings.Builder
	dumpMarkedBlocks(&b, lexer.lexRunes(state.blockRunes(src), src))
	for _, entry := range lexer.queue {
		fmt.Fprintf(&b, " q=%q", entry.src)
	}
	return b.String()
}

// A lexer and its buffers (token slabs, inline queue, code point buffer) are kept for the next source: a source lexes to the same tokens whatever was lexed before it, in documents of one token and of hundreds.
func TestMarkedBlockLexerReuseEqualsFresh(t *testing.T) {
	var sources []string
	markedBlockSources(func(src string) {
		if len(sources) < 3000 || len(sources)%97 == 0 {
			sources = append(sources, src)
		}
	})
	long := func(n int, unit string) string { return strings.Repeat(unit, n) }
	sources = append(sources, long(100, "paragraph\n\n"), long(40, "# h\n\n"), long(70, "- a\n- b\n\n"), long(33, "> q\n\n"), long(50, "| a |\n|-|\n| b |\n\n"), long(20, "[x]: u\n\nt [x]\n\n"), "x", "", long(300, "a\nb\n\n"), "é"+long(20000, "ü\n\n"))
	reused := &markedInlineState{}
	for i, src := range sources {
		want := lexMarkedBlocks(&markedInlineState{}, src)
		if got := lexMarkedBlocks(reused, src); got != want {
			t.Fatalf("source %d %.80q: reused lexer\n got %s\nwant %s", i, src, got, want)
		}
		reused.startDocument()
	}
}
