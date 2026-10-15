package yaml12

import (
	"math/rand"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// mutatedYAMLTexts makes n texts by mutating the corpus: characters inserted, deleted, replaced, duplicated and swapped, and lines cut and reindented. Each mutation keeps the YAML-significant characters likely, so the texts probe the parser's error and recovery paths.
func mutatedYAMLTexts(n int, seed int64) []string {
	corpus := yamlCorpus()
	rng := rand.New(rand.NewSource(seed))
	significant := []string{":", "-", "?", "#", "&", "*", "!", "|", ">", "[", "]", "{", "}", ",", "'", "\"", "\n", " ", "  ", "\t", "%", "@", "`", "\\", "---", "...", "~", ".", "0", "a", "é", "😀", "\r", "\u2028", "\u00a0"}
	mutate := func(src string) string {
		runes := []rune(src)
		for range 1 + rng.Intn(3) {
			at := 0
			if len(runes) > 0 {
				at = rng.Intn(len(runes) + 1)
			}
			switch rng.Intn(7) {
			case 0:
				runes = append(runes[:at], append([]rune(significant[rng.Intn(len(significant))]), runes[at:]...)...)
			case 1:
				if at < len(runes) {
					runes = append(runes[:at], runes[at+1:]...)
				}
			case 2:
				if at < len(runes) {
					runes[at], _ = utf8.DecodeRuneInString(significant[rng.Intn(len(significant))])
				}
			case 3:
				if at < len(runes) {
					runes = append(runes[:at+1], runes[at:]...)
				}
			case 4:
				if at+1 < len(runes) {
					runes[at], runes[at+1] = runes[at+1], runes[at]
				}
			case 5:
				lines := strings.Split(string(runes), "\n")
				i := rng.Intn(len(lines))
				switch rng.Intn(3) {
				case 0:
					lines[i] = "  " + lines[i]
				case 1:
					lines[i] = strings.TrimLeft(lines[i], " ")
				default:
					lines = append(lines[:i], lines[i+1:]...)
				}
				runes = []rune(strings.Join(lines, "\n"))
			default:
				other := []rune(corpus[rng.Intn(len(corpus))])
				if len(other) > 0 {
					from := rng.Intn(len(other))
					to := from + rng.Intn(len(other)-from+1)
					runes = append(runes[:at], append(append([]rune{}, other[from:to]...), runes[at:]...)...)
				}
			}
		}
		return string(runes)
	}
	mutated := make([]string, 0, n)
	for len(mutated) < n {
		mutated = append(mutated, mutate(corpus[rng.Intn(len(corpus))]))
	}
	return mutated
}

// TestParseMatchesTheLibraryOnMutatedCorpus compares parse() of eemeli/yaml 2.9.0 with Parse for mutated corpus texts: the value, or the exact error message.
func TestParseMatchesTheLibraryOnMutatedCorpus(t *testing.T) {
	compareParseWithLibrary(t, mutatedYAMLTexts(6000, 20261010))
}

// TestLexerMatchesTheLibraryOnMutatedCorpus compares the token stream of the library's Lexer with lex for mutated corpus texts.
func TestLexerMatchesTheLibraryOnMutatedCorpus(t *testing.T) {
	corpus := mutatedYAMLTexts(6000, 20261010)
	var want [][]string
	pioracle.Run(t, `
const { Lexer } = await import(new URL("file://" + root + "/../yaml/index.js").href);
emit(input.map((src) => [...new Lexer().lex(src)]));`, corpus, &want)
	failures := 0
	for i, src := range corpus {
		got := lex(src)
		if !slices.Equal(got, want[i]) {
			if failures++; failures <= 10 {
				t.Errorf("%q:\n got %q\nwant %q", src, got, want[i])
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d texts lexed differently", failures, len(corpus))
	}
}

// TestParseMatchesTheLibraryOnUTF16Offsets compares parse() of eemeli/yaml 2.9.0 with Parse for texts the library reads as UTF-16: offsets, lengths and character counts the library takes in code units while Pig's lexer works on UTF-8 bytes, plus explicitly tagged text the core tags do not resolve and the key normalisation of JavaScript collections.
func TestParseMatchesTheLibraryOnUTF16Offsets(t *testing.T) {
	long := strings.Repeat("k", 1022)
	compareParseWithLibrary(t, []string{
		"a: 1\né  line\nc: 22", "a: 1\n\u2028 d\nc: 22", "- a #  c\n\u2028 d\n-- b", "-  a&# c\n\u00a0 d\n- b",
		"a: \"\\u\u202812\"", "a: \"😀\\ué082\"", "a: \"\\😀u%2028\"", "a: \"\\ud80éè\n  description:0\"", "a: \"\\ud8\u00a000\"-", ":a: \"\\U\u00a0FFFFFFFF\"",
		"? [" + long + "é: v]", "? [" + long + "😀: v]", "[" + long + "😀: v]", "[" + long + "é: v]", long + "😀: v", long + "é: v", long + "éé: v",
		"a: !!binary |\n  aGVsbG8gd29😀bGQ=\n", "a: !!binary aGVs\u00e9bG8=", "a: !!binary aGV😀s",
		"? [&a\u2028 x]\n: v", "a: &a\u00a0 x", "a: &a\ufeff x", "a: &a\u3000 x",
		"!!omap\n- 0: 1\n- -0: 2", "!!set {0, -0}", "!!omap\n- -0: 2",
		"? [!!bool abc, !!null abc, !!int abc, !!float 1 d, !!int 7.5x, !!float 5x, !!int \u00a07, !!float ' 8', !!int 1e1x]\n: v",
		"? [!!int '12', !!float \"1.5\", !!float '1 d', !!int 0x]\n: v", "? [!!int 0o, !!int ' ', !!float '', !!float .inf, !!int abc, !!bool '', !!null x]\n: v",
		"a: 'x\nb", "a: '\n....\n", "a: \"x\n  y", "- 'x\n- y", "a: 1\n\u2028 b\nc: 22",
	})
}

// TestParseMatchesTheLibraryOnErrorRangesPastNonASCII pins the error range of a block scalar used as an implicit key. The library ends it at the token's offset plus the UTF-16 length of the scalar's content, which lies after the header, so the carets under the line count units of the content whatever precedes or follows it; a byte count of the content once printed one caret, or too many, when it held a multi-byte character.
func TestParseMatchesTheLibraryOnErrorRangesPastNonASCII(t *testing.T) {
	compareParseWithLibrary(t, []string{
		"a: 1\n|b: x\ny: [1]\n", "a: 1\n|b: x\nÿ: [1]\n", "a: 1\n|b: x\nÿ: 1\n", "a: 1\n|b: x\né\n", "a: 1\n|b: x\n😀\n", "a: 1\n|b: x\ne\n",
		"name: éè\n|escription: 😀 x\nÿ: [😀, \"😀\"]", ": x\n   y\n|-é\n  x", "a:\n> \u2028&x\n  - b", "é: 1\n|b: x\ny\n", "é: 😀\n|b: 日本\nz: 1\n",
	})
}

// TestParseMatchesTheLibraryOnCollectionTagsOnScalars pins a scalar that names a collection tag. getTagObject picks the tag by name and !!map and !!seq have no stringify, so the scalar goes to stringifyString without the string tag's actualString: a text that would read as a number stays plain in the stringified key (`!!map 1`), where a string scalar is quoted (`"1"`).
func TestParseMatchesTheLibraryOnCollectionTagsOnScalars(t *testing.T) {
	compareParseWithLibrary(t, []string{
		"? [!!map 1]\n: v", "? {a: !!map 1}\n: v", "? [1.10, 1e2: !!map\n  1]\n: v", "? [!!seq 1]\n: v", "? [!!seq true, !!map null, !!map 1.5, !!seq x]\n: v", "? [!!str 1]\n: v", "? [!!int 1, !!float 2, !!bool true, !!null null]\n: v",
	})
}
