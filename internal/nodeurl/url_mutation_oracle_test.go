package nodeurl

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestParseMatchesNodeURLOnMutatedInputs compares ParseURL with Node's URL over mutated parseInputs: URL delimiters, percent escapes, brackets, backslashes, whitespace, control characters, dots, digits and non-ASCII letters inserted, deleted, replaced and spliced between inputs.
func TestParseMatchesNodeURLOnMutatedInputs(t *testing.T) {
	corpus := parseInputs()
	rng := rand.New(rand.NewSource(20261010))
	markers := []string{":", "/", "//", "///", "\\", "@", "?", "#", "%", "%2e", "%41", "%zz", "[", "]", "[::1]", ".", "..", "/..", "/./", "0x", "1", "255", "65535", "99999", " ", "\t", "\n", "\x00", "\x01", "\x7f", "é", "ü", "日", "😀", "xn--", "ß", "İ", "ﬀ", "\u00ad", "\u200b", "\u3002", "\uff0e", "<", ">", "\"", "'", "`", "{", "}", "|", "^", "~", ";", "&", "=", "+", ",", "file:", "http:", "ws:", "localhost", "a", "A"}
	mutate := func(src string) string {
		runes := []rune(src)
		for range 1 + rng.Intn(3) {
			at := 0
			if len(runes) > 0 {
				at = rng.Intn(len(runes) + 1)
			}
			switch rng.Intn(5) {
			case 0, 1:
				runes = append(runes[:at], append([]rune(markers[rng.Intn(len(markers))]), runes[at:]...)...)
			case 2:
				if at < len(runes) {
					runes = append(runes[:at], runes[at+1:]...)
				}
			case 3:
				if at < len(runes) {
					runes[at], _ = utf8.DecodeRuneInString(markers[rng.Intn(len(markers))])
				}
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
	inputs := make([]string, 0, 6000)
	for len(inputs) < cap(inputs) {
		inputs = append(inputs, mutate(corpus[rng.Intn(len(corpus))]))
	}
	if !strings.Contains(strings.Join(inputs, ""), "http") {
		t.Fatal("mutation lost the corpus")
	}
	compareParseWithNode(t, inputs)
}
