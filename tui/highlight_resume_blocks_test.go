//go:build !pig_strip_syntax_highlight

package tui

import (
	"encoding/json"
	"hash/fnv"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestStreamedHighlightIsAWholeRunAcrossBlocksAndEdits: a resume state holds for the code of its run and the text that extends
// it, not for any code that begins with the text before the point, because the decisions before the point read past it. The
// resume states and the memo are kept from one block to the next here, and each block is the last one appended to, cut short,
// grown again, edited in the middle or replaced by another with the same beginning; every step returns the lines a whole run
// returns, in every language of highlight.js's oracle corpus and in JavaScript and TypeScript soups.
func TestStreamedHighlightIsAWholeRunAcrossBlocksAndEdits(t *testing.T) {
	LoadAllHighlightLanguages()
	data, err := os.ReadFile("internal/hljs/testdata/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus []struct{ ID, Lang, Code string }
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	type sample struct{ lang, id, code string }
	var samples []sample
	for i, c := range corpus {
		if SupportsLanguage(c.Lang) && (!(testing.Short() || raceEnabled) || i%4 == 0) {
			samples = append(samples, sample{c.Lang, c.ID, c.Code})
		}
	}
	for seed := range 40 {
		random := rand.New(rand.NewPCG(uint64(seed), 5))
		var soup strings.Builder
		for range 8 + random.IntN(20) {
			soup.WriteString(streamedScriptPool[random.IntN(len(streamedScriptPool))])
		}
		samples = append(samples, sample{[]string{"javascript", "typescript"}[seed%2], "soup", soup.String()})
	}
	theme := ActiveTheme()
	hlMu.Lock()
	resetHighlightCacheLocked()
	hlTheme = theme
	hlMu.Unlock()
	tails := []string{"", "x", "\n", "(", ")", " =>", "/*", "*/", "//", "{", "'", "`", "<a>", "\n\n\n", "=>", ",\n  ", "\"", "#", "中"}
	steps, resumed := 0, 0
	for _, s := range samples {
		h := fnv.New64a()
		h.Write([]byte(s.id + s.lang))
		random := rand.New(rand.NewPCG(h.Sum64(), uint64(len(s.code))))
		// cut is a rune boundary of str at or after at, clamped to str.
		cut := func(str string, at int) int {
			at = min(max(at, 0), len(str))
			for at < len(str) && !utf8.RuneStart(str[at]) {
				at++
			}
			return at
		}
		text := s.code[:cut(s.code, random.IntN(len(s.code)+1))]
		for range 14 {
			switch random.IntN(5) {
			case 0: // append
				text = s.code[:cut(s.code, len(text)+1+random.IntN(max(1, len(s.code)/6)))]
			case 1: // cut short
				text = text[:cut(text, random.IntN(len(text)+1))]
			case 2: // grow again
				text = s.code[:cut(s.code, random.IntN(len(s.code)+1))]
			case 3: // edit in the middle
				at := cut(text, random.IntN(len(text)+1))
				end := cut(text, at+random.IntN(8))
				text = text[:at] + tails[random.IntN(len(tails))] + text[end:]
			case 4: // another block with the same beginning
				at := cut(text, random.IntN(len(text)+1))
				text = text[:at] + tails[random.IntN(len(tails))] + s.code[cut(s.code, random.IntN(len(s.code)+1)):]
			}
			if !utf8.ValidString(text) {
				text = strings.ToValidUTF8(text, "")
			}
			if base, _ := hlFindResume(text, s.lang, theme); base.prefix != "" {
				resumed++
			}
			steps++
			got := highlightCodeLines(text, s.lang)
			want := highlightCodeUncached(highlightRegistry(), text, s.lang, theme)
			if !slices.Equal(got, want) {
				t.Fatalf("%s (%s) at %d bytes %q:\n got %q\nwant %q", s.id, s.lang, len(text), text, got, want)
			}
		}
	}
	t.Logf("%d steps, %d resumed", steps, resumed)
}

// TestResumeStateDoesNotServeCodeThatOnlySharesItsPrefix: highlight a JavaScript block, then a block that begins with the text
// before the block's last resume point and differs after it. The first run read past that point, so the second must not resume
// from it. Several first blocks (a regular expression, an unclosed parenthesis, a comment) are tried, as which point is the
// last one kept depends on the guards.
func TestResumeStateDoesNotServeCodeThatOnlySharesItsPrefix(t *testing.T) {
	theme := ActiveTheme()
	second := "// line\n=>中async (,\n  /*,\n  /* "
	for _, lang := range []string{"javascript", "typescript"} {
		for _, first := range []string{
			"/x/\n(\n  /*,\n  /* ",
			"/x/;\nfoo(\n  /*,\n  /* ",
			"/a/ // c\n(\n  /*,\n  /* ",
			"/\n(\n  /*,\n  /* ",
			"/x/g.test(a);\n// line\n=> async (,\n  /*,\n  /* ",
			"a = /x/;\n// line\n x\n(\n",
			"// line\n(\n",
			"// line\n=> async (,\n  /*,\n  /* ",
		} {
			hlMu.Lock()
			resetHighlightCacheLocked()
			hlTheme = theme
			hlMu.Unlock()
			highlightCodeLines(first, lang)
			for _, code := range []string{second, first[:len(first)/2] + second, strings.SplitN(first, "\n", 2)[0] + "\n" + second} {
				got := highlightCodeLines(code, lang)
				want := highlightCodeUncached(highlightRegistry(), code, lang, theme)
				if !slices.Equal(got, want) {
					t.Fatalf("%s: %q then %q:\n got %q\nwant %q", lang, first, code, got, want)
				}
			}
		}
	}
}
