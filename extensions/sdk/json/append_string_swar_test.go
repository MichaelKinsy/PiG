package json

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"
)

// appendStringBytewise is appendString as it was before the eight-byte scan (go1.27.1 encode.go plus the WTF-8 surrogate branch), with the
// U+2028/U+2029 escape made optional as escapes selects and one replacement per maximal ill-formed subpart (LW-JSON-1). It is the reference
// the scan must equal.
func appendStringBytewise[Bytes []byte | string](dst []byte, src Bytes, escapes stringEscapes) []byte {
	escapeHTML := escapes&escapeHTMLChars != 0
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(src); {
		if b := src[i]; b < utf8.RuneSelf {
			if htmlSafeSet[b] || (!escapeHTML && safeSet[b]) {
				i++
				continue
			}
			dst = append(dst, src[start:i]...)
			switch b {
			case '\\', '"':
				dst = append(dst, '\\', b)
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				dst = append(dst, '\\', 'u', '0', '0', hex[b>>4], hex[b&0xF])
			}
			i++
			start = i
			continue
		}
		if len(src)-i >= 3 && src[i] == 0xed && src[i+1] >= 0xa0 && src[i+1] <= 0xbf && src[i+2] >= 0x80 && src[i+2] <= 0xbf {
			unit := uint16(src[i]&15)<<12 | uint16(src[i+1]&63)<<6 | uint16(src[i+2]&63)
			dst = append(dst, src[start:i]...)
			if unit < 0xdc00 && len(src)-i >= 6 && src[i+3] == 0xed && src[i+4] >= 0xb0 && src[i+4] <= 0xbf && src[i+5] >= 0x80 && src[i+5] <= 0xbf {
				low := uint16(src[i+3]&15)<<12 | uint16(src[i+4]&63)<<6 | uint16(src[i+5]&63)
				dst = utf8.AppendRune(dst, 0x10000+(rune(unit)-0xd800)<<10+rune(low)-0xdc00)
				i += 6
				start = i
				continue
			}
			dst = append(dst, '\\', 'u', hex[unit>>12], hex[(unit>>8)&15], hex[(unit>>4)&15], hex[unit&15])
			i += 3
			start = i
			continue
		}
		n := min(len(src)-i, utf8.UTFMax)
		c, size := utf8.DecodeRuneInString(string(src[i : i+n]))
		if c == utf8.RuneError && size == 1 {
			dst = append(dst, src[start:i]...)
			if escapes&escapeReplacement != 0 {
				dst = append(dst, `\ufffd`...)
			} else {
				dst = append(dst, "\ufffd"...)
			}
			i += maximalSubpart(src, i)
			start = i
			continue
		}
		if escapes&escapeLineSeparators != 0 && (c == '\u2028' || c == '\u2029') {
			dst = append(dst, src[start:i]...)
			dst = append(dst, '\\', 'u', '2', '0', '2', hex[c&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	dst = append(dst, src[start:]...)
	dst = append(dst, '"')
	return dst
}

var swarPieces = []string{
	"a", "Z", " ", "<", ">", "&", "'", "/", "\x7f", "\"", "\\", "\b", "\f", "\n", "\r", "\t", "\x00", "\x01", "\x1f", "\x20",
	"é", "€", "😀", "\u2028", "\u2029", "\ufffd", "\u0080", "\u07ff", "\uffff",
	"\xed\xa0\x80", "\xed\xb0\x80", "\xed\xa0\xbd\xed\xb8\x80", "\xed\x9f\xbf",
	"\xff", "\xe2\x82", "\xc0\xaf", "\xf0\x9f\x98", "\x80", "\xf4\x90\x80\x80",
	"plain ascii text run ", "0123456", "01234567", "012345678",
}

// The eight-byte scan writes exactly what the byte-at-a-time encoder wrote, in every escape mode, for string and []byte input, on 200,000
// strings over every byte class the encoder branches on, at every offset of the 8-byte blocks.
func TestAppendStringMatchesBytewise(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	modes := []stringEscapes{0, escapeHTMLChars, escapeLineSeparators | escapeReplacement, escapeHTMLChars | escapeLineSeparators | escapeReplacement}
	for i := range 200000 {
		var b strings.Builder
		for range rng.IntN(40) {
			b.WriteString(swarPieces[rng.IntN(len(swarPieces))])
		}
		s := b.String()
		mode := modes[i%len(modes)]
		want := string(appendStringBytewise([]byte("prefix"), s, mode))
		if got := string(appendString([]byte("prefix"), s, mode)); got != want {
			t.Fatalf("case %d mode %d %q:\n got %s\nwant %s", i, mode, s, got, want)
		}
		if got := string(appendString([]byte("prefix"), []byte(s), mode)); got != want {
			t.Fatalf("case %d mode %d []byte %q:\n got %s\nwant %s", i, mode, s, got, want)
		}
	}
}
