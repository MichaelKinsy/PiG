package tui

import (
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestMarkedRuleSourcesMatchPinnedMarked proves every marked rule the Markdown port evaluates is the verbatim source marked 18.0.11 composes (Lexer.rules), read from the vendored package, so the port cannot drift from a hand-copied pattern.
func TestMarkedRuleSourcesMatchPinnedMarked(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { Lexer } from "./widthx/testdata/pi/marked/lib/marked.esm.js";
const pick = (rules, keys) => Object.fromEntries(keys.map((key) => [key, rules[key].source]));
console.log(JSON.stringify({
	inline: pick(Lexer.rules.inline.gfm, ["escape", "tag", "link", "reflink", "nolink", "reflinkSearch", "emStrongLDelim", "emStrongRDelimAst", "emStrongRDelimUnd", "code", "br", "autolink", "url", "_backpedal", "text", "anyPunctuation", "blockSkip"]),
	block: pick(Lexer.rules.block.gfm, ["html", "code", "hr", "def", "table", "paragraph"]),
}));`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("pinned marked oracle: %v", err)
	}
	var want struct{ Inline, Block map[string]string }
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{
		"inline.escape":            markedInlineEscape.source,
		"inline.tag":               markedInlineTag.source,
		"inline.link":              markedInlineLink.source,
		"inline.reflink":           markedInlineReflink.source,
		"inline.nolink":            markedInlineNolink.source,
		"inline.reflinkSearch":     markedReflinkSearch.source,
		"inline.emStrongLDelim":    markedEmStrongLDelim.source,
		"inline.emStrongRDelimAst": markedEmStrongRDelimAst.source,
		"inline.emStrongRDelimUnd": markedEmStrongRDelimUnd.source,
		"inline.code":              markedInlineCode.source,
		"inline.br":                markedInlineBr.source,
		"inline.autolink":          markedInlineAutolink.source,
		"inline.url":               markedInlineURL.source,
		"inline._backpedal":        markedInlineBackpedal.source,
		"inline.text":              markedInlineText.source,
		"inline.anyPunctuation":    markedAnyPunctuation.source,
		"inline.blockSkip":         markedBlockSkip.source,
		"block.html":               markedBlockHTML.source,
		"block.code":               markedBlockCode.source,
		"block.hr":                 markedBlockHr.source,
		"block.def":                markedBlockDef.source,
		"block.table":              markedBlockTable.source,
		"block.paragraph":          markedBlockParagraphSource,
	}
	for name, source := range got {
		group, key, _ := strings.Cut(name, ".")
		expected := want.Inline[key]
		if group == "block" {
			expected = want.Block[key]
		}
		if expected == "" || source != expected {
			t.Errorf("%s:\n got %q\nwant %q", name, source, expected)
		}
	}
}

// TestTranslateMarkedPatternGivesJavaScriptMeaning pins the constructs whose regexp2 reading differs from JavaScript's.
func TestTranslateMarkedPatternGivesJavaScriptMeaning(t *testing.T) {
	for _, tc := range []struct {
		source, input string
		match         bool
	}{
		{`^a$`, "a\n", false},         // JavaScript $ without m is end of input; .NET $ also matches before a final newline.
		{`^\s$`, "\u0085", false},     // NEL is .NET whitespace, not JavaScript whitespace.
		{`^\s$`, "\uFEFF", true},      // BOM is JavaScript whitespace.
		{`^\w$`, "é", false},          // JavaScript \w is ASCII.
		{`^\d$`, "٣", false},          // JavaScript \d is ASCII.
		{`^.$`, "\u2028", false},      // . excludes every JavaScript line terminator.
		{`^[\s\S]$`, "\u0085", true},  // [\s\S] still matches every character.
		{`^a\b`, "aé", true},          // \b uses ASCII word characters.
		{`^[^\s*]$`, "\u3000", false}, // \s inside a class is JavaScript whitespace.
	} {
		re := newMarkedRegexp(tc.source)
		if got := re.exec([]rune(tc.input)) != nil; got != tc.match {
			t.Errorf("/%s/ on %q: match %t, want %t", tc.source, tc.input, got, tc.match)
		}
	}
}

// TestMarkedProceduralRulesMatchRegexps compares the hand-evaluated inline text and left-delimiter rules with marked's own patterns over generated sources rich in delimiters, schemes, emails, hard breaks and astral characters.
func TestMarkedProceduralRulesMatchRegexps(t *testing.T) {
	alphabet := []string{"a", "Z", "_", "*", "~", "`", " ", "  ", "\n", "@", ".", "!", "(", ")", "[", "<", "\\", "http", "HTTP", "ftp://", "www.", "wWw.", "x@y", "😀", "é", "\u3000", "-", "#", "+", "日"}
	seed := uint32(1)
	next := func(n int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % n
	}
	for range 20000 {
		var b strings.Builder
		for range 1 + next(12) {
			b.WriteString(alphabet[next(len(alphabet))])
		}
		src := []rune(b.String())
		wantText := 0
		if m := markedInlineText.exec(src); m != nil {
			wantText = m.Length
		}
		if got := markedInlineTextLength(src); got != wantText {
			t.Fatalf("inline text on %q: length %d, want %d", string(src), got, wantText)
		}
		if src[0] != '*' && src[0] != '_' {
			continue
		}
		wantNext, wantRun := markedDelimNone, 0
		if m := markedEmStrongLDelim.exec(src); m != nil {
			_, ok1 := markedGroup(m, 1)
			_, ok2 := markedGroup(m, 2)
			_, ok3 := markedGroup(m, 3)
			_, ok4 := markedGroup(m, 4)
			switch {
			case ok1 || ok3:
				wantNext, wantRun = markedDelimPunctuation, m.Length-1
			case ok2 || ok4:
				wantNext, wantRun = markedDelimOther, m.Length-1
			}
		}
		run, gotNext := markedLeftDelimiter(src)
		if gotNext != wantNext || gotNext != markedDelimNone && run != wantRun {
			t.Fatalf("left delimiter on %q: (%d, %d), want (%d, %d)", string(src), run, gotNext, wantRun, wantNext)
		}
	}
}

// TestMarkedRightDelimiterScanMatchesRegexps compares the hand-evaluated closing-delimiter scan with emStrongRDelimAst and emStrongRDelimUnd, match by match.
func TestMarkedRightDelimiterScanMatchesRegexps(t *testing.T) {
	alphabet := []string{"a", "*", "**", "_", "__", "~", " ", "\n", ".", "(", "😀", "é", "\u3000", "+", "#", "日", "[aa]"}
	seed := uint32(7)
	next := func(n int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % n
	}
	for range 30000 {
		var b strings.Builder
		for range 1 + next(14) {
			b.WriteString(alphabet[next(len(alphabet))])
		}
		src := []rune(b.String())
		for _, delim := range []rune{'*', '_'} {
			re := markedEmStrongRDelimUnd
			if delim == '*' {
				re = markedEmStrongRDelimAst
			}
			pos := 0
			for m := re.exec(src); m != nil; m = re.next(m) {
				wantGroup, wantLength := 0, 0
				for g := 1; g <= 6; g++ {
					if text, ok := markedGroup(m, g); ok && text != "" {
						wantGroup, wantLength = g, m.GroupByNumber(g).Length
						break
					}
				}
				index, end, group, length, ok := markedRightDelimiterMatch(src, pos, delim)
				if !ok || index != m.Index || end != m.Index+m.Length || group != wantGroup || length != wantLength {
					t.Fatalf("%q delim %q from %d: (%d, %d, %d, %d, %t), want (%d, %d, %d, %d)", string(src), delim, pos, index, end, group, length, ok, m.Index, m.Index+m.Length, wantGroup, wantLength)
				}
				pos = end
			}
			if _, _, _, _, ok := markedRightDelimiterMatch(src, pos, delim); ok {
				t.Fatalf("%q delim %q: extra match after %d", string(src), delim, pos)
			}
		}
	}
}

// markedMaskSourceRegexp is Lexer.ts maskedSrc without link definitions evaluated with marked's own anyPunctuation and blockSkip patterns, the reference for markedMaskSource.
func markedMaskSourceRegexp(src []rune) []rune {
	masked := slices.Clone(src)
	type span struct{ start, length int }
	var spans []span
	for m := markedAnyPunctuation.exec(masked); m != nil; m = markedAnyPunctuation.next(m) {
		spans = append(spans, span{m.Index, m.Length})
	}
	for _, s := range spans {
		for i := s.start; i < s.start+s.length; i++ {
			masked[i] = '+'
		}
	}
	spans = spans[:0]
	for m := markedBlockSkip.exec(masked); m != nil; m = markedBlockSkip.next(m) {
		spans = append(spans, span{m.Index, m.Length})
	}
	for _, s := range spans {
		masked[s.start] = '['
		for i := s.start + 1; i < s.start+s.length-1; i++ {
			masked[i] = 'a'
		}
		masked[s.start+s.length-1] = ']'
	}
	return masked
}

// TestMarkedHandEvaluatedRulesMatchRegexps compares the rules evaluated without a regexp (emphasis masking, code span, br, escape, strict strikethrough, hr and punctuation unescaping) with the patterns they replace, and checks that the bracket reach never rejects an offset where the link, reflink or nolink rule matches, over generated sources rich in the characters each rule reads.
func TestMarkedHandEvaluatedRulesMatchRegexps(t *testing.T) {
	alphabet := []string{"a", "[", "]", "(", ")", "](", "][", "`", "``", "\\", "\\(", "<", ">", "< ", " ", "  ", "\n", "\u2028", "!", "*", "~", "~~", "-", "_", "\t", "😀", "é", "\u3000"}
	seed := uint32(11)
	next := func(n int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % n
	}
	for range 40000 {
		var b strings.Builder
		for range 1 + next(16) {
			b.WriteString(alphabet[next(len(alphabet))])
		}
		str := b.String()
		src := []rune(str)
		if got, want := markedMaskSource(slices.Clone(src)), markedMaskSourceRegexp(src); !slices.Equal(got, want) {
			t.Fatalf("mask %q: %q, want %q", str, string(got), string(want))
		}
		wantEnd := 0
		if m := markedInlineCode.exec(src); m != nil {
			wantEnd = m.Length
		}
		if _, end, _ := markedInlineCodeMatch(src); end != wantEnd {
			t.Fatalf("code %q: end %d, want %d", str, end, wantEnd)
		}
		wantEnd = 0
		if m := markedInlineBr.exec(src); m != nil {
			wantEnd = m.Length
		}
		if got := markedBreakLength(src); got != wantEnd {
			t.Fatalf("br %q: %d, want %d", str, got, wantEnd)
		}
		wantEnd = 0
		if m := markedInlineEscape.exec(src); m != nil {
			wantEnd = m.Length
		}
		if tok, ok := markedEscapeToken(src, str); len(tok.raw) != wantEnd || ok != (wantEnd > 0) {
			t.Fatalf("escape %q: %q, want length %d", str, tok.raw, wantEnd)
		}
		wantText, wantEnd := 0, 0
		if m := markedStrictStrikethrough.exec(src); m != nil {
			g := m.GroupByNumber(2)
			wantText, wantEnd = g.Index+g.Length, m.Length
		}
		if textEnd, end, _ := markedStrictStrikethroughMatch(src); textEnd != wantText || end != wantEnd {
			t.Fatalf("strikethrough %q: (%d, %d), want (%d, %d)", str, textEnd, end, wantText, wantEnd)
		}
		if line, _, _ := strings.Cut(str, "\n"); isMarkedHr(line) != (markedBlockHr.exec([]rune(line)) != nil) {
			t.Fatalf("hr %q: %t", line, isMarkedHr(line))
		}
		var want strings.Builder
		last := 0
		for m := markedAnyPunctuation.exec(src); m != nil; m = markedAnyPunctuation.next(m) {
			want.WriteString(string(src[last:m.Index]) + string(src[m.Index+1:m.Index+m.Length]))
			last = m.Index + m.Length
		}
		want.WriteString(string(src[last:]))
		if got := markedUnescapePunctuation(str); got != want.String() {
			t.Fatalf("unescape %q: %q, want %q", str, got, want.String())
		}
		reach := markedBracketReachOf(str)
		for k, at := 0, 0; k < len(src); at, k = at+utf8.RuneLen(src[k]), k+1 {
			rules := reach.at(at)
			if !rules.link && markedInlineLink.exec(src[k:]) != nil {
				t.Fatalf("link reach rejects %q at %d, which the link rule matches", str, k)
			}
			if !rules.reflink && markedInlineReflink.exec(src[k:]) != nil {
				t.Fatalf("reflink reach rejects %q at %d, which the reflink rule matches", str, k)
			}
			if !rules.nolink && markedInlineNolink.exec(src[k:]) != nil {
				t.Fatalf("nolink reach rejects %q at %d, which the nolink rule matches", str, k)
			}
		}
	}
}

// TestMarkedHrMatchesRegexp compares the hand-evaluated hr rule with marked's pattern on the indentation and spacing boundaries the generated sources rarely reach.
func TestMarkedHrMatchesRegexp(t *testing.T) {
	for _, line := range []string{"---", "   ---", "    ---", "     ***", " - - -", "*\t*\t*", "___ ", "**", "-_-", "- - - x", "***---", "", "  "} {
		if got, want := isMarkedHr(line), markedBlockHr.exec([]rune(line)) != nil; got != want {
			t.Errorf("hr %q: %t, want %t", line, got, want)
		}
	}
}
