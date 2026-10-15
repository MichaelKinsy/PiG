package jv

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// bytewiseAppendString is AppendString as it was before it skipped plain runs eight bytes at a time: one byte per step, each multi-byte
// sequence classified on its own. TestAppendStringMatchesBytewise and TestAppendString200KBudget compare against it.
func bytewiseAppendString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x80 {
			if c >= 0x20 && c != '"' && c != '\\' {
				i++
				continue
			}
			dst = append(dst, s[start:i]...)
			switch c {
			case '"':
				dst = append(dst, '\\', '"')
			case '\\':
				dst = append(dst, '\\', '\\')
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
				dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&15])
			}
			i++
			start = i
			continue
		}
		size, kind := decodeMulti(s, i)
		switch kind {
		case multiOK:
			i += size
		case multiSurrogate:
			dst = append(dst, s[start:i]...)
			unit := surrogateUnit(s, i)
			if unit < 0xDC00 && i+6 <= len(s) {
				// A high unit followed by a low unit is one code point in a JavaScript string, which prints as itself.
				if size, kind := decodeMulti(s, i+3); size == 3 && kind == multiSurrogate {
					if next := surrogateUnit(s, i+3); next >= 0xDC00 {
						r := 0x10000 + (rune(unit)-0xD800)<<10 + rune(next) - 0xDC00
						dst = append(dst, byte(0xF0|r>>18), byte(0x80|r>>12&0x3F), byte(0x80|r>>6&0x3F), byte(0x80|r&0x3F))
						i += 6
						start = i
						continue
					}
				}
			}
			dst = append(dst, '\\', 'u', hexDigits[unit>>12], hexDigits[unit>>8&15], hexDigits[unit>>4&15], hexDigits[unit&15])
			i += 3
			start = i
		default: // ill-formed bytes: a decoded JS string holds one U+FFFD for each maximal subpart (WHATWG UTF-8 decode, as Node's TextDecoder and Buffer)
			dst = append(dst, s[start:i]...)
			dst = append(dst, 0xEF, 0xBF, 0xBD)
			i += maximalSubpart(s, i)
			start = i
		}
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

// The eight-byte scan writes exactly what the byte-at-a-time loop writes, on 200,000 strings over every byte class (C0 controls, '"', '\\',
// HTML characters, U+007F, multi-byte characters, U+2028/U+2029, a literal U+FFFD, WTF-8 lone and split surrogates, ill-formed bytes)
// placed at every offset of the 8-byte blocks, and after a prefix in dst with or without spare capacity.
func TestAppendStringMatchesBytewise(t *testing.T) {
	pieces := []string{
		"a", "Z", " ", "<", ">", "&", "'", "/", "\x7f", "\"", "\\", "\b", "\f", "\n", "\r", "\t", "\x00", "\x01", "\x1f", "\x20",
		"é", "€", "😀", "\u2028", "\u2029", "\ufffd", "\u0080", "\u07ff", "\uffff",
		"\xed\xa0\x80", "\xed\xb0\x80", "\xed\xa0\xbd\xed\xb8\x80", "\xed\x9f\xbf",
		"\xff", "\xe2\x82", "\xc0\xaf", "\xf0\x9f\x98", "\x80", "\xf4\x90\x80\x80",
		"plain ascii text run ", "0123456", "01234567", "012345678",
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 200000 {
		var b strings.Builder
		for range rng.IntN(40) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		s := b.String()
		prefix := []byte("prefix")
		if i%2 == 0 {
			prefix = append(make([]byte, 0, 64), prefix...)
		}
		want := string(bytewiseAppendString([]byte("prefix"), s))
		if got := string(AppendString(prefix, s)); got != want {
			t.Fatalf("case %d %q:\n got %s\nwant %s", i, s, got, want)
		}
	}
}
