package nodeurl

import (
	"math/rand"
	"strings"
	"testing"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// composedURLs assembles n strings from the tokens the WHATWG URL parser branches on: schemes, slashes and backslashes, userinfo, hosts (names, IDN, IPv4 in every radix, IPv6), ports at and past the limits, dot segments, percent escapes, forbidden code points, C0 controls, whitespace and non-ASCII.
func composedURLs(n int, seed int64) []string {
	rng := rand.New(rand.NewSource(seed))
	pieces := []string{
		"http:", "https:", "HTTP:", "ws:", "wss:", "ftp:", "file:", "x:", "git+ssh:", "a.b-c+d:", "javascript:", "data:", "mailto:", ":", "1x:",
		"//", "//", "/", "/", "/", "\\", "\\\\", "///", "@", "@", ":", ":", "?", "#", "[", "]", "::1", "fe80::1%25eth0", "[::1]", "[::ffff:1.2.3.4]", "[1:2:3:4:5:6:7:8]", "[::", "[zz]",
		"host", "Host.Example", "localhost", "example.com", "example.com.", "127.0.0.1", "0x7f.1", "1.2.3", "1.2.3.256", "4294967295", "4294967296", "0177.0.0.1", "08.1.1.1", "1e3", "münchen.de", "xn--", "XN--", "ｘｎ--", "xn--a", "xn--é", "\u00ad", "\ufe0f", "\u034f", "\u200d", "xn--mnchen-3ya.de", "ex%41mple", "%zz", "a%2Fb", "%2e", "%2E%2e", "..", ".", "...",
		"user", "pw", "u:p", "0", "21", "80", "443", "65535", "65536", "99999", "-1", "00080", "p a", "a b", "a<b", "a^b", "a|b", "a%00b", "a\x01b", "a\tb", "a\nb", "\r", " ", "  ", "\u00a0", "\u2028", "\ufeff", "é", "😀", "\ud7ff", "日本", "ß", "İ", "\u3002", "\uff0e", "\uff0f", "\u0000",
		"'", "\"", "`", "{", "}", "<", ">", "~", "!", "$", "&", "=", "+", ";", ",", "*", "(", ")", "%", "%41", "%", "q=1", "code=a%20b",
	}
	out := make([]string, n)
	for i := range out {
		var b strings.Builder
		for range 1 + rng.Intn(10) {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		out[i] = b.String()
	}
	return out
}

// TestParseMatchesNodeURLOnComposedInputs compares ParseURL with Node's `new URL` for random compositions of URL syntax tokens.
func TestParseMatchesNodeURLOnComposedInputs(t *testing.T) {
	compareParseWithNode(t, composedURLs(20000, 20261011))
}

// TestParseMatchesNodeURLOnInvalidPunycodeLabels pins the hosts the composed corpus found: a label that is only "xn--" after UTS #46 mapping (separators, ignored code points, compatibility forms), and an "xn--" label holding non-ASCII text.
func TestParseMatchesNodeURLOnInvalidPunycodeLabels(t *testing.T) {
	compareParseWithNode(t, []string{
		"ws:xn--。", "ws:。xn--", "https:xn--．/*", "wss:xn--｡/x", "https://..\ufeffxn--#", "https://xn--\u00ad/", "https://a.xn--\ufe0f.b/", "https://XN--/", "https://ｘｎ--/",
		"https:xn--4294967296日本xn--", "https://xn--é/", "https://a.xn--日本/", "https://a.\u3164xn--/", "https://a.x\u17b4n--/", "https://a.\u115fxn--/", "https://a.xn-\uffa0-/", "https://a.\u1160x\u17b5n--/", "https://a.x\u034fn--/", "https://xn--a/", "https://xn--mnchen-3ya.de/", "https://xn--/", "https://a.xn--.b/", "https://xn--\u200d/",
	})
}

// TestInvalidPunycodeLabelCheckDropsEveryCodePointTheMappingIgnores keeps the label check's ignored set in step with the x/net/idna mapping: a code point the mapping removes must not hide an "xn--" label from the check.
func TestInvalidPunycodeLabelCheckDropsEveryCodePointTheMappingIgnores(t *testing.T) {
	for r := rune(utf8.RuneSelf); r <= unicode.MaxRune; r++ {
		if utf16.IsSurrogate(r) {
			continue
		}
		if ascii, err := whatwgDomainToASCII.ToASCII("a" + string(r) + "b"); err == nil && ascii == "ab" && dropIgnoredCodePoint(r) != -1 {
			t.Errorf("the mapping ignores %U but the label check keeps it", r)
		}
	}
}
