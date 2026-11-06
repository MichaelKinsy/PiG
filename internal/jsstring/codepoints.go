package jsstring

import "unicode/utf8"

// A Go string holds a lone UTF-16 surrogate as the three WTF-8 bytes of its code value (ED A0 80 to ED BF BF), as JavaScript holds it as one unit. Code that
// matches on code points (the Markdown lexers, the LaTeX parser) treats each lone surrogate as one code point of its own value, U+D800 to U+DFFF, and builds
// text from code points with the surrogates spelled as WTF-8. Go's own conversions (string(rune), []rune, range) turn every such byte into U+FFFD, which
// measures three cells for a unit that Pi measures as none and writes as one replacement character.
//
// Unlike DecodeRuneInString these do not join two adjacent encoded halves: each three-byte spelling is one code point, so the bytes of a text and the
// lengths of its code points add up (CodePointLen) and a text built back from its code points is the same bytes.

// surrogateAt is the code value of the lone surrogate the three bytes at the start of s spell.
func surrogateAt(s string) (rune, bool) {
	if len(s) < 3 || s[0] != 0xED || s[1] < 0xA0 || s[1] > 0xBF || s[2] < 0x80 || s[2] > 0xBF {
		return 0, false
	}
	return rune(0xD000 | int(s[1]&0x3F)<<6 | int(s[2]&0x3F)), true
}

// DecodeCodePoint is utf8.DecodeRuneInString, except that the three bytes of a lone surrogate are one code point of its value.
func DecodeCodePoint(s string) (rune, int) {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size == 1 {
		if surrogate, ok := surrogateAt(s); ok {
			return surrogate, 3
		}
	}
	return r, size
}

// DecodeLastCodePoint is utf8.DecodeLastRuneInString with the same exception.
func DecodeLastCodePoint(s string) (rune, int) {
	r, size := utf8.DecodeLastRuneInString(s)
	if r == utf8.RuneError && size == 1 && len(s) >= 3 {
		if surrogate, ok := surrogateAt(s[len(s)-3:]); ok {
			return surrogate, 3
		}
	}
	return r, size
}

// CodePointLen is the length of r in WTF-8.
func CodePointLen(r rune) int {
	if r >= 0xD800 && r <= 0xDFFF {
		return 3
	}
	return utf8.RuneLen(r)
}

// CodePointCount is the number of code points of s.
func CodePointCount(s string) int {
	if utf8.ValidString(s) {
		return utf8.RuneCountInString(s)
	}
	n := 0
	for i := 0; i < len(s); {
		_, size := DecodeCodePoint(s[i:])
		i += size
		n++
	}
	return n
}

// AppendCodePoints appends the code points of s to dst.
func AppendCodePoints(dst []rune, s string) []rune {
	for i := 0; i < len(s); {
		if c := s[i]; c < utf8.RuneSelf {
			dst = append(dst, rune(c))
			i++
			continue
		}
		r, size := DecodeCodePoint(s[i:])
		dst = append(dst, r)
		i += size
	}
	return dst
}

// CodePoints is the code points of s: []rune(s) that keeps a lone surrogate.
func CodePoints(s string) []rune {
	if len(s) == 0 {
		return nil
	}
	return AppendCodePoints(make([]rune, 0, len(s)), s)
}

// FromCodePoints is the string of runes, a code point in the range of the surrogates spelled as its WTF-8 bytes: string(runes) that keeps a lone surrogate.
func FromCodePoints(runes []rune) string {
	for i, r := range runes {
		if r >= 0xD800 && r <= 0xDFFF {
			out := make([]byte, 0, len(runes)+4)
			out = append(out, string(runes[:i])...)
			for _, r := range runes[i:] {
				out = AppendCodePoint(out, r)
			}
			return string(out)
		}
	}
	return string(runes)
}

// AppendCodePoint appends r to dst: utf8.AppendRune that spells a surrogate as its three WTF-8 bytes.
func AppendCodePoint(dst []byte, r rune) []byte {
	if r >= 0xD800 && r <= 0xDFFF {
		return append(dst, 0xED, byte(0x80|(r>>6)&0x3F), byte(0x80|r&0x3F))
	}
	return utf8.AppendRune(dst, r)
}

// CodePointString is the string of one code point: string(r) that keeps a lone surrogate.
func CodePointString(r rune) string {
	if r >= 0xD800 && r <= 0xDFFF {
		return string(AppendCodePoint(nil, r))
	}
	return string(r)
}

// ReplaceInvalid is s with every byte that is neither part of valid UTF-8 nor one of the three bytes of a lone surrogate replaced by U+FFFD, a code point
// of its own as for utf8's decoding; s itself when it has none.
func ReplaceInvalid(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var out []byte
	for i := 0; i < len(s); {
		r, size := DecodeCodePoint(s[i:])
		if r == utf8.RuneError && size == 1 {
			if out == nil {
				out = make([]byte, 0, len(s)+8)
				out = append(out, s[:i]...)
			}
			out = utf8.AppendRune(out, utf8.RuneError)
		} else if out != nil {
			out = append(out, s[i:i+size]...)
		}
		i += size
	}
	if out == nil {
		return s
	}
	return string(out)
}
