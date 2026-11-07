package localecompare

import (
	"bytes"
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"

	xcollate "golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// xtextCompare is what Compare was before the collation tables were trimmed to the root locale: golang.org/x/text's collator for language.Und,
// with the U+FFFD rewrite. The tests below keep Compare equal to it for every input, not close to it.
type xtextCompare struct{ c *xcollate.Collator }

func newXTextCompare() xtextCompare { return xtextCompare{xcollate.New(language.Und)} }

func (x xtextCompare) compare(a, b string) int {
	if !strings.Contains(a, "\uFFFD") && !strings.Contains(b, "\uFFFD") {
		return x.c.CompareString(a, b)
	}
	return x.c.CompareString(xtextTokens.Replace(a), xtextTokens.Replace(b))
}

var xtextTokens = strings.NewReplacer("\uFFFD", "\uFFFF\t", "\uFFFF", "\uFFFF\uFFFF")

func encodeWTF8(r rune) string {
	if r >= 0xD800 && r <= 0xDFFF {
		return string([]byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F})
	}
	return string(utf8.AppendRune(nil, r))
}

// Every code point, lone surrogates included, compares with a set of anchors as x/text's collator does, from both sides.
func TestCompareEqualsXTextForEveryCodePoint(t *testing.T) {
	anchors := []string{"", "a", "A", "z", "-", " ", "1", "é", "\u0301", "\uFFFD", "\uFFFF", "\uFFFE", "\U0010FFFF", "\u4e00", "\uac00", "\u1100", "😀", "\xed\xa0\x80", "a\uFFFD", "\uFFFDa", "\uFFFF\t"}
	old, c := newXTextCompare(), NewCollator()
	for r := rune(0); r <= utf8.MaxRune; r++ {
		s := encodeWTF8(r)
		for _, a := range anchors {
			if got, want := c.CompareString(s, a), old.compare(s, a); got != want {
				t.Fatalf("CompareString(%q, %q) = %d, x/text %d", s, a, got, want)
			}
			if got, want := c.CompareString(a, s), old.compare(a, s); got != want {
				t.Fatalf("CompareString(%q, %q) = %d, x/text %d", a, s, got, want)
			}
		}
		if got, want := c.CompareString(s, s), old.compare(s, s); got != want {
			t.Fatalf("CompareString(%q, %q) = %d, x/text %d", s, s, got, want)
		}
	}
}

// Random strings, those that hold U+FFFD or U+FFFF among them, compare as x/text's collator does.
func TestCompareEqualsXTextForRandomStrings(t *testing.T) {
	pieces := []string{
		"a", "A", "b", "B", "z", "0", "1", " ", "-", "_", ".", "/", "\t", "\x00", "é", "e\u0301", "ß", "ss", "ä", "a\u0308", "ǅ", "ﬁ", "\u00ad", "\u200b", "ch", "l·l", "ŀ",
		"\uFFFD", "\uFFFD", "\uFFFF", "\uFFFE", "\U0010FFFF", "\U0010FFFE", "\u0378", "\ue000", "\xed\xa0\x80", "\xff", "\xc0\x80", "\xe2\x82",
		"日本語", "中", "汉", "한", "각", "ᄀ", "α", "я", "😀", "👍🏽", "\u0301", "\u0323", "\u0334", "\U00020000", "\uF900",
	}
	rng := rand.New(rand.NewPCG(20261106, 2))
	random := func() string {
		var b strings.Builder
		n := rng.IntN(7)
		if rng.IntN(100) == 0 {
			n = 700 + rng.IntN(900)
		}
		for range n {
			if rng.IntN(6) == 0 {
				b.WriteString(encodeWTF8(rune(rng.IntN(utf8.MaxRune + 1))))
			} else {
				b.WriteString(pieces[rng.IntN(len(pieces))])
			}
		}
		return b.String()
	}
	old, c := newXTextCompare(), NewCollator()
	strs := make([]string, 3000)
	for i := range strs {
		strs[i] = random()
	}
	for range 300000 {
		a, b := strs[rng.IntN(len(strs))], strs[rng.IntN(len(strs))]
		if got, want := c.CompareString(a, b), old.compare(a, b); got != want {
			t.Fatalf("CompareString(%q, %q) = %d, x/text %d", a, b, got, want)
		}
		if got, want := Compare(a, b), old.compare(a, b); got != want {
			t.Fatalf("Compare(%q, %q) = %d, x/text %d", a, b, got, want)
		}
	}
	// A key is x/text's key: ls orders names by it.
	var buf xcollate.Buffer
	ref := xcollate.New(language.Und)
	for _, s := range strs {
		buf.Reset()
		if got, want := c.AppendKey(nil, s), ref.KeyFromString(&buf, s); !bytes.Equal(got, want) {
			t.Fatalf("AppendKey(%q) = % x, x/text % x", s, got, want)
		}
	}
}
