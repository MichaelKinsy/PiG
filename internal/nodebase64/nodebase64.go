// Package nodebase64 reads base64 text as Node's Buffer.from(text, "base64") does.
package nodebase64

import (
	"unicode/utf16"
	"unicode/utf8"
)

// Decode mirrors Node's Buffer.from(data, "base64"): it accepts the standard and URL-safe alphabets, skips every other character (whitespace included), stops at
// the first '=', and decodes a trailing partial quantum of two or three characters to one or two bytes. Node's legacy decoder reads the low byte of each UTF-16 code
// unit of a two-byte string, so U+0141 reads as 'A' and a character outside the BMP, whose surrogate pair ends in 0x3D or lower bytes of its own, can read as '='.
// The result is never nil.
func Decode(data string) []byte {
	out := make([]byte, 0, len(data)-len(data)/4)
	var acc uint32
	n := 0
	var units [2]uint16
	for i := 0; i < len(data); {
		units[0] = uint16(data[i])
		count := 1
		if data[i] >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(data[i:])
			i += size - 1
			if r >= 0x10000 {
				high, low := utf16.EncodeRune(r)
				units[0], units[1], count = uint16(high), uint16(low), 2
			} else {
				units[0] = uint16(r)
			}
		}
		i++
		for _, unit := range units[:count] {
			c := byte(unit)
			var v byte
			switch {
			case c >= 'A' && c <= 'Z':
				v = c - 'A'
			case c >= 'a' && c <= 'z':
				v = c - 'a' + 26
			case c >= '0' && c <= '9':
				v = c - '0' + 52
			case c == '+' || c == '-':
				v = 62
			case c == '/' || c == '_':
				v = 63
			case c == '=':
				return finish(out, acc, n)
			default:
				continue
			}
			acc = acc<<6 | uint32(v)
			n++
			if n == 4 {
				out = append(out, byte(acc>>16), byte(acc>>8), byte(acc))
				acc, n = 0, 0
			}
		}
	}
	return finish(out, acc, n)
}

func finish(out []byte, acc uint32, n int) []byte {
	switch n {
	case 2:
		out = append(out, byte(acc>>4))
	case 3:
		out = append(out, byte(acc>>10), byte(acc>>2))
	}
	return out
}
