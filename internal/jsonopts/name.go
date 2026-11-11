package jsonopts

import (
	"bytes"
	"encoding/json/jsontext"
	"unicode/utf16"
	"unicode/utf8"
)

// ReadName reads the next object member name from d and returns it as the JavaScript string JSON.parse gives for the property key, in the
// WTF-8 form internal/jsstring uses: an escaped lone surrogate ("\ud800") is its three WTF-8 bytes, a raw ill-formed byte sequence is U+FFFD
// (one per maximal subpart, as Node decodes bytes to a string), and every other name is plain UTF-8. Two names are the same Go string exactly
// when JSON.parse gives the same key, so a map or a search keyed by the result dedups as a JavaScript object does. Token.String() cannot do
// this: it turns "\ud800", "\ud801" and the raw bytes ED A0 80 all into U+FFFD, which merges three distinct keys into one.
//
// The caller reads names where an object has one (PeekKind is not '}'); anything else is an error.
func ReadName(d *jsontext.Decoder) (string, error) {
	raw, err := d.ReadValue()
	if err != nil {
		return "", err
	}
	return Name(raw), nil
}

// Name is the JavaScript key of the quoted JSON string raw (see ReadName); raw is well-formed up to ill-formed UTF-8, as a jsontext decoder
// with AllowInvalidUTF8 hands it back.
func Name(raw []byte) string {
	body := raw[1 : len(raw)-1]
	if bytes.IndexByte(body, '\\') < 0 && utf8.Valid(body) {
		return string(body)
	}
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); {
		c := body[i]
		switch {
		case c == '\\':
			i++
			switch e := body[i]; e {
			case 'b':
				out, i = append(out, '\b'), i+1
			case 'f':
				out, i = append(out, '\f'), i+1
			case 'n':
				out, i = append(out, '\n'), i+1
			case 'r':
				out, i = append(out, '\r'), i+1
			case 't':
				out, i = append(out, '\t'), i+1
			case 'u':
				unit := hex4(body[i+1:])
				i += 5
				if utf16.IsSurrogate(unit) && unit < 0xDC00 && i+6 <= len(body) && body[i] == '\\' && body[i+1] == 'u' {
					if low := hex4(body[i+2:]); low >= 0xDC00 && low <= 0xDFFF {
						out, i = utf8.AppendRune(out, utf16.DecodeRune(unit, low)), i+6
						continue
					}
				}
				if utf16.IsSurrogate(unit) {
					out = append(out, 0xE0|byte(unit>>12), 0x80|byte(unit>>6)&0x3F, 0x80|byte(unit)&0x3F)
				} else {
					out = utf8.AppendRune(out, unit)
				}
			default: // '"', '\\', '/'
				out, i = append(out, e), i+1
			}
		case c < utf8.RuneSelf:
			out, i = append(out, c), i+1
		default:
			r, size := utf8.DecodeRune(body[i:])
			if r == utf8.RuneError && size == 1 {
				out, i = utf8.AppendRune(out, utf8.RuneError), i+illFormedLen(body[i:])
			} else {
				out, i = append(out, body[i:i+size]...), i+size
			}
		}
	}
	return string(out)
}

// hex4 is the value of the four hex digits at the start of b, which jsontext has already checked.
func hex4(b []byte) rune {
	var v rune
	for _, c := range b[:4] {
		switch {
		case c >= 'a':
			c -= 'a' - 10
		case c >= 'A':
			c -= 'A' - 10
		default:
			c -= '0'
		}
		v = v<<4 | rune(c)
	}
	return v
}

// illFormedLen is the length of the maximal subpart of an ill-formed UTF-8 sequence at the start of b: the longest prefix that could begin
// a well-formed sequence, at least one byte. The WHATWG decoder, which Node uses, replaces each such subpart with one U+FFFD.
func illFormedLen(b []byte) int {
	lo, hi := byte(0x80), byte(0xBF)
	need := 0
	switch c := b[0]; {
	case c >= 0xC2 && c <= 0xDF:
		need = 1
	case c == 0xE0:
		need, lo = 2, 0xA0
	case c >= 0xE1 && c <= 0xEC, c == 0xEE, c == 0xEF:
		need = 2
	case c == 0xED:
		need, hi = 2, 0x9F
	case c == 0xF0:
		need, lo = 3, 0x90
	case c >= 0xF1 && c <= 0xF3:
		need = 3
	case c == 0xF4:
		need, hi = 3, 0x8F
	default:
		return 1
	}
	n := 1
	for ; n <= need && n < len(b); n++ {
		if b[n] < lo || b[n] > hi {
			break
		}
		lo, hi = 0x80, 0xBF
	}
	return n
}

// ValidName is name with each lone surrogate (its three WTF-8 bytes) replaced by U+FFFD, the string encoding/json and Token.String() give; name
// itself when it has none. A site that keeps a key in a Go string it hands to encoding/json uses this, and dedups on the name ReadName gave.
func ValidName(name string) string {
	for i := 0; i+2 < len(name); i++ {
		if name[i] == 0xED && name[i+1] >= 0xA0 {
			out := make([]byte, 0, len(name))
			for j := 0; j < len(name); {
				if name[j] == 0xED && j+2 < len(name) && name[j+1] >= 0xA0 && name[j+1] <= 0xBF {
					out, j = utf8.AppendRune(out, utf8.RuneError), j+3
					continue
				}
				out, j = append(out, name[j]), j+1
			}
			return string(out)
		}
	}
	return name
}
