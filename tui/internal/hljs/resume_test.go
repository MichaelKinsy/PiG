package hljs

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// resumeCorpus is the highlight.js oracle corpus: one code sample per language case.
func resumeCorpus(t *testing.T) []struct{ ID, Lang, Code string } {
	t.Helper()
	data, err := os.ReadFile("testdata/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus []struct{ ID, Lang, Code string }
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	return corpus
}

// TestHighlightResumableEqualsAFreshRun: for every corpus sample cut at many prefixes, the HTML before a resume point of the prefix's run joined with the HTML of a resumed run over the whole code is the HTML of a fresh run over the whole code.
func TestHighlightResumableEqualsAFreshRun(t *testing.T) {
	r := NewRegistry()
	r.LoadAllLanguages()
	checked, resumed := 0, 0
	for _, c := range resumeCorpus(t) {
		if !r.SupportsLanguage(c.Lang) {
			continue
		}
		want, err := r.Highlight(c.Code, c.Lang, true)
		if err != nil {
			continue
		}
		step := max(1, len(c.Code)/16)
		for cut := step; cut <= len(c.Code); cut += step {
			for cut < len(c.Code) && !utf8.RuneStart(c.Code[cut]) {
				cut++
			}
			prefix := c.Code[:cut]
			prefixHTML, points, err := r.HighlightResumable(prefix, c.Lang, true, ResumePoint{})
			if err != nil {
				t.Fatalf("%s: %v", c.ID, err)
			}
			if plain, _ := r.Highlight(prefix, c.Lang, true); plain != prefixHTML {
				t.Fatalf("%s: recording changed the HTML of a fresh run", c.ID)
			}
			for _, p := range points {
				checked++
				suffix, _, err := r.HighlightResumable(c.Code, c.Lang, true, p)
				if err != nil {
					continue
				}
				resumed++
				if got := prefixHTML[:p.HTML] + suffix; got != want {
					t.Fatalf("%s: resuming at byte %d of a %d-byte prefix of %d changed the HTML\nwant %q\n got %q", c.ID, p.Byte, cut, len(c.Code), want, got)
				}
			}
		}
	}
	t.Logf("%d resume points checked, %d resumed", checked, resumed)
	if resumed < 500 {
		t.Fatalf("only %d resumed runs: the corpus no longer exercises resumption", resumed)
	}
}

// TestHighlightResumableFollowsLaterText covers a text whose earlier tokens are decided by text that arrives much later: a multi-line arrow-function parameter list whose closing parenthesis comes after many lines. A language whose patterns read without bound offers no resume point, and every cut resumed from every point it does offer equals a whole run.
func TestHighlightResumableFollowsLaterText(t *testing.T) {
	r := NewRegistry()
	r.LoadAllLanguages()
	code := "foo();\nconst f = (a,\n" + strings.Repeat("  b,\n", 40) + ") => a;\n" + strings.Repeat("bar();\n", 10)
	for _, lang := range []string{"javascript", "typescript"} {
		want, err := r.Highlight(code, lang, true)
		if err != nil {
			t.Fatal(err)
		}
		for cut := 1; cut <= len(code); cut++ {
			prefixHTML, points, err := r.HighlightResumable(code[:cut], lang, true, ResumePoint{})
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range points {
				suffix, _, err := r.HighlightResumable(code, lang, true, p)
				if err != nil {
					continue
				}
				if got := prefixHTML[:p.HTML] + suffix; got != want {
					t.Fatalf("%s: resuming at byte %d of a %d-byte prefix changed the HTML\nwant %q\n got %q", lang, p.Byte, cut, want, got)
				}
			}
		}
	}
}

// TestResumeLimit pins where a resume point may sit: at least the pattern reach plus one line break before the end, and before the last character that is not white space when a pattern repeats white space.
func TestResumeLimit(t *testing.T) {
	for _, c := range []struct {
		name  string
		code  string
		reach reach
		want  int
	}{
		{"one break after", "a\nb\nc\nd", reach{}, 5},
		{"no break", "ab", reach{}, -1},
		{"reach adds breaks", "a\nb\nc\nd", reach{lines: 1}, 3},
		{"reach beyond the breaks", "a\nb", reach{lines: 1}, -1},
		{"trailing white space", "a  \n\n\n\n", reach{spaceRun: true}, 0},
		{"white space without a repeated pattern", "a  \n\n\n\n", reach{}, 6},
	} {
		if got := resumeLimit(c.code, c.reach); got != c.want {
			t.Errorf("%s: resumeLimit(%q, %+v) = %d, want %d", c.name, c.code, c.reach, got, c.want)
		}
	}
}

// TestPatternReach pins which patterns read without bound across lines.
func TestPatternReach(t *testing.T) {
	for _, c := range []struct {
		source string
		want   reach
	}{
		{`\d+(\.\d*)?`, reach{}},
		{`[A-Za-z0-9\\._:-]+`, reach{}},
		{`\S+`, reach{}},
		{`.*$`, reach{}},
		{`[^\n]*`, reach{}},
		{`[^\r\n]*`, reach{}},
		{`[^\s"]*`, reach{}},
		// A class that negates "\r" or U+2028 but not "\n" still matches "\n".
		{`[^\r]*`, reach{lines: 1, unbounded: true}},
		{"[^\u2028\r\"]+", reach{lines: 1, unbounded: true}},
		{`[^"]*`, reach{lines: 1, unbounded: true}},
		{`[\s\S]*?`, reach{lines: 1, unbounded: true}},
		{`(.|\n)*?`, reach{lines: 1, unbounded: true}},
		{`\s*(\{|$)`, reach{lines: 1, spaceRun: true}},
		{`\n[ \t]+`, reach{lines: 1}},
		{`a\nb\n`, reach{lines: 2}},
		{`\(\s*\)`, reach{lines: 1, spaceRun: true}},
		{`\w[\w :\/.-]*:`, reach{}},
		{`[{,\n]\s*(?=(\/\/.*$)*x)`, reach{lines: 2, spaceRun: true}},
	} {
		if got := patternReach(c.source); got != c.want {
			t.Errorf("patternReach(%q) = %+v, want %+v", c.source, got, c.want)
		}
	}
}

// TestResumedRunCountsTheIterationsOfAWholeRun: highlight.js throws once its scan iterations exceed highlightJSIterationLimit and three times the match index, so a run resumed at a checkpoint must count the iterations before it. For every checkpoint of every corpus sample, the resumed run ends with the iteration count of the whole run.
func TestResumedRunCountsTheIterationsOfAWholeRun(t *testing.T) {
	r := NewRegistry()
	r.LoadAllLanguages()
	checked := 0
	for _, c := range resumeCorpus(t) {
		if !r.SupportsLanguage(c.Lang) {
			continue
		}
		units := stringToUnits(c.Code)
		whole := r.highlightFrom(c.Lang, units, true, nil, checkpoint{}, true)
		if whole.failed {
			continue
		}
		for _, point := range whole.checkpoints {
			resumed := r.highlightFrom(c.Lang, units, true, nil, point, false)
			if resumed.failed {
				t.Fatalf("%s: the run resumed at unit %d failed", c.ID, point.unit)
			}
			if resumed.iterations != whole.iterations {
				t.Fatalf("%s: the run resumed at unit %d counted %d iterations, the whole run %d", c.ID, point.unit, resumed.iterations, whole.iterations)
			}
			checked++
		}
	}
	if checked < 1000 {
		t.Fatalf("only %d checkpoints checked", checked)
	}
	t.Logf("%d checkpoints checked", checked)
}

// TestResumeLimitHoldsBackPointsLaterTextDecides covers the margins of resumeLimit with texts a later character changes. Apache's `[` after a line break changes how the line before it reads, so a point needs reach.lines+1 line breaks after it; QML's `{` or `:` after blank lines changes how the name before them reads, through a pattern that repeats white space, so a point needs a character other than white space after it. Every point a prefix offers, resumed over the prefix with that character after it, equals a whole run.
func TestResumeLimitHoldsBackPointsLaterTextDecides(t *testing.T) {
	r := NewRegistry()
	r.LoadAllLanguages()
	cases := []struct {
		lang     string
		prefixes []string
		tails    []string
	}{
		{"apache", []string{"x\nfoo\n", "x\nfoo \n", "x\n1\n", "x\n@foo\n", "x\nFoo.bar\n"}, []string{"["}},
		{"qml", []string{"x\nFoo.bar\n\n", "x\nFoo.bar \n\n", "x\nFoo.bar\t\n\n", "x\nFoo.bar\n \n\n"}, []string{"{", ":", "::"}},
	}
	for _, c := range cases {
		for _, prefix := range c.prefixes {
			prefixHTML, points, err := r.HighlightResumable(prefix, c.lang, true, ResumePoint{})
			if err != nil {
				t.Fatal(err)
			}
			for _, tail := range c.tails {
				code := prefix + tail + " y\nz\n"
				whole, err := r.Highlight(code, c.lang, true)
				if err != nil {
					t.Fatal(err)
				}
				for _, p := range points {
					suffix, _, err := r.HighlightResumable(code, c.lang, true, p)
					if err != nil {
						t.Fatal(err)
					}
					if got := prefixHTML[:p.HTML] + suffix; got != whole {
						t.Errorf("%s %q then %q, resumed at byte %d:\n got %q\nwant %q", c.lang, prefix, tail, p.Byte, got, whole)
					}
				}
			}
		}
	}
}

// TestLanguageReachDoesNotDependOnTheFirstText: a language's reach, and so the resume points it offers, is the same whether it is computed before any text is highlighted or after every corpus sample of the language is.
func TestLanguageReachDoesNotDependOnTheFirstText(t *testing.T) {
	fresh := NewRegistry()
	fresh.LoadAllLanguages()
	used := NewRegistry()
	used.LoadAllLanguages()
	for _, c := range resumeCorpus(t) {
		if used.SupportsLanguage(c.Lang) {
			if _, err := used.Highlight(c.Code, c.Lang, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, lang := range fresh.Languages() {
		before := languageReach(compileLanguage(fresh.getLanguage(lang)))
		after := languageReach(compileLanguage(used.getLanguage(lang)))
		if before != after {
			t.Errorf("%s: reach %+v before any text, %+v after the corpus", lang, before, after)
		}
	}
}
