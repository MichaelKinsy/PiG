package hljs

import (
	"math/bits"
	"slices"
	"strings"
	"testing"
)

// matchersOf is every joined matcher a language's modes can run, one per starting rule of each resumable matcher.
func matchersOf(language *Mode) []*multiRegex {
	var out []*multiRegex
	seen := map[*Mode]bool{}
	var walk func(m *Mode)
	walk = func(m *Mode) {
		if m == nil || seen[m] {
			return
		}
		seen[m] = true
		if r := m.comp().matcher; r != nil {
			for i := range max(1, len(r.rules)) {
				if mr := r.getMatcher(i); !mr.empty {
					out = append(out, mr)
				}
			}
		}
		for _, c := range m.contains {
			walk(c.mode)
		}
		for _, c := range m.comp().cachedVariants {
			walk(c)
		}
		walk(m.starts)
	}
	walk(language)
	return out
}

const dispatchProbeText = "é中😀 \u2028\t\r\n# \"'`\\${}()[]<>=+-*/%&|^~!?:;,.@_0aZ 12 x\n"

// TestBranchDispatchEqualsTheJoinedExpression: at every position of every corpus sample (and of a text of punctuation and non-ASCII units), the matcher of every mode of the sample's language finds, with the dispatch, the match the joined regular expression finds: the same index, the same groups, the same branch.
func TestBranchDispatchEqualsTheJoinedExpression(t *testing.T) {
	r := NewRegistry()
	r.LoadAllLanguages()
	checked, skipped := 0, 0
	probed := map[string]bool{}
	for _, c := range resumeCorpus(t) {
		if !r.SupportsLanguage(c.Lang) {
			continue
		}
		language := compileLanguage(r.getLanguage(c.Lang))
		texts := []string{c.Code}
		if !probed[c.Lang] {
			probed[c.Lang] = true
			texts = append(texts, dispatchProbeText)
		}
		for _, text := range texts {
			units := stringToUnits(text)
			for _, m := range matchersOf(language) {
				if m.dispatch == nil {
					skipped++
					continue
				}
				// Scan the way the highlighter does: from each match to the next. Every match, and the gap before it, is compared; so is every 97th start.
				compare := func(p int) *jsMatch {
					want := m.matcherRe.exec(units, p)
					got := m.dispatch.exec(m.matcherRe, units, p, m.matchAt-1)
					checked++
					if (want == nil) != (got == nil) || want != nil && (want.index != got.index || !slices.Equal(want.spans, got.spans)) {
						t.Fatalf("%s %s from %d: joined %+v, dispatch %+v\nmatcher %s", c.ID, c.Lang, p, want, got, m.matcherRe.source)
					}
					return want
				}
				for p := 0; p <= len(units); {
					match := compare(p)
					if match == nil {
						break
					}
					p = match.index + 1
				}
				for p := 0; p <= len(units); p += 97 {
					compare(p)
				}
			}
		}
	}
	t.Logf("%d positions compared, %d matchers without dispatch", checked, skipped)
	if checked < 100_000 {
		t.Fatalf("only %d positions compared", checked)
	}
}

// TestBranchDispatchHighlightsLikeTheJoinedExpression: the HTML of every corpus sample is the same with and without the dispatch.
func TestBranchDispatchHighlightsLikeTheJoinedExpression(t *testing.T) {
	with := NewRegistry()
	with.LoadAllLanguages()
	dispatchEnabled = false
	without := NewRegistry()
	without.LoadAllLanguages()
	for _, c := range resumeCorpus(t) {
		if !with.SupportsLanguage(c.Lang) {
			continue
		}
		dispatchEnabled = true
		got, err := with.Highlight(c.Code, c.Lang, true)
		dispatchEnabled = false
		want, wantErr := without.Highlight(c.Code, c.Lang, true)
		if (err == nil) != (wantErr == nil) || got != want {
			t.Fatalf("%s (%s): dispatch %q (%v), joined %q (%v)", c.ID, c.Lang, got, err, want, wantErr)
		}
	}
	dispatchEnabled = true
}

// describeFirst is a first set as its ASCII members in order, then + for the non-ASCII units, $ for the end of the text and ? for a pattern that can match anywhere.
func describeFirst(f firstSet) string {
	var b strings.Builder
	for c := range rune(128) {
		if f.has(c) {
			b.WriteRune(c)
		}
	}
	if f.nonASCII {
		b.WriteByte('+')
	}
	if f.atEnd {
		b.WriteByte('$')
	}
	if f.nullable {
		b.WriteByte('?')
	}
	return b.String()
}

func TestPatternFirst(t *testing.T) {
	const all = "\x00\x01\x02\x03\x04\x05\x06\x07\x08\t\n\v\f\r\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~\x7f"
	except := func(drop string) string {
		return strings.Map(func(r rune) rune { return map[bool]rune{true: -1, false: r}[strings.ContainsRune(drop, r)] }, all)
	}
	for _, c := range []struct {
		source     string
		ignoreCase bool
		want       string
	}{
		{`\d+`, false, "0123456789"},
		{`a|b`, false, "ab"},
		{`(?=# type:)`, false, "#"},
		{`$`, false, "\n\r+$"},
		{`^foo`, false, "f"},
		{`\bfoo`, false, "f"},
		{`(?=a)(?=b)`, false, ""},
		{`(?=a)b?`, false, "a"},
		{`x*y`, false, "xy"},
		{`x*`, false, "x?"},
		{`(?:a|)b`, false, "ab"},
		{`a{0,2}b`, false, "ab"},
		{`a{2}b`, false, "a"},
		{`{a`, false, "{"},
		{`a{`, false, "a"},
		{`ab`, true, "Aa+"},
		{`[a-c]`, true, "ABCabc+"},
		{`[^a]`, false, except("a") + "+"},
		{`.`, false, except("\n\r") + "+"},
		{`\s+`, false, "\t\n\v\f\r +"},
		{`[\w-]`, false, "-0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ_abcdefghijklmnopqrstuvwxyz"},
		{`[\]x]`, false, "]x"},
		{`\1`, false, all + "+?"},
		{`(?<=a)b`, false, "b"},
		{`(?=a)?b`, false, "ab"},
		{`(?:(?=a))b`, false, "b"},
		{`(?!a)b`, false, "b"},
		{`a(?=b)`, false, "a"},
		{`(?:(?=a)|b)c`, false, "bc"},
		{`\u0041|\x42|\cJ|\n`, false, "\nAB"},
		{`é`, false, "+"},
		{`[^\n]`, false, except("\n") + "+"},
		{`(`, false, all + "+?"},
	} {
		if got := describeFirst(patternFirst(c.source, c.ignoreCase)); got != c.want {
			t.Errorf("patternFirst(%q, %v) = %q, want %q", c.source, c.ignoreCase, got, c.want)
		}
	}
}

// TestBranchDispatchSkipsMostPositions: the root matcher of the languages a streamed answer most often fences tries, per ASCII unit, a small part of its branches: the dispatch skips a position when none is left, and a regression to the joined expression's try-everything cost shows here. Python's root has 28 branches and TypeScript's 32; the joined expression tries every one at every position.
func TestBranchDispatchSkipsMostPositions(t *testing.T) {
	r := NewRegistry()
	r.LoadAllLanguages()
	for _, lang := range []string{"python", "typescript", "go", "bash", "json", "java"} {
		if _, err := r.Highlight("x\n", lang, true); err != nil {
			t.Fatal(err)
		}
		d := compileLanguage(r.getLanguage(lang)).comp().matcher.getMatcher(0).dispatch
		if d == nil {
			t.Fatalf("%s: root matcher has no dispatch", lang)
		}
		tried, skipped := 0, 0
		for c := range 128 {
			n := bits.OnesCount64(d.ascii[c] | d.always)
			tried += n
			if n == 0 {
				skipped++
			}
		}
		if avg := float64(tried) / 128; avg > 2 {
			t.Errorf("%s: %d branches, %.2f tried per ASCII unit, %d of 128 units skipped", lang, len(d.branches), avg, skipped)
		}
	}
}
