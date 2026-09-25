package widthx

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// graphemeIterPieces mixes ASCII with every cluster rule that can join across
// an ASCII neighbour or depends on prior state: combining marks, ZWJ emoji,
// emoji presentation/text selectors, keycaps, regional-indicator runs of odd
// and even length, Hangul jamo, Thai/Lao AM, prepend marks, CR LF, controls,
// wide CJK and invalid UTF-8.
var graphemeIterPieces = []string{
	"a", "Z", " ", "~", "#", "*", "1", "\t", "\r", "\n", "\r\n", "\x00", "\x7f", "\x1b",
	"e\u0301", "\u0301", "\u0308", "\u200d", "\u200b", "\ufe0f", "\ufe0e",
	"\u26a0", "\u26a0\ufe0f", "\u2764\ufe0f", "#\ufe0f\u20e3", "1\u20e3",
	"\U0001F468\u200d\U0001F469\u200d\U0001F467", "\U0001F44D\U0001F3FD", "\U0001F3F3\ufe0f\u200d\U0001F308",
	"\U0001F1FA", "\U0001F1F8", "\U0001F1FA\U0001F1F8", "\U0001F1EF\U0001F1F5\U0001F1EB", "\U0001F1E6\U0001F1E6\U0001F1E6",
	"\u1100", "\u1161", "\u11a8", "\ud55c", "\u0e01\u0e33", "\u0e33", "\u0eb3", "\u0e01",
	"\u0600", "\u0915\u094d\u0937", "\u0903", "\u4e2d", "\u6587", "\uff71", "\xff", "\xe2\x82",
}

// assertGraphemeIterMatchesUniseg checks the iterator's ASCII fast path
// against the reference segmenter (FirstGrapheme + GraphemeWidth), which
// pi_width_diff_test.go holds to upstream Intl.Segmenter + graphemeWidth.
func assertGraphemeIterMatchesUniseg(t *testing.T, s string) {
	t.Helper()
	rest := s
	got := newGraphemeIter(s)
	for i := 0; ; i++ {
		wantOK := rest != ""
		gotOK := got.Next()
		if wantOK != gotOK {
			t.Fatalf("%q cluster %d: Next = %v, reference %v", s, i, gotOK, wantOK)
		}
		if !wantOK {
			return
		}
		var cluster string
		cluster, rest = FirstGrapheme(rest)
		if got.Str() != cluster || got.Width() != GraphemeWidth(cluster) {
			t.Fatalf("%q cluster %d: got (%q, %d), reference (%q, %d)", s, i, got.Str(), got.Width(), cluster, GraphemeWidth(cluster))
		}
	}
}

func TestGraphemeIterMatchesUniseg(t *testing.T) {
	assertGraphemeIterMatchesUniseg(t, "")
	for _, a := range graphemeIterPieces {
		assertGraphemeIterMatchesUniseg(t, a)
		for _, b := range graphemeIterPieces {
			assertGraphemeIterMatchesUniseg(t, a+b)
			assertGraphemeIterMatchesUniseg(t, "x"+a+"y"+b+"z")
		}
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 20000 {
		var b strings.Builder
		for range 1 + rng.IntN(12) {
			b.WriteString(graphemeIterPieces[rng.IntN(len(graphemeIterPieces))])
		}
		assertGraphemeIterMatchesUniseg(t, b.String())
	}
}

func FuzzGraphemeIterMatchesUniseg(f *testing.F) {
	for _, piece := range graphemeIterPieces {
		f.Add("ab" + piece + "c")
	}
	f.Fuzz(assertGraphemeIterMatchesUniseg)
}
