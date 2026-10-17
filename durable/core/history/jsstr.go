// SPDX-License-Identifier: MIT

package history

// JavaScript string operations on WTF-8 bytes (see unescape): concatenation, slicing by UTF-16 unit and trim, used
// where Pi builds a string and the result is stored or sent.

func isHighSurrogateAt(b []byte, i int) bool {
	return i+2 < len(b) && b[i] == 0xED && b[i+1] >= 0xA0 && b[i+1] <= 0xAF
}

func isLowSurrogateAt(b []byte, i int) bool {
	return i+2 < len(b) && b[i] == 0xED && b[i+1] >= 0xB0 && b[i+1] <= 0xBF
}

func surrogateAt(b []byte, i int) rune {
	return rune(b[i]&0x0F)<<12 | rune(b[i+1]&0x3F)<<6 | rune(b[i+2]&0x3F)
}

// appendJS appends piece to dst as JavaScript string concatenation does: a lone high surrogate that ends dst and a
// lone low surrogate that starts piece become one code point.
func appendJS(dst, piece []byte) []byte {
	if n := len(dst); n >= 3 && isHighSurrogateAt(dst, n-3) && isLowSurrogateAt(piece, 0) {
		hi, lo := surrogateAt(dst, n-3), surrogateAt(piece, 0)
		dst = appendRune(dst[:n-3], 0x10000+((hi-0xD800)<<10)+(lo-0xDC00))
		return append(dst, piece[3:]...)
	}
	return append(dst, piece...)
}

// sliceUnits returns the first n UTF-16 units of w. A cut inside a surrogate pair leaves its lone high surrogate.
func sliceUnits(w []byte, n int) []byte {
	units := 0
	for i := 0; i < len(w); {
		c := w[i]
		var size, u int
		switch {
		case c < 0x80:
			size, u = 1, 1
		case c < 0xE0:
			size, u = 2, 1
		case c < 0xF0:
			size, u = 3, 1
		default:
			size, u = 4, 2
		}
		if units+u > n {
			if u == 2 && units < n {
				cp := rune(c&0x07)<<18 | rune(w[i+1]&0x3F)<<12 | rune(w[i+2]&0x3F)<<6 | rune(w[i+3]&0x3F)
				return appendRune(append([]byte(nil), w[:i]...), 0xD800+((cp-0x10000)>>10))
			}
			return w[:i]
		}
		units += u
		i += size
	}
	return w
}

// isJSSpace reports whether the WTF-8 sequence at w[i:] is a character String.prototype.trim removes, and its size.
func isJSSpace(w []byte, i int) (bool, int) {
	c := w[i]
	switch {
	case c == ' ' || (c >= 0x09 && c <= 0x0D):
		return true, 1
	case c == 0xC2 && i+1 < len(w) && w[i+1] == 0xA0:
		return true, 2
	case c == 0xE1 && i+2 < len(w) && w[i+1] == 0x9A && w[i+2] == 0x80: // U+1680
		return true, 3
	case c == 0xE2 && i+2 < len(w):
		b1, b2 := w[i+1], w[i+2]
		switch {
		case b1 == 0x80 && ((b2 >= 0x80 && b2 <= 0x8A) || b2 == 0xA8 || b2 == 0xA9 || b2 == 0xAF): // U+2000-200A, 2028, 2029, 202F
			return true, 3
		case b1 == 0x81 && b2 == 0x9F: // U+205F
			return true, 3
		}
	case c == 0xE3 && i+2 < len(w) && w[i+1] == 0x80 && w[i+2] == 0x80: // U+3000
		return true, 3
	case c == 0xEF && i+2 < len(w) && w[i+1] == 0xBB && w[i+2] == 0xBF: // U+FEFF
		return true, 3
	}
	return false, 1
}

// trimJS is String.prototype.trim on WTF-8.
func trimJS(w []byte) []byte {
	start := 0
	for start < len(w) {
		ok, n := isJSSpace(w, start)
		if !ok {
			break
		}
		start += n
	}
	end := len(w)
	for end > start {
		// step back one character
		j := end - 1
		for j > start && w[j]&0xC0 == 0x80 {
			j--
		}
		ok, n := isJSSpace(w, j)
		if !ok || j+n != end {
			break
		}
		end = j
	}
	return w[start:end]
}
