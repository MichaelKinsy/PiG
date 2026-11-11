package jsonwire

import "bytes"

// MayHaveJSForms reports whether compact JSON text may hold a negative number that is zero or a \ufffd escape, the forms sdk json.AppendJSNormalized rewrites to what
// JSON.stringify writes. It may report true for text that has neither (a "-0" or a \ufffd inside a string), never false for text that has one, so a caller skips the
// rewrite scan for text that cannot change. A negative number that is zero starts "-0" or has a negative exponent (-1e-400).
func MayHaveJSForms(text []byte) bool {
	for rest := text; ; {
		i := bytes.IndexByte(rest, '-')
		if i < 0 {
			break
		}
		rest = rest[i+1:]
		if len(rest) == 0 || rest[0] < '0' || rest[0] > '9' {
			continue
		}
		if rest[0] == '0' {
			return true
		}
		j := 1
		for j < len(rest) && (rest[j] >= '0' && rest[j] <= '9' || rest[j] == '.') {
			j++
		}
		if j+1 < len(rest) && (rest[j] == 'e' || rest[j] == 'E') && rest[j+1] == '-' {
			return true
		}
	}
	for i := 0; ; {
		j := bytes.Index(text[i:], []byte(`\u`))
		if j < 0 {
			return false
		}
		i += j + 2
		if i+4 <= len(text) && bytes.EqualFold(text[i:i+4], []byte("fffd")) {
			return true
		}
	}
}
