package resolvepath

// pi: packages/coding-agent/src/utils/paths.ts UNICODE_SPACES

import (
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
)

// NormalizeUnicodeSpaces replaces what Pi's UNICODE_SPACES regexp matches and copies every other byte, ill-formed UTF-8 included, as a
// regexp replacement does.
func TestNormalizeUnicodeSpacesMatchesTheRegexpReplacement(t *testing.T) {
	reference := regexp.MustCompile(`[\x{00A0}\x{2000}-\x{200A}\x{202F}\x{205F}\x{3000}]`)
	pieces := []string{"a", "/", " ", "\u00A0", "\u2000", "\u2005", "\u200A", "\u200B", "\u202F", "\u205F", "\u3000", "\u3001", "\u1FFF",
		"\u00A1", "é", "😀", "\xff", "\xe2\x80", "\xe2", "\xc2", "\xc2\xa0\xa0", "\ufffd"}
	cases := []string{"", "plain/path.txt", "\u00A0", "a\u00A0b\u2000c\u200Ad\u202Fe\u205Ff\u3000g", "\xe2\x80\xa0", "\xe2\x80\x8a\xff"}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 5000 {
		var b strings.Builder
		for range rng.IntN(12) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		cases = append(cases, b.String())
	}
	for _, s := range cases {
		if got, want := NormalizeUnicodeSpaces(s), reference.ReplaceAllString(s, " "); got != want {
			t.Fatalf("NormalizeUnicodeSpaces(%q) = %q, want %q", s, got, want)
		}
	}
}
