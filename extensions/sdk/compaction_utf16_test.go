package sdk

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf16"
)

// jsLength is the length JavaScript reads from the string these bytes decode to: Go's conversion to runes makes each byte that is not UTF-8 one U+FFFD, as the
// text a JSON decoder hands an extension holds it, and utf16.Encode gives the code units of the runes.
func jsLength(text string) int {
	return len(utf16.Encode([]rune(text)))
}

func TestUTF16LengthIsTheUTF16LengthOfTheRunes(t *testing.T) {
	for text, want := range map[string]int{
		"": 0, "a": 1, "abcdefgh": 8, "abcdefghi": 9, "é": 1, "日本語": 3, "😀": 2, "𝒜": 2, "\U0010ffff": 2, "\U0000ffff": 1,
		"abcdefg😀": 9, "abcdefgh😀": 10, "abcdefgh😀abcdefgh": 18, "\xff": 1, "\xf0\x9f\x98": 3, "abcdefgh\xf0\x9f\x98": 11, "\xed\xa0\x80": 3, "\xf4\x90\x80\x80": 4,
	} {
		if got := utf16Length(text); got != want {
			t.Errorf("utf16Length(%q) = %d, want %d", text, got, want)
		}
	}
}

// utf16Fragments are the pieces random texts are built from: ASCII runs of every length around the eight bytes the fast path reads at a time, characters of
// every encoded size, a unit at each end of the surrogate range, and bytes that are not UTF-8.
func utf16Fragments(rng *rand.Rand) string {
	switch rng.IntN(12) {
	case 0, 1, 2, 3:
		return strings.Repeat("a", rng.IntN(20))
	case 4:
		return "é"
	case 5:
		return "日"
	case 6:
		return "😀"
	case 7:
		return "\U0010ffff"
	case 8:
		return "\ud7ff\ue000"
	case 9:
		return string([]byte{byte(0x80 + rng.IntN(0x80))})
	case 10:
		return string([]byte{0xf0, 0x9f, 0x98})
	}
	return string([]byte{0xed, 0xa0, 0x80})
}

func TestUTF16LengthOfRandomTextsMatchesTheUTF16LengthOfTheRunes(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 13))
	for range 50000 {
		var text strings.Builder
		for range rng.IntN(8) {
			text.WriteString(utf16Fragments(rng))
		}
		if got, want := utf16Length(text.String()), jsLength(text.String()); got != want {
			t.Fatalf("utf16Length(%q) = %d, want %d", text.String(), got, want)
		}
	}
}

func FuzzUTF16Length(f *testing.F) {
	for _, seed := range []string{"", "abcdefgh", "abcdefghé", "abcdefgh😀", "\xffabcdefgh", "abcdefg\xf0\x9f\x98", strings.Repeat("a", 64) + "日" + strings.Repeat("b", 9)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if got, want := utf16Length(text), jsLength(text); got != want {
			t.Fatalf("utf16Length(%q) = %d, want %d", text, got, want)
		}
	})
}

func BenchmarkUTF16Length(b *testing.B) {
	for _, text := range []struct{ name, text string }{
		{"ascii64", strings.Repeat("a", 64)},
		{"ascii1k", strings.Repeat("lorem ipsum dolor sit amet ", 40)},
		{"asciiWithOneAccent1k", strings.Repeat("lorem ipsum dolor sit amet ", 20) + "é" + strings.Repeat("lorem ipsum dolor sit amet ", 20)},
		{"cjk1k", strings.Repeat("日本語のテキスト", 100)},
		{"emoji1k", strings.Repeat("😀", 250)},
		{"mixed1k", strings.Repeat("hello é world 😀 ", 60)},
	} {
		b.Run(text.name, func(b *testing.B) {
			b.SetBytes(int64(len(text.text)))
			units := 0
			for b.Loop() {
				units += utf16Length(text.text)
			}
			if units == 0 {
				b.Fatal("no units")
			}
		})
	}
}
