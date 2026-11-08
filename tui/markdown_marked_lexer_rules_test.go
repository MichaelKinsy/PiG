package tui

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// The block lexer evaluates some of marked's rules without a regexp: they run for every line of every block the lexer reads. These tests compare each with the pattern marked builds for it, on every source of up to four characters from an alphabet of the characters the rules look at and on random longer ones.

func markedRuleSources(t *testing.T, each func(src string)) {
	t.Helper()
	// Under the race detector the corpus is sampled (race_on_test.go): every source of up to three characters and 20,000 random ones, not four and 200,000. The plain run takes all of it.
	depth, random := 4, 200000
	if raceEnabled {
		depth, random = 3, 20000
	}
	alphabet := []string{" ", "-", "+", "*", "_", "=", "1", "0", "9", ".", ")", "|", ":", "`", "~", "#", ">", "<", "!", "[", "]", "$", "\\", "a", "\n", "\r", "\u00a0", "\u2028", "é", "🙂"}
	var all func(prefix string, n int)
	all = func(prefix string, n int) {
		each(prefix)
		if n == 0 {
			return
		}
		for _, s := range alphabet {
			all(prefix+s, n-1)
		}
	}
	all("", depth)
	rng := rand.New(rand.NewPCG(11, 13))
	for range random {
		var b strings.Builder
		b.WriteString(strings.Repeat(" ", rng.IntN(6)))
		b.WriteString(strings.Repeat(alphabet[rng.IntN(len(alphabet))], rng.IntN(5)))
		for range rng.IntN(12) {
			b.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		each(b.String())
	}
}

func TestMarkedBlockRulesEqualTheirPatterns(t *testing.T) {
	listStart := newAnchoredMarkedRegexp(`^( {0,3}(?:[*+-]|\d{1,9}[.)]))([ \t][^\n]*?)?(?:\n|$)`, false)
	task := newAnchoredMarkedRegexp(`^\[[ xX]\] +\S`, false)
	taskPrefix := newAnchoredMarkedRegexp(`^\[[ xX]\] +`, false)
	newline := newAnchoredMarkedRegexp(`^(?:[ \t]*(?:\n|$))+`, false)
	latexStart := newMarkedRegexp(`(?:^|\n) {0,3}(?:\$\$|\\\[)`)
	itemRules := map[string]*markedRegexp{}
	for _, bull := range []string{"-", "*", "+", "1.", "12)"} {
		source := `\` + bull
		if len(bull) > 1 {
			source = `\d{1,9}\` + bull[len(bull)-1:]
		}
		itemRules[bull] = newAnchoredMarkedRegexp(`^( {0,3}`+source+`)((?:[\t ][^\n]*)?(?:\n|$))`, false)
	}
	var indentRules [4]struct{ bullet, hr, fences, heading, html, quote *markedRegexp }
	for n := range indentRules {
		spaces := " {0," + string(rune('0'+n)) + "}"
		indentRules[n].bullet = newAnchoredMarkedRegexp(`^`+spaces+`(?:[*+-]|\d{1,9}[.)])((?:[ \t][^\n]*)?(?:\n|$))`, false)
		indentRules[n].hr = newAnchoredMarkedRegexp(`^`+spaces+`((?:- *){3,}|(?:_ *){3,}|(?:\* *){3,})(?:\n+|$)`, false)
		indentRules[n].fences = newAnchoredMarkedRegexp("^"+spaces+"(?:```|~~~)", false)
		indentRules[n].heading = newAnchoredMarkedRegexp(`^`+spaces+`#`, false)
		indentRules[n].html = newAnchoredMarkedRegexp(`^`+spaces+`<(?:[a-z].*>|!--)`, true)
		indentRules[n].quote = newAnchoredMarkedRegexp(`^`+spaces+`>`, false)
	}
	paragraph := newAnchoredMarkedRegexp(markedParagraphInterrupt, false)
	quoteParagraph := newAnchoredMarkedRegexp(markedQuoteInterrupt, false)
	quote := newAnchoredMarkedRegexp(`( {0,3}> ?([^\n]+(?:\n(?!`+markedQuoteInterrupt+`)[^\n]+)*|[^\n]*)(?:\n|$))+`, false)

	markedRuleSources(t, func(src string) {
		runes := []rune(src)
		line, _, _ := strings.Cut(src, "\n")

		// Tokenizer.list: the bullet that starts a list.
		match := listStart.exec(runes)
		end, ok := markedBulletEnd(runes, markedLeadingSpaces(runes))
		if ok != (match != nil) && markedLeadingSpaces(runes) <= 3 || ok && markedLeadingSpaces(runes) <= 3 && match != nil && end != match.GroupByNumber(1).Index+match.GroupByNumber(1).Length {
			t.Fatalf("bullet of %q: %d %v, the pattern gives %v", src, end, ok, match != nil)
		}

		// Tokenizer.list: the item rule of each kind of bullet.
		for bull, rule := range itemRules {
			match := rule.exec(runes)
			bulletLength, length, ok := markedListItem(runes, bull)
			if ok != (match != nil) || ok && (bulletLength != match.GroupByNumber(1).Length || length != match.Length) {
				t.Fatalf("item %q of %q: %d, %d, %v; the pattern gives %v", bull, src, bulletLength, length, ok, match != nil)
			}
		}

		// The rules that end a list item at a line, for each indent.
		for n, rules := range indentRules {
			for _, check := range []struct {
				name string
				got  bool
				rule *markedRegexp
			}{
				{"bullet", markedItemBegin(line, n), rules.bullet},
				{"hr", markedHrBegin(line, n), rules.hr},
				{"fences", markedFencesBegin(line, n), rules.fences},
				{"heading", markedHeadingBegin(line, n), rules.heading},
				{"html", markedHTMLBeginAt(line, n), rules.html},
				{"quote", markedBlockquoteBegin(line, n), rules.quote},
			} {
				if want := check.rule.exec([]rune(line)) != nil; check.got != want {
					t.Fatalf("%s rule of indent %d on %q: %t, the pattern gives %t", check.name, n, line, check.got, want)
				}
			}
		}

		// Task markers.
		length, isTask := markedTaskPrefix(src)
		if want := task.exec(runes) != nil; isTask != want {
			t.Fatalf("task %q: %t, want %t", src, isTask, want)
		}
		if prefix := taskPrefix.exec(runes); (prefix != nil) != (length > 0) || prefix != nil && len(string(runes[:prefix.Length])) != length {
			t.Fatalf("task prefix of %q: %d, the pattern gives %v", src, length, prefix != nil)
		}

		// Lexer.blockTokens: the newline rule.
		if n, want := markedSpaceLength(runes), newline.exec(runes); want == nil && n != 0 || want != nil && n != want.Length {
			t.Fatalf("space of %q: %d, the pattern gives %v", src, n, want)
		}

		// The lookahead of the paragraph rule, in the document and in a blockquote.
		if len(runes) > 0 {
			if got, want := markedParagraphInterrupted(runes, false), paragraph.exec(runes) != nil; got != want {
				t.Fatalf("paragraph lookahead on %q: %t, want %t", src, got, want)
			}
			if got, want := markedParagraphInterrupted(runes, true), quoteParagraph.exec(runes) != nil; got != want {
				t.Fatalf("blockquote paragraph lookahead on %q: %t, want %t", src, got, want)
			}
		}

		// The blockquote rule.
		if got, want := markedQuoteMatch(runes), quote.exec(runes); want == nil && got != 0 || want != nil && got != want.Length {
			t.Fatalf("blockquote of %q: %d, the pattern gives %v", src, got, want)
		}

		// The start() of the latexBlock extension on src.slice(1), read in UTF-16 code units, where a first character outside the BMP leaves a lone surrogate. The cut is src.substring(0, start+1).
		wantCut := -1
		if len(runes) > 0 {
			astral := runes[0] > 0xFFFF
			slice := runes[1:]
			if astral {
				slice = append([]rune{0xFFFD}, slice...)
			}
			if match := latexStart.exec(slice); match != nil {
				start := match.Index
				if slice[start] == '\n' {
					start++
				}
				wantCut = start + 1
				if astral {
					// The surrogate pair of the first character is two units but one code point.
					wantCut--
				}
			}
		}
		if got := markedLatexStart(runes); got+1 != wantCut && (got >= 0 || wantCut >= 0) {
			t.Fatalf("latex start of %q: cut %d, the pattern gives %d", src, got+1, wantCut)
		}
		// The memoized search of a lexing gives the start of each suffix.
		next := -1
		for pos := range runes {
			if got, want := markedLatexStartAt(runes, pos, &next), markedLatexStart(runes[pos:]); got != want {
				t.Fatalf("latex start of %q at %d: %d, want %d", src, pos, got, want)
			}
		}
	})
}

// markedLatexStart is markedLatexStartAt at the start of src.
func markedLatexStart(src []rune) int {
	next := -1
	return markedLatexStartAt(src, 0, &next)
}
