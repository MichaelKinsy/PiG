package jv

import (
	"math"
	"math/bits"
	"slices"
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
		if v.nIndex == 0 && v.dead == 0 {
			for i, k := range v.keys {
				if i > 0 {
					dst = append(dst, ',')
				}
				dst = AppendString(dst, k)
				dst = append(dst, ':')
				dst = Append(dst, v.vals[i])
			}
			return append(dst, '}')
		}
		first := true
		v.Entries(func(k string, item any) bool {
			if !first {
				dst = append(dst, ',')
			}
			first = false
			dst = AppendString(dst, k)
			dst = append(dst, ':')
			dst = Append(dst, item)
			return true
		})
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
	for i := PlainEnd(s, 0); i < len(s); i = PlainEnd(s, i) {
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

const (
	swarLSB = 0x0101010101010101
	swarMSB = 0x8080808080808080
)

// PlainEnd is the index of the first byte at or after i that a JSON string scan
// must look at (below 0x20, '"', '\\' or from 0x80), or len(s). AppendString and
// the ai package's chunk decoder share it. It reads eight bytes
// at a time with byte loads and shifts (no unsafe, so TinyGo builds it the same
// way): lane k of the word is byte k, and a borrow can mark a lane above a
// marked one falsely, never one below, so the lowest mark is exact.
func PlainEnd(s string, i int) int {
	// Four independent words per branch: a long plain run, such as source text, keeps the ALUs busy and pays one branch
	// per 32 bytes. The lowest marked lane of the first word with one is the stop, as in the word loop below.
	for ; i+32 <= len(s); i += 32 {
		b := s[i : i+32]
		w0 := uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 | uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
		w1 := uint64(b[8]) | uint64(b[9])<<8 | uint64(b[10])<<16 | uint64(b[11])<<24 | uint64(b[12])<<32 | uint64(b[13])<<40 | uint64(b[14])<<48 | uint64(b[15])<<56
		w2 := uint64(b[16]) | uint64(b[17])<<8 | uint64(b[18])<<16 | uint64(b[19])<<24 | uint64(b[20])<<32 | uint64(b[21])<<40 | uint64(b[22])<<48 | uint64(b[23])<<56
		w3 := uint64(b[24]) | uint64(b[25])<<8 | uint64(b[26])<<16 | uint64(b[27])<<24 | uint64(b[28])<<32 | uint64(b[29])<<40 | uint64(b[30])<<48 | uint64(b[31])<<56
		s0, s1, s2, s3 := plainStops(w0), plainStops(w1), plainStops(w2), plainStops(w3)
		if s0|s1|s2|s3 != 0 {
			switch {
			case s0 != 0:
				return i + bits.TrailingZeros64(s0)>>3
			case s1 != 0:
				return i + 8 + bits.TrailingZeros64(s1)>>3
			case s2 != 0:
				return i + 16 + bits.TrailingZeros64(s2)>>3
			}
			return i + 24 + bits.TrailingZeros64(s3)>>3
		}
	}
	for ; i+8 <= len(s); i += 8 {
		block := s[i : i+8]
		w := uint64(block[0]) | uint64(block[1])<<8 | uint64(block[2])<<16 | uint64(block[3])<<24 |
			uint64(block[4])<<32 | uint64(block[5])<<40 | uint64(block[6])<<48 | uint64(block[7])<<56
		if stops := plainStops(w); stops != 0 {
			return i + bits.TrailingZeros64(stops)>>3
		}
	}
	for ; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == '"' || c == '\\' || c >= 0x80 {
			return i
		}
	}
	return i
}

// plainStops marks the lanes of w that end a plain run: below 0x20, '"', '\\', or from 0x80.
// (w - 0x20 per lane) &^ w marks a lane below 0x20; w itself marks a lane from 0x80.
func plainStops(w uint64) uint64 {
	return ((w-swarLSB*0x20)&^w | swarZero(w^(swarLSB*'"')) | swarZero(w^(swarLSB*'\\')) | w) & swarMSB
}

// swarZero marks each zero lane of x, and possibly lanes above one.
func swarZero(x uint64) uint64 { return (x - swarLSB) &^ x }

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
