package hljs

import (
	"slices"
	"testing"
)

// TestLexemeScannerReadsTheFormsItFinds pins which keyword patterns the scanner takes: a class repeated, or a class and a second class repeated, of ASCII units. Any other pattern is left to the regular expression.
func TestLexemeScannerReadsTheFormsItFinds(t *testing.T) {
	for _, c := range []struct {
		source string
		taken  bool
	}{
		{`\w+`, true},
		{`\d+`, true},
		{`[A-Za-z$_][0-9A-Za-z$_]*`, true},
		{`[a-zA-Z_]\w*`, true},
		{`[\w\.]+`, true},
		{`[\w.-]+`, true},
		{`[a-zA-Z][a-zA-Z0-9_-]*`, true},
		{`[-a-z]+`, true},
		{`\w*`, false},
		{`\w+?`, false},
		{`\b\w+\b`, false},
		{`\S+`, false},
		{`[^\s]+`, false},
		{`[\w\s]+`, false},
		{`[a-z]+|\d+`, false},
		{`(\w+)`, false},
		{`[a-z]`, false},
		{`[a-z]{2,}`, false},
		{`[a-zé]+`, false},
		{`[\w-x]+`, false},
		{`[a-\d]+`, false},
		{`[]+`, false},
		{`.+`, false},
		{`\w+\b`, false},
		{`[a-z][a-z]*[a-z]*`, false},
	} {
		if got := newLexemeScanner(c.source, false) != nil; got != c.taken {
			t.Errorf("newLexemeScanner(%q) taken = %v, want %v", c.source, got, c.taken)
		}
	}
}

// modesOf is every mode a language can reach.
func modesOf(language *Mode) []*Mode {
	var out []*Mode
	seen := map[*Mode]bool{}
	var walk func(m *Mode)
	walk = func(m *Mode) {
		if m == nil || seen[m] {
			return
		}
		seen[m] = true
		out = append(out, m)
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

// lexemesOf is the lexemes next finds in buffer, each as its bounds.
func lexemesOf(buffer []rune, next func(buffer []rune, from int) (start, end int, ok bool)) [][2]int {
	var out [][2]int
	for from := 0; ; {
		start, end, ok := next(buffer, from)
		if !ok {
			return out
		}
		out = append(out, [2]int{start, end})
		if end == from && start == from {
			return out
		}
		from = end
	}
}

// TestLexemeScannerFindsWhatTheKeywordPatternFinds: for every keyword pattern of every language that the scanner takes, the scanner and the regular expression find the same lexemes in three texts that put every code unit in every place the two forms tell apart: every unit in order (runs of a class), every unit alone between units no class holds (the first class), and every unit after a unit of the first class (the second class). Under the race detector every seventh unit is in them.
func TestLexemeScannerFindsWhatTheKeywordPatternFinds(t *testing.T) {
	r := NewRegistry()
	r.LoadAllLanguages()
	step := rune(1)
	if raceEnabled {
		step = 7
	}
	// U+E000 is a private-use unit: it has no case and no class of ASCII units holds it.
	const apart = 0xe000
	var inOrder, alone []rune
	for u := rune(0); u <= 0xffff; u += step {
		inOrder = append(inOrder, u)
		alone = append(alone, u, apart)
	}
	type key struct {
		source     string
		ignoreCase bool
	}
	seen := map[key]bool{}
	modes, taken := 0, 0
	for _, lang := range r.Languages() {
		language := compileLanguage(r.getLanguage(lang))
		for _, mode := range modesOf(language) {
			re := mode.comp().keywordPatternRe
			if re == nil {
				continue
			}
			modes++
			scanner := mode.comp().lexemes
			if scanner == nil {
				continue
			}
			taken++
			if k := (key{re.source, re.ignoreCase}); seen[k] {
				continue
			} else {
				seen[k] = true
			}
			// A unit of the first class, for the text that puts every unit after one.
			first := rune(-1)
			for u := range rune(128) {
				if scanner.has(&scanner.first, u) {
					first = u
					break
				}
			}
			if first < 0 {
				t.Fatalf("%s: the scanner of /%s/ has an empty first class", lang, re.source)
			}
			var after []rune
			for u := rune(0); u <= 0xffff; u += step {
				after = append(after, first, u, apart)
			}
			for name, text := range map[string][]rune{"every unit in order": inOrder, "every unit alone": alone, "every unit after one of the first class": after} {
				want := lexemesOf(text, func(buffer []rune, from int) (int, int, bool) {
					m, ok := re.exec(buffer, from)
					return m.index, m.end, ok
				})
				got := lexemesOf(text, scanner.next)
				if !slices.Equal(got, want) {
					at := 0
					for at < len(got) && at < len(want) && got[at] == want[at] {
						at++
					}
					t.Fatalf("%s /%s/ i=%v, %s: the scanner finds %d lexemes, the pattern %d; the first that differs is number %d", lang, re.source, re.ignoreCase, name, len(got), len(want), at)
				}
			}
		}
	}
	t.Logf("%d modes with a keyword pattern, %d of them scanned without the regular expression, %d distinct patterns compared", modes, taken, len(seen))
	if taken == 0 {
		t.Fatal("no keyword pattern is taken by the scanner")
	}
}
