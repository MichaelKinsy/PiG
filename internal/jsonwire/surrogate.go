package jsonwire

import "bytes"

// HasLoneSurrogateEscape reports whether the JSON text holds a lone UTF-16 surrogate in either form a string of the text can keep it in, which the
// sdk decoder keeps as the surrogate unit and encoding/json/v2 replaces with U+FFFD, where JSON.parse keeps the unit:
//   - a \uD800-\uDFFF escape that is not half of a well-formed pair;
//   - the three-byte form of a surrogate, ED A0..BF xx, which is not UTF-8.
//
// A backslash is read with the byte after it, so the second byte of an escaped backslash starts nothing. It may report true for text that
// has none (the bytes ED A0..BF outside a string, or inside a name or a raw message), never false for text that has one, so a caller that must keep
// the unit sends such text to the sdk decoder and everything else to v2.
func HasLoneSurrogateEscape(data []byte) bool {
	return hasSurrogateBytes(data) || hasLoneSurrogateEscape(data)
}

// hasSurrogateBytes reports whether data holds ED A0..BF, the first two bytes of a surrogate's three-byte form.
func hasSurrogateBytes(data []byte) bool {
	for {
		i := bytes.IndexByte(data, 0xED)
		if i < 0 || i+1 >= len(data) {
			return false
		}
		if data[i+1] >= 0xA0 && data[i+1] <= 0xBF {
			return true
		}
		data = data[i+1:]
	}
}

// hasLoneSurrogateEscape reports whether data holds a \uD800-\uDFFF escape that is not half of a pair. A backslash starts a two-character escape,
// which an escaped backslash is one of, or a \u escape of six bytes.
func hasLoneSurrogateEscape(data []byte) bool {
	for {
		i := bytes.IndexByte(data, '\\')
		if i < 0 || i+1 >= len(data) {
			return false
		}
		if data[i+1] != 'u' {
			data = data[i+2:]
			continue
		}
		rest := data[i+2:]
		switch surrogateKind(rest) {
		case lowSurrogate:
			return true
		case highSurrogate:
			if len(rest) < 10 || rest[4] != '\\' || rest[5] != 'u' || surrogateKind(rest[6:]) != lowSurrogate {
				return true
			}
			data = rest[10:] // both escapes of the pair
			continue
		}
		data = rest[min(4, len(rest)):] // the escape
	}
}

const (
	notSurrogate = iota
	highSurrogate
	lowSurrogate
)

// surrogateKind classifies the four hex digits that follow "\u" at the start of s.
func surrogateKind(s []byte) int {
	if len(s) < 4 || s[0] != 'd' && s[0] != 'D' {
		return notSurrogate
	}
	switch s[1] {
	case '8', '9', 'a', 'b', 'A', 'B':
		return highSurrogate
	case 'c', 'd', 'e', 'f', 'C', 'D', 'E', 'F':
		return lowSurrogate
	}
	return notSurrogate
}
