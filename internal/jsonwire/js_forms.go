package jsonwire

import "bytes"

// MayHaveJSForms reports whether compact JSON text may hold a negative zero or a \ufffd escape, the forms sdk json.AppendJSNormalized rewrites to what
// JSON.stringify writes. It may report true for text that has neither (a "-0" or a \ufffd inside a string, a longer number like -01 is not JSON), never
// false for text that has one, so a caller skips the rewrite scan for text that cannot change.
func MayHaveJSForms(text []byte) bool {
	if bytes.Contains(text, []byte("-0")) {
		return true
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
