package jv

import (
	"math"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/jsonscan"
)

const hexDigits = "0123456789abcdef"

// Stringify returns JSON.stringify(value) with no indent. A non-finite number
// prints as null, as JSON.stringify prints it. Any type outside the value model
// is a programming error and prints as null.
func Stringify(value any) string {
	var buf [96]byte // most values are short: the text grows in place and the string is the one allocation
	return string(Append(buf[:0], value))
}

// Append appends JSON.stringify(value) to dst.
func Append(dst []byte, value any) []byte {
	if dst == nil {
		dst = make([]byte, 0, 64) // growing from nothing takes 8, 16, 32 before the first record fits
	}
	switch v := value.(type) {
	case nil:
		return append(dst, "null"...)
	case bool:
		if v {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return append(dst, "null"...)
		}
		return AppendNumber(dst, v)
	case string:
		return AppendString(dst, v)
	case Raw:
		return append(dst, v...)
	case *Object:
		dst = append(dst, '{')
		keys := v.keys
		if v.indexKey {
			keys = v.orderedKeys()
		}
		for i, k := range keys {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = AppendString(dst, k)
			dst = append(dst, ':')
			if v.indexKey {
				item, _ := v.Get(k)
				dst = Append(dst, item)
			} else {
				dst = Append(dst, v.vals[i])
			}
		}
		return append(dst, '}')
	case []any:
		dst = append(dst, '[')
		for i, item := range v {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = Append(dst, item)
		}
		return append(dst, ']')
	}
	return append(dst, "null"...)
}

// AppendString appends the JSON.stringify form of s: only the quote, the
// backslash and C0 controls are escaped, an unpaired surrogate prints as \udXXX,
// a high surrogate directly followed by a low one prints as the character the
// pair forms (as JavaScript joins them), and every other character prints as
// itself.
//
// Runs that need no escape are found eight bytes at a time and copied whole,
// and dst grows once for the whole string.
func AppendString(dst []byte, s string) []byte {
	if cap(dst)-len(dst) < len(s)+2 {
		dst = slices.Grow(dst, len(s)+len(s)/8+2)
	}
	dst = append(dst, '"')
	start := 0
	for i := jsonscan.PlainEnd(s, 0); i < len(s); i = jsonscan.PlainEnd(s, i) {
		c := s[i]
		if c < 0x80 {
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

// surrogateUnit is the UTF-16 unit of the three-byte WTF-8 surrogate sequence at s[i].
func surrogateUnit(s string, i int) uint16 {
	return uint16(s[i]&0x0f)<<12 | uint16(s[i+1]&0x3f)<<6 | uint16(s[i+2]&0x3f)
}

const (
	multiOK = iota
	multiSurrogate
	multiBad
)

// decodeMulti classifies the multi-byte sequence at s[i] (s[i] >= 0x80).
func decodeMulti(s string, i int) (int, int) {
	c := s[i]
	n := len(s) - i
	cont := func(j int) bool { return j < n && s[i+j]&0xC0 == 0x80 }
	switch {
	case c >= 0xC2 && c <= 0xDF:
		if cont(1) {
			return 2, multiOK
		}
	case c == 0xE0:
		if n > 1 && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && cont(2) {
			return 3, multiOK
		}
	case c == 0xED:
		if n > 1 && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && cont(2) {
			return 3, multiSurrogate
		}
		if n > 1 && s[i+1] >= 0x80 && s[i+1] <= 0x9F && cont(2) {
			return 3, multiOK
		}
	case c >= 0xE1 && c <= 0xEF:
		if cont(1) && cont(2) {
			return 3, multiOK
		}
	case c == 0xF0:
		if n > 1 && s[i+1] >= 0x90 && s[i+1] <= 0xBF && cont(2) && cont(3) {
			return 4, multiOK
		}
	case c >= 0xF1 && c <= 0xF3:
		if cont(1) && cont(2) && cont(3) {
			return 4, multiOK
		}
	case c == 0xF4:
		if n > 1 && s[i+1] >= 0x80 && s[i+1] <= 0x8F && cont(2) && cont(3) {
			return 4, multiOK
		}
	}
	return 1, multiBad
}

// maximalSubpart is the length of the ill-formed sequence at s[i] that a WHATWG UTF-8 decoder replaces with one U+FFFD: a lead byte with the
// continuation bytes it accepts before the sequence breaks off, or one byte that cannot start a sequence.
func maximalSubpart(s string, i int) int {
	c := s[i]
	need, lo, hi := 0, byte(0x80), byte(0xBF)
	switch {
	case c >= 0xC2 && c <= 0xDF:
		need = 2
	case c == 0xE0:
		need, lo = 3, 0xA0
	case c == 0xED:
		need, hi = 3, 0x9F
	case c >= 0xE1 && c <= 0xEF:
		need = 3
	case c == 0xF0:
		need, lo = 4, 0x90
	case c >= 0xF1 && c <= 0xF3:
		need = 4
	case c == 0xF4:
		need, hi = 4, 0x8F
	default:
		return 1
	}
	n := 1
	if i+n < len(s) && s[i+n] >= lo && s[i+n] <= hi {
		n++
		for n < need && i+n < len(s) && s[i+n]&0xC0 == 0x80 {
			n++
		}
	}
	return n
}
