package collate

import (
	"bytes"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"

	xcollate "golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/third_party/collate/tablegen"
)

// reference is golang.org/x/text/collate for the root locale: the collator this package replaces. Every test below holds the fork to its result.
type reference struct {
	c   *xcollate.Collator
	buf xcollate.Buffer
}

func newReference() *reference { return &reference{c: xcollate.New(language.Und)} }

func (r *reference) key(s string) []byte {
	r.buf.Reset()
	return r.c.KeyFromString(&r.buf, s)
}

// encode is utf8.AppendRune that also encodes the surrogates (WTF-8, as JavaScript strings with lone surrogates reach Go).
func encode(r rune) string {
	if r >= 0xD800 && r <= 0xDFFF {
		return string([]byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F})
	}
	return string(utf8.AppendRune(nil, r))
}

type pair struct {
	ref *reference
	c   *Collator
}

func newPair() pair { return pair{newReference(), New()} }

func (p pair) checkKey(t *testing.T, s string) {
	t.Helper()
	if got, want := p.c.AppendKey(nil, s), p.ref.key(s); !bytes.Equal(got, want) {
		t.Fatalf("AppendKey(%q) = % x, x/text % x", s, got, want)
	}
}

func (p pair) checkCompare(t *testing.T, a, b string) {
	t.Helper()
	if got, want := p.c.CompareString(a, b), p.ref.c.CompareString(a, b); got != want {
		t.Fatalf("CompareString(%q, %q) = %d, x/text %d", a, b, got, want)
	}
}

// Every code point alone has the weights x/text gives it, surrogates included; so does the code point before a combining acute accent and a
// combining dot below (different combining classes, which reorder the weights and match discontiguous contractions), and a few ASCII
// neighbours.
func TestEveryCodePointWeighsAsXTextDoes(t *testing.T) {
	p := newPair()
	for r := rune(0); r <= utf8.MaxRune; r++ {
		s := encode(r)
		p.checkKey(t, s)
		if r < 0x30000 || r >= 0xE0000 && r < 0xE0200 {
			p.checkKey(t, s+"\u0301")
			p.checkKey(t, s+"\u0323\u0301")
			p.checkKey(t, "a"+s+"\u0323")
		}
	}
}

// The trie reads bytes: invalid, truncated and overlong sequences, which valid text never holds but a file name may, weigh as x/text weighs them.
func TestEveryByteSequenceWeighsAsXTextDoes(t *testing.T) {
	p := newPair()
	for c0 := 0x80; c0 <= 0xFF; c0++ {
		p.checkKey(t, string([]byte{byte(c0)}))
		for c1 := 0; c1 <= 0xFF; c1++ {
			p.checkKey(t, string([]byte{byte(c0), byte(c1)}))
		}
	}
	for c0 := 0xE0; c0 <= 0xF7; c0++ {
		for _, c1 := range append([]int{0x7F, 0xC0}, seq(0x80, 0xC0)...) {
			for c2 := 0; c2 <= 0xFF; c2++ {
				p.checkKey(t, string([]byte{byte(c0), byte(c1), byte(c2)}))
			}
		}
	}
	for c0 := 0xF0; c0 <= 0xF7; c0++ {
		for c1 := 0x80; c1 < 0xC0; c1++ {
			for c2 := 0x80; c2 < 0xC0; c2++ {
				for c3 := 0x80; c3 < 0xC0; c3++ {
					p.checkKey(t, string([]byte{byte(c0), byte(c1), byte(c2), byte(c3)}))
				}
				p.checkKey(t, string([]byte{byte(c0), byte(c1), byte(c2), 0x7F}))
				p.checkKey(t, string([]byte{byte(c0), byte(c1), byte(c2), 0xC0}))
			}
		}
	}
}

func seq(from, to int) []int {
	var out []int
	for i := from; i < to; i++ {
		out = append(out, i)
	}
	return out
}

// contractionStrings lists every string a contraction of the table matches, by walking each contraction trie from the code points that start one.
func contractionStrings() []string {
	var out []string
	for r := rune(0); r <= utf8.MaxRune; r++ {
		start := encode(r)
		ce, sz := lookupString(start)
		if sz != len(start) || ce.ctype() != ceContractionIndex {
			continue
		}
		index, n, _ := splitContractIndex(ce)
		var walk func(prefix string, base, n int)
		walk = func(prefix string, base, n int) {
			for _, e := range contractionTries[base : base+n] {
				if e.N == final {
					for c := int(e.L); c <= int(e.H); c++ {
						out = append(out, prefix+string([]byte{byte(c)}))
					}
					continue
				}
				next := prefix + string([]byte{e.L})
				out = append(out, next)
				walk(next, base+int(e.H)+n, int(e.N))
			}
		}
		out = append(out, start)
		walk(start, index, n)
	}
	return out
}

func TestContractionsWeighAsXTextDoes(t *testing.T) {
	p := newPair()
	all := contractionStrings()
	if len(all) < 200 {
		t.Fatalf("only %d contraction strings: the walk of the contraction tries is broken", len(all))
	}
	for _, s := range all {
		p.checkKey(t, s)
		p.checkKey(t, s+"\u0301")
		p.checkKey(t, s+"b")
		// A combining mark between the code points of a contraction does not block it when its combining class is higher.
		if _, size := utf8.DecodeRuneInString(s); size < len(s) {
			for _, mark := range []string{"\u0301", "\u0323", "\u0334", "\u035C", "\u0F71"} {
				p.checkKey(t, s[:size]+mark+s[size:])
			}
		}
	}
	for i, a := range all {
		p.checkCompare(t, a, all[(i*7+1)%len(all)])
		p.checkCompare(t, a+"x", all[(i*13+5)%len(all)]+"y")
	}
}

// randomString builds strings from every kind of input a name or an identifier holds, and from the kinds only collation tables hold:
// contractions, combining runs longer than the 30 the iterator reorders, strings longer than its 512-element buffer, Hangul, Han, emoji
// sequences, noncharacters, lone surrogates and invalid bytes.
func randomString(rng *rand.Rand, pieces []string) string {
	var b strings.Builder
	n := rng.IntN(7)
	switch rng.IntN(60) {
	case 0:
		n = 600 + rng.IntN(1500)
	case 1:
		b.WriteString("e")
		for range 25 + rng.IntN(40) {
			b.WriteString(encode(rune(0x300 + rng.IntN(0x70))))
		}
	}
	for range n {
		switch rng.IntN(8) {
		case 0:
			b.WriteString(encode(rune(rng.IntN(utf8.MaxRune + 1))))
		case 1:
			b.WriteString(encode(rune(rng.IntN(0x3000))))
		case 2:
			b.WriteString(encode(rune(0xAC00 + rng.IntN(0x2BA4)))) // Hangul syllables and a few after
		default:
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
	}
	return b.String()
}

func TestRandomStringsCompareAsXTextDoes(t *testing.T) {
	p := newPair()
	pieces := []string{
		"a", "A", "b", "B", "z", "Z", "0", "1", "9", " ", "-", "_", ".", "/", "~", "#", "\t", "\x00", "\x7f", "é", "e\u0301", "É", "ß", "ss", "æ", "Æ", "ø", "o\u0338", "ǅ", "ǆ", "ﬁ", "fi", "ı", "İ", "i\u0307",
		"\u00ad", "\u200b", "\u200d", "\ufeff", "\uFFFD", "\uFFFF", "\uFFFE", "\U0010FFFF", "\U0001FFFE", "\u0378", "\ue000", "\xed\xa0\x80", "\xed\xaf\xbf", "\xed\xb0\x80", "\xed\xbf\xbf", "\xff", "\xc0\x80", "\xe2\x82", "\xf0\x9f\x98",
		"日", "本", "語", "中", "文", "汉", "字", "漢", "\u3400", "\u9fff", "\U00020000", "\U0002FA1D", "\uF900", "ひ", "カ", "ｶ", "한", "각", "ᄀ", "ᅡ", "ᆨ", "α", "Ω", "ω", "я", "Я", "ا", "א", "😀", "👍🏽", "👨\u200d👩\u200d👧", "🇯🇵",
		"\u0300", "\u0301", "\u0308", "\u0323", "\u0334", "\u0345", "\u064b", "\u0e49", "\u3099", "ａ", "Ａ", "①", "½", "²", "㎏", "ǰ", "\u1e9b\u0323", "\u0f73", "\u0f71\u0f72",
	}
	pieces = append(pieces, contractionStrings()...)
	rng := rand.New(rand.NewPCG(20261106, 1))
	strs := make([]string, 4000)
	for i := range strs {
		strs[i] = randomString(rng, pieces)
		p.checkKey(t, strs[i])
	}
	for range 400000 {
		a, b := strs[rng.IntN(len(strs))], strs[rng.IntN(len(strs))]
		p.checkCompare(t, a, b)
	}
	// Near-equal pairs: a string against itself with one rune changed, the same string in two cases or two normalisation forms.
	for i := range 40000 {
		a := strs[i%len(strs)]
		p.checkCompare(t, a, a)
		p.checkCompare(t, a, a+"\u0301")
		p.checkCompare(t, a+"a", a+"A")
		p.checkCompare(t, a+"e\u0301", a+"\u00e9")
		p.checkCompare(t, a+"\u1100\u1161\u11a8", a+"\uac01")
	}
	p.checkCompare(t, "", "")
	p.checkCompare(t, "", "a")
	p.checkCompare(t, "a", "")
}

// tables.go is what tablegen derives from the x/text version go.mod requires; a different x/text version or a hand edit fails here.
func TestTablesMatchXText(t *testing.T) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}} {{.Version}}", "golang.org/x/text").Output()
	if err != nil {
		t.Fatal(err)
	}
	dir, version, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	source, err := os.ReadFile(dir + "/collate/tables.go")
	if err != nil {
		t.Fatal(err)
	}
	have, err := os.ReadFile("tables.go")
	if err != nil {
		t.Fatal(err)
	}
	want, err := tablegen.Generate(source, "golang.org/x/text@"+version+"/collate/tables.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(have, want) {
		t.Fatalf("tables.go differs from the tables tablegen derives from golang.org/x/text@%s: run `go generate ./third_party/collate`", version)
	}
}
