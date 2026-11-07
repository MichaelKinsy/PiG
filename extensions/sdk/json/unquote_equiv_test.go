package json

import (
	"bytes"
	"testing"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// unquoteBytesReference is unquoteBytes as it was before the string scan went eight bytes at a time: the rune-by-rune version every
// quoted string decoded through. TestUnquoteMatchesReference and FuzzUnquoteMatchesReference hold unquote and unquoteBytes to it.
func unquoteBytesReference(s []byte) (t []byte, ok bool) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return
	}
	s = s[1 : len(s)-1]

	// Check for unusual characters. If there are none,
	// then no unquoting is needed, so return a slice of the
	// original bytes.
	r := 0
	for r < len(s) {
		c := s[r]
		if c == '\\' || c == '"' || c < ' ' {
			break
		}
		rr, size := utf8.DecodeRune(s[r:])
		if rr == utf8.RuneError && size == 1 {
			if isWTF8Surrogate(s[r:]) {
				// pig: a lone surrogate in internal/jsstring's WTF-8 form is one JavaScript string unit, kept as is.
				r += 3
				continue
			}
			break
		}
		r += size
	}
	if r == len(s) {
		return s, true
	}

	b := make([]byte, addSize(len(s), 2*utf8.UTFMax))
	w := copy(b, s[0:r])
	for r < len(s) {
		// Out of room? Can only happen if s is full of
		// malformed UTF-8 and we're replacing each
		// byte with RuneError.
		if w >= len(b)-2*utf8.UTFMax {
			nb := make([]byte, mulSize(addSize(len(b), utf8.UTFMax), 2))
			copy(nb, b[0:w])
			b = nb
		}
		switch c := s[r]; {
		case c == '\\':
			r++
			if r >= len(s) {
				return
			}
			switch s[r] {
			default:
				return
			case '"', '\\', '/', '\'':
				b[w] = s[r]
				r++
				w++
			case 'b':
				b[w] = '\b'
				r++
				w++
			case 'f':
				b[w] = '\f'
				r++
				w++
			case 'n':
				b[w] = '\n'
				r++
				w++
			case 'r':
				b[w] = '\r'
				r++
				w++
			case 't':
				b[w] = '\t'
				r++
				w++
			case 'u':
				r--
				rr := getu4(s[r:])
				if rr < 0 {
					return
				}
				r += 6
				if utf16.IsSurrogate(rr) {
					rr1 := getu4(s[r:])
					if dec := utf16.DecodeRune(rr, rr1); dec != unicode.ReplacementChar {
						// A valid pair; consume.
						r += 6
						w += utf8.EncodeRune(b[w:], dec)
						break
					}
					// Match internal/jsstring's WTF-8 convention without converting the rest of the string.
					b[w] = byte(0xe0 | rr>>12)
					b[w+1] = byte(0x80 | (rr>>6)&63)
					b[w+2] = byte(0x80 | rr&63)
					w += 3
					break
				}
				w += utf8.EncodeRune(b[w:], rr)
			}

		// Quote, control characters are invalid.
		case c == '"', c < ' ':
			return

		// ASCII
		case c < utf8.RuneSelf:
			b[w] = c
			r++
			w++

		// pig: keep a lone surrogate in WTF-8 form (one JavaScript string unit); coerce anything else to well-formed UTF-8.
		case isWTF8Surrogate(s[r:]):
			w += copy(b[w:], s[r:r+3])
			r += 3

		// Coerce to well-formed UTF-8.
		default:
			rr, size := utf8.DecodeRune(s[r:])
			r += size
			w += utf8.EncodeRune(b[w:], rr)
		}
	}
	return b[0:w], true
}

var unquoteCases = []string{
	``, `a`, `hello world`, `a\nb`, `\"`, `\\`, `\/`, `\b\f\n\r\t`, `\u0041`, `\u00e9`, `\u00E9x`, `\u20ac`, `\ud83d\ude00`, `\ud83d`, `\ud83dx`, `\ude00`, `\ud83d\u0041`, `\ud800\ud800`,
	`\u003c\u003e\u0026`, `é`, `日本語`, `😀`, "a\xffb", "\xff", "\xc3", "\xc3(", "\xe2\x82", "\xed\xa0\x80", "\xed\xa0\x80x", "a\xed\xbf\xbfb", "\xef\xbf\xbd", "\xf4\x90\x80\x80",
	"\x7f", "tab\there", "ctl\x01", "\x1f", `\a`, `\u00`, `\uzzzz`, `\`, "q\"q", `\'`,
	"the quick brown fox jumps over the lazy dog, the quick brown fox jumps over the lazy dog \\n and more \\u00e9",
	"0123456789abcdef0123456789abcdef\n0123456789abcdef0123456789abcdef é 0123456789abcdef\xff0123456789",
}

func checkUnquote(t *testing.T, inner string) {
	t.Helper()
	quoted := []byte(`"` + inner + `"`)
	want, wantOK := unquoteBytesReference(quoted)
	got, gotOK := unquoteBytes(quoted)
	if wantOK != gotOK || (wantOK && !bytes.Equal(want, got)) {
		t.Fatalf("%q: reference %q %v, unquoteBytes %q %v", inner, want, wantOK, got, gotOK)
	}
	str, strOK := unquote(quoted)
	if wantOK != strOK || (wantOK && str != string(want)) {
		t.Fatalf("%q: reference %q %v, unquote %q %v", inner, want, wantOK, str, strOK)
	}
}

func TestUnquoteMatchesReference(t *testing.T) {
	for _, inner := range unquoteCases {
		checkUnquote(t, inner)
	}
	// Each case at every offset of a run of ASCII, so the eight-byte scan meets it at every lane.
	for _, inner := range unquoteCases {
		for pad := range 17 {
			prefix := string(bytes.Repeat([]byte("x"), pad))
			checkUnquote(t, prefix+inner)
			checkUnquote(t, inner+prefix)
		}
	}
	for _, bad := range []string{``, `"`, `a`, `"a`, `a"`} {
		got, ok := unquoteBytes([]byte(bad))
		want, wantOK := unquoteBytesReference([]byte(bad))
		if ok != wantOK || !bytes.Equal(got, want) {
			t.Fatalf("%q: reference %q %v, got %q %v", bad, want, wantOK, got, ok)
		}
	}
}

func FuzzUnquoteMatchesReference(f *testing.F) {
	for _, inner := range unquoteCases {
		f.Add([]byte(inner))
	}
	f.Fuzz(func(t *testing.T, inner []byte) {
		checkUnquote(t, string(inner))
	})
}
