package jv

// A string of the value model is a sequence of UTF-16 code units held as
// UTF-8, with a lone surrogate in its three-byte generalized form. The
// functions here count, cut and join by code unit as ECMAScript does.

// surrogateAt reports the code unit when s[i:] starts with a three-byte
// surrogate encoding.
func surrogateAt(s string, i int) (uint16, bool) {
	if i+2 < len(s) && s[i] == 0xED && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2]&0xC0 == 0x80 {
		return uint16(s[i]&0x0f)<<12 | uint16(s[i+1]&0x3f)<<6 | uint16(s[i+2]&0x3f), true
	}
	return 0, false
}

// units appends the UTF-16 code units of s.
func units(dst []uint16, s string) []uint16 {
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c < 0x80:
			dst = append(dst, uint16(c))
			i++
		case c < 0xE0 && i+1 < len(s):
			dst = append(dst, uint16(c&0x1f)<<6|uint16(s[i+1]&0x3f))
			i += 2
		case c < 0xF0 && i+2 < len(s):
			dst = append(dst, uint16(c&0x0f)<<12|uint16(s[i+1]&0x3f)<<6|uint16(s[i+2]&0x3f))
			i += 3
		case i+3 < len(s):
			r := uint32(c&0x07)<<18 | uint32(s[i+1]&0x3f)<<12 | uint32(s[i+2]&0x3f)<<6 | uint32(s[i+3]&0x3f)
			r -= 0x10000
			dst = append(dst, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3ff)))
			i += 4
		default:
			dst = append(dst, 0xFFFD)
			i++
		}
	}
	return dst
}

// fromUnits builds the string for code units, pairing adjacent surrogates.
func fromUnits(u []uint16) string {
	out := make([]byte, 0, len(u)+len(u)/2)
	for i := 0; i < len(u); i++ {
		c := int(u[i])
		if c >= 0xD800 && c <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF {
			c = 0x10000 + (c-0xD800)<<10 + (int(u[i+1]) - 0xDC00)
			i++
		}
		out = appendCodePoint(out, c)
	}
	return string(out)
}

// UTF16Len returns s.length.
func UTF16Len(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c < 0x80, c >= 0xC0 && c < 0xF0:
			n++
		case c >= 0xF0:
			n += 2
		}
	}
	return n
}

// UTF16Units returns the code units of s.
func UTF16Units(s string) []uint16 { return units(make([]uint16, 0, len(s)), s) }

// FromUTF16 returns the string of the code units.
func FromUTF16(u []uint16) string { return fromUnits(u) }

// SliceFrom returns s.slice(from) for 0 <= from.
func SliceFrom(s string, from int) string {
	if from <= 0 {
		return s
	}
	u := UTF16Units(s)
	if from >= len(u) {
		return ""
	}
	return fromUnits(u[from:])
}

// Concat returns a + b, joining a trailing high surrogate of a with a leading
// low surrogate of b into one code point as UTF-16 concatenation does.
func Concat(a, b string) string {
	if len(a) >= 3 && len(b) >= 3 {
		hi, ok1 := surrogateAt(a, len(a)-3)
		lo, ok2 := surrogateAt(b, 0)
		if ok1 && ok2 && hi >= 0xD800 && hi <= 0xDBFF && lo >= 0xDC00 && lo <= 0xDFFF {
			r := 0x10000 + (int(hi)-0xD800)<<10 + (int(lo) - 0xDC00)
			return a[:len(a)-3] + string(appendCodePoint(nil, r)) + b[3:]
		}
	}
	return a + b
}

// HasPrefix reports whether s starts with p as a UTF-16 sequence. A prefix
// ending in a lone high surrogate matches the first half of a pair in s.
func HasPrefix(s, p string) bool {
	if len(p) <= len(s) && s[:len(p)] == p {
		return true
	}
	if len(p) < 3 {
		return false
	}
	pu, su := UTF16Units(p), UTF16Units(s)
	if len(pu) > len(su) {
		return false
	}
	for i := range pu {
		if pu[i] != su[i] {
			return false
		}
	}
	return true
}
