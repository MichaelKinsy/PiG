package hljs

import (
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestGuardedSourcesArePinned: a language keeps resume points in spite of a pattern that reads without bound only when the
// pattern is one guardedSources lists, and only javascript and typescript have them. A grammar update that changes one of
// these patterns, or adds a pattern of another language that reads without bound and has a guard's source, fails here.
func TestGuardedSourcesArePinned(t *testing.T) {
	r := NewRegistry()
	r.LoadAllLanguages()
	var guarded []string
	for _, name := range r.Languages() {
		reach := languageReach(compileLanguage(r.getLanguage(name)))
		if reach.guards != 0 {
			guarded = append(guarded, name)
		}
	}
	if want := []string{"javascript", "typescript"}; !slices.Equal(guarded, want) {
		t.Fatalf("languages with guarded patterns: %v, want %v", guarded, want)
	}
	for _, name := range guarded {
		lang := compileLanguage(r.getLanguage(name))
		reach := languageReach(lang)
		if reach.unbounded {
			t.Errorf("%s: a pattern that reads without bound has no guard", name)
		}
		if reach.lines != 1 {
			t.Errorf("%s: reach of %d lines, want 1: the guarded patterns count their own line break, and no other pattern reads more than one", name, reach.lines)
		}
		if want := guardParens | guardTrivia | guardAngle; reach.guards != want {
			t.Errorf("%s: guards %b, want %b", name, reach.guards, want)
		}
		sources := lang.setLang().sources
		for source := range guardedSources {
			if !slices.Contains(sources, source) {
				t.Errorf("%s: no pattern %q", name, source)
			}
		}
		if !slices.Contains(sources, xmlTagBegin) {
			t.Errorf("%s: no pattern %q", name, xmlTagBegin)
		}
	}
	for source := range guardedSources {
		if !patternReach(source).unbounded {
			t.Errorf("%q does not read without bound: it needs no guard", source)
		}
	}
}

func TestGuardPoints(t *testing.T) {
	filler := " a b c d e f g h\n"
	for _, c := range []struct {
		name   string
		code   string
		guards guardSet
		at     string // the text before the point
		want   bool
	}{
		{"parens closed", "f(a, b);" + filler, guardParens, "f(a, b);", true},
		{"parens open", "f(a,\n b);" + filler, guardParens, "f(a,\n", false},
		{"parens nested open", "f((a),\n b);" + filler, guardParens, "f((a),\n", false},
		{"a closing parenthesis without an opening one", "a);" + filler, guardParens, "a);", true},
		{"an angle name followed by a character", "a<b;" + filler, guardAngle, "a<b;", true},
		{"an angle name followed by a greater sign", "a<b>c;" + filler, guardAngle, "a<b>c;", false},
		{"an angle name at the point that ends the code", "a; <b", guardAngle, "a; ", false},
		{"a parenthesis at the point", "x = (a,\n b);" + filler, guardParens, "x = ", false},
		{"a brace at the point with comments to the end", "a ;{ // x\n", guardTrivia, "a ;", false},
		{"a brace at the point then a block comment that does not end", "a ;{ /* c d e f g h i j", guardTrivia, "a ;", false},
		{"a comma at the point then a block comment that does not end", "a ;, /* c d e f g h i j", guardTrivia, "a ;", false},
		{"an angle with a space", "a < b;" + filler, guardAngle, "a < b;", true},
		{"a shift", "a << b;" + filler, guardAngle, "a << b;", true},
		{"an angle name that runs past the point", "a<bcd;" + filler, guardAngle, "a<b", true},
		{"a fragment", "a = <>;" + filler, guardAngle, "a = <>;", true},
		{"trivia then code", "a;\n// c\n/* d */\n" + filler, guardTrivia, "a;\n", true},
		{"only a line comment after", "a;\n" + "// c", guardTrivia, "a;\n", false},
		{"only comments after", "a;\n// c\n// d\n", guardTrivia, "a;\n", false},
		{"a block comment that does not end", "a;\n/* c" + filler, guardTrivia, "a;\n", false},
		{"a block comment that ends after an even run of stars", "a;\n/** c **/" + filler, guardTrivia, "a;\n", false},
		{"a block comment that ends after an odd run of stars", "a;\n/** c ***/" + filler, guardTrivia, "a;\n", true},
		{"an empty block comment", "a;\n/**/" + filler, guardTrivia, "a;\n", true},
		{"too few tokens after", "a;\n  b c d e f", guardTrivia, "a;\n", false},
		{"tokens after", "a;\n  b c d e f g h", guardTrivia, "a;\n", true},
		{"white space only after", "a;\n\n\n\n\n\n\n\n\n", guardTrivia, "a;\n", false},
		{"no guards", "f(a,\n b);" + filler, 0, "f(a,\n", true},
	} {
		points := []ResumePoint{{Byte: len(c.at)}}
		got := len(guardPoints(slices.Clone(points), c.code, c.guards)) == 1
		if got != c.want {
			t.Errorf("%s: point after %q in %q kept: %v, want %v", c.name, c.at, c.code, got, c.want)
		}
	}
}

// guardedPool is the pieces of text the differential tests join into JavaScript and TypeScript: parentheses that close lines
// later, arrow functions and methods whose parameters span lines, object keys after comments, block comments that end after
// an even run of stars, tags and type arguments, and strings and templates holding what the patterns look for.
var guardedPool = []string{
	"const a = 1;\n", "function f(a, b) {\n", "}\n", "\n", "\n\n\n", "  return a + b;\n",
	"const g = (a, b) => a + b;\n", "const h = (a,\n  b,\n  c) =>\n  a;\n", "foo(a,\n  b);\n", "x = (\n", ") => 1;\n",
	"const o = {\n  key: 1,\n  // note\n  other: 2,\n  /* c */ third: 3,\n};\n", "{ a: 1, b: 2 }\n",
	"// a line comment\n", "// another\n// and another\n", "/* a block */\n", "/** doc **/\n", "/*** x ***/\n", "/**/\n", "/*\n * multi\n */\n",
	"class A {\n  m(x) {\n    return x;\n  }\n  get v() { return 1; }\n}\n", "m(a, b) {\n}\n",
	"const t: Array<string> = [];\n", "let x = a<b;\n", "let y = a < b;\n", "let z = a<b>c;\n", "const e = <div>text</div>;\n", "const f2 = <>x</>;\n", "<b\n",
	"const s = \"(\";\n", "const u = `(${a}) =>`;\n", "const r = /(a|b)/g;\n", "const c = x ? (y) : (z);\n", "if (a) {\n} else {\n}\n",
	"interface I { a: number; b?: string }\n", "type T = (a: number) => void;\n", "export default function () {}\n",
	"a, b\n", "{\n", ",\n", "key:\n", "  v: 1,\n", "const n = 0x1f + 1.5e3;\n", "`\n", "'str'\n", "\"d\"\n",
}

func guardedText(random *rand.Rand, pieces int) string {
	var text strings.Builder
	for range pieces {
		text.WriteString(guardedPool[random.IntN(len(guardedPool))])
	}
	return text.String()
}

// checkResumedFromEveryPoint compares, for each point the run over code[:cut] offers, the HTML before the point joined with a
// run resumed over more text, with a fresh run over that text.
func checkResumedFromEveryPoint(t *testing.T, r *Registry, lang, code string, cut int, extensions []int) (points int) {
	t.Helper()
	prefixHTML, offered, err := r.HighlightResumable(NewText(code[:cut]), lang, true, ResumePoint{})
	if err != nil {
		t.Fatalf("%s: %v", lang, err)
	}
	for _, end := range extensions {
		want, err := r.Highlight(code[:end], lang, true)
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		for _, p := range offered {
			suffix, _, err := r.HighlightResumable(NewText(code[:end]), lang, true, p)
			if err != nil {
				continue
			}
			points++
			if got := prefixHTML[:p.HTML] + suffix; got != want {
				t.Fatalf("%s: resuming at byte %d of a %d-byte prefix of %d bytes changed the HTML\ncode %q\nwant %q\n got %q", lang, p.Byte, cut, end, code[:end], want, got)
			}
		}
	}
	return points
}

// TestGuardedResumeEqualsAFreshRun: random JavaScript and TypeScript, cut where a model could stop, resumed from every point
// the checks keep over the whole text and over a longer cut, is a fresh run.
func TestGuardedResumeEqualsAFreshRun(t *testing.T) {
	seeds := 12
	if testing.Short() {
		seeds = 3
	}
	r := NewRegistry()
	r.LoadAllLanguages()
	total := 0
	for seed := range seeds {
		random := rand.New(rand.NewPCG(uint64(seed), 0x9e3779b97f4a7c15))
		code := guardedText(random, 8+random.IntN(24))
		for _, lang := range []string{"typescript", "javascript"} {
			for cut := 1; cut <= len(code); cut += 1 + random.IntN(9) {
				for cut < len(code) && !utf8.RuneStart(code[cut]) {
					cut++
				}
				more := min(len(code), cut+1+random.IntN(len(code)))
				for more < len(code) && !utf8.RuneStart(code[more]) {
					more++
				}
				total += checkResumedFromEveryPoint(t, r, lang, code, cut, []int{len(code), more})
			}
		}
	}
	t.Logf("%d resumed runs equal a fresh run", total)
	if total < 1000 {
		t.Fatalf("only %d resumed runs: the checks keep too few points to test", total)
	}
}

// guardedTokens are the tokens of a text with a few parentheses, braces, commas, comments, colons, arrows and angle brackets,
// the characters the patterns that read without bound begin at or look for.
var guardedTokens = []string{"(", ")", "=>", "{", "}", ",", ":", ";", "k", "k:", "f(", "a", "b", "=", "<", ">", "<t", "/>", "</t>", "/*", "*/", "**/", "//", "'", "`", "\n", " "}

// TestGuardedResumeOfTokenSoup: short random sequences of the tokens above, cut at every byte, resumed from every point over the
// whole text, are a fresh run. PIG_HLJS_SOUP=n runs n sequences instead of the default.
func TestGuardedResumeOfTokenSoup(t *testing.T) {
	samples := 300
	if testing.Short() {
		samples = 60
	}
	if env := os.Getenv("PIG_HLJS_SOUP"); env != "" {
		n, err := strconv.Atoi(env)
		if err != nil {
			t.Fatal(err)
		}
		samples = n
	}
	r := NewRegistry()
	r.LoadAllLanguages()
	for seed := range samples {
		random := rand.New(rand.NewPCG(uint64(seed), 0x51ed270b))
		var text strings.Builder
		for range 3 + random.IntN(14) {
			text.WriteString(guardedTokens[random.IntN(len(guardedTokens))])
			switch random.IntN(5) {
			case 0:
				text.WriteString(" ")
			case 1:
				text.WriteString("\n")
			case 2:
				text.WriteString(strings.Repeat("\n", 1+random.IntN(8)))
			}
		}
		code := text.String()
		for _, lang := range []string{"typescript", "javascript"} {
			for cut := 1; cut < len(code); cut++ {
				checkResumedFromEveryPoint(t, r, lang, code, cut, []int{len(code)})
			}
		}
	}
}

// TestGuardedResumeFollowsLaterWhiteSpaceTails: a pattern that reads across white space or comments to a few characters after
// them reaches the end of a text that stops there, and the text that follows decides it. Every cut of each text, resumed from
// each of its points over the whole text, is a fresh run.
func TestGuardedResumeFollowsLaterWhiteSpaceTails(t *testing.T) {
	r := NewRegistry()
	r.LoadAllLanguages()
	blank := strings.Repeat("\n", 8)
	for _, code := range []string{
		"a;\nfoo(a)" + blank + "=> 1;\nb;\n",
		"a;\nfoo(a)" + blank + "{ b: 1 }\nc;\n",
		"const o = {\n  a: 1," + blank + "  b: 2,\n};\nc;\n",
		"x = {\n// c1\n// c2\n// c3\n// c4\n// c5\n// c6\n  key: 1 }\nz;\n",
		"x = {\n/* a */\n/* b */" + blank + "key: 1 }\nz;\n",
		"x = {\n/** a **/" + blank + "key: 1 */ }\nz;\n",
		"a,\n" + blank + "  b: 1;\nc;\n",
		"a;\n(" + blank + "b) => c;\nd;\n",
		"a;\nconst f = (x" + blank + ") => x;\nd;\n",
		"a;\nclass A { m(x" + blank + ") { return x; } }\nd;\n",
		"a;\nlet t = <Tag" + blank + ">x</Tag>;\nd;\n",
		"a;\nlet t = a<b" + blank + ">c;\nd;\n",
	} {
		for _, lang := range []string{"typescript", "javascript"} {
			for cut := 1; cut < len(code); cut++ {
				checkResumedFromEveryPoint(t, r, lang, code, cut, []int{len(code)})
			}
		}
	}
}

// TestGuardedResumeKeepsPointsOfCode: the checks do not throw the points of ordinary code away, or resuming would not help.
func TestGuardedResumeKeepsPointsOfCode(t *testing.T) {
	r := NewRegistry()
	r.LoadAllLanguages()
	var code strings.Builder
	for n := range 40 {
		fmt.Fprintf(&code, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }\n", n, n%97, n)
	}
	for _, lang := range []string{"typescript", "javascript"} {
		_, points, err := r.HighlightResumable(NewText(code.String()), lang, true, ResumePoint{})
		if err != nil {
			t.Fatal(err)
		}
		if len(points) < 30 {
			t.Errorf("%s: %d points over 40 lines of code", lang, len(points))
		}
	}
}

// FuzzGuardedResume: any text cut anywhere, resumed from each point of its run over a longer text, is a fresh run.
func FuzzGuardedResume(f *testing.F) {
	random := rand.New(rand.NewPCG(1, 0x9e3779b97f4a7c15))
	for range 12 {
		f.Add(guardedText(random, 6+random.IntN(12)), uint16(random.IntN(400)), uint16(random.IntN(400)), random.IntN(2) == 0)
	}
	r := NewRegistry()
	r.LoadAllLanguages()
	f.Fuzz(func(t *testing.T, code string, cutAt, moreAt uint16, typescript bool) {
		if len(code) == 0 || len(code) > 2000 || !utf8.ValidString(code) {
			return
		}
		lang := "javascript"
		if typescript {
			lang = "typescript"
		}
		cut := int(cutAt) % len(code)
		more := cut + int(moreAt)%(len(code)-cut+1)
		checkResumedFromEveryPoint(t, r, lang, code, cut, []int{more, len(code)})
	})
}
