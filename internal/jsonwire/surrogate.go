package jsonwire

import "bytes"

// HasLoneSurrogateEscape reports whether the JSON text holds a \uD800-\uDFFF escape that is not half of a well-formed pair, as a string of the
// text does not hold in Go: encoding/json/v2 decodes it to U+FFFD, where JSON.parse keeps the UTF-16 unit and the sdk decoder keeps it as the
// surrogate escape. It may report true for text that has none (an escaped backslash before a "u"), never false for text that has one, so a caller
// that must keep the unit sends such text to the sdk decoder and everything else to v2.
func HasLoneSurrogateEscape(data []byte) bool {
	for {
		i := bytes.Index(data, []byte(`\u`))
		if i < 0 {
			return false
		}
		data = data[i+2:]
		switch surrogateKind(data) {
		case lowSurrogate:
			return true
		case highSurrogate:
			if len(data) < 10 || data[4] != '\\' || data[5] != 'u' || surrogateKind(data[6:]) != lowSurrogate {
				return true
			}
			data = data[10:]
		}
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
