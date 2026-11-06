package jsstring

import "strings"

// JoinsSurrogates reports whether a ends with a lone high surrogate and b starts with a lone low surrogate, both held in their three-byte forms (the way a Go string holds a JavaScript string that has one), so that a+b in JavaScript is one character.
func JoinsSurrogates(a, b string) bool {
	return len(a) >= 3 && len(b) >= 3 && a[len(a)-3] == 0xED && a[len(a)-2] >= 0xA0 && a[len(a)-2] <= 0xAF && b[0] == 0xED && b[1] >= 0xB0 && b[1] <= 0xBF
}

// Concat is a+b as JavaScript strings: a lone high surrogate at the end of a and a lone low surrogate at the start of b make one character.
func Concat(a, b string) string {
	if !JoinsSurrogates(a, b) {
		return a + b
	}
	high := rune(a[len(a)-3]&0x0F)<<12 | rune(a[len(a)-2]&0x3F)<<6 | rune(a[len(a)-1]&0x3F)
	low := rune(b[0]&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
	return a[:len(a)-3] + string(0x10000+(high-0xD800)<<10+(low-0xDC00)) + b[3:]
}

// HasAdjacentSurrogates reports whether s has a lone high surrogate directly followed by a lone low surrogate, both in their three-byte forms: two halves a plain Go concatenation left apart that are one character in JavaScript, and that Canonical joins. It does not allocate.
func HasAdjacentSurrogates(s string) bool {
	for i := 0; i+6 <= len(s); i++ {
		j := strings.IndexByte(s[i:len(s)-5], 0xED)
		if j < 0 {
			return false
		}
		i += j
		if s[i+1] >= 0xA0 && s[i+1] <= 0xAF && s[i+2]&0xC0 == 0x80 && s[i+3] == 0xED && s[i+4] >= 0xB0 && s[i+4] <= 0xBF && s[i+5]&0xC0 == 0x80 {
			return true
		}
	}
	return false
}
