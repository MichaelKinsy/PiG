package jv

import "math"

const hexDigits = "0123456789abcdef"

// Stringify returns JSON.stringify(value) with no indent. A non-finite number
// prints as null, as JSON.stringify prints it. Any type outside the value model
// is a programming error and prints as null.
func Stringify(value any) string { return string(Append(nil, value)) }

// Append appends JSON.stringify(value) to dst.
func Append(dst []byte, value any) []byte {
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
	case *Object:
		dst = append(dst, '{')
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
// backslash and C0 controls are escaped, a lone surrogate prints as \udXXX, and
// every other character prints as itself.
func AppendString(dst []byte, s string) []byte {
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
			unit := uint16(s[i]&0x0f)<<12 | uint16(s[i+1]&0x3f)<<6 | uint16(s[i+2]&0x3f)
			dst = append(dst, '\\', 'u', hexDigits[unit>>12], hexDigits[unit>>8&15], hexDigits[unit>>4&15], hexDigits[unit&15])
			i += 3
			start = i
		default: // ill-formed byte: a decoded JS string would hold U+FFFD
			dst = append(dst, s[start:i]...)
			dst = append(dst, 0xEF, 0xBF, 0xBD)
			i++
			start = i
		}
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
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
