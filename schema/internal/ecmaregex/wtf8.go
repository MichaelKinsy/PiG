package ecmaregex

import "unicode/utf8"

// A Go string that holds a JavaScript string keeps a lone UTF-16 surrogate as the three bytes WTF-8 gives it (0xED 0xA0..0xBF 0x80..0xBF); any other byte sequence that is not UTF-8 reads as U+FFFD, one per byte, as range over a string does.

// appendUnits appends the UTF-16 code units of s to dst, one per rune value.
func appendUnits(dst []rune, s string) []rune {
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c < utf8.RuneSelf:
			dst = append(dst, rune(c))
			i++
		case c == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2] >= 0x80 && s[i+2] <= 0xBF:
			dst = append(dst, rune(c&0xF)<<12|rune(s[i+1]&0x3F)<<6|rune(s[i+2]&0x3F))
			i += 3
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r >= 0x10000 {
				r -= 0x10000
				dst = append(dst, 0xD800+r>>10, 0xDC00+r&0x3FF)
			} else {
				dst = append(dst, r)
			}
			i += size
		}
	}
	return dst
}

// codePoints joins each lead surrogate that a trail surrogate follows into one code point and keeps the other units. It reuses the slice.
func codePoints(units []rune) []rune {
	out := units[:0]
	for i := 0; i < len(units); i++ {
		c := units[i]
		if isLeadSurrogate(c) && i+1 < len(units) && isTrailSurrogate(units[i+1]) {
			c = 0x10000 + (c-0xD800)<<10 + (units[i+1] - 0xDC00)
			i++
		}
		out = append(out, c)
	}
	return out
}
