package runtime

import "strings"

// isJSSpace reports whether r is JavaScript white space or a line terminator: what `String.prototype.trim` removes
// and what `\s` matches in a JavaScript regular expression (Go's `\s` is ASCII only and lacks \v).
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsTrim is `text.trim()`.
func jsTrim(text string) string { return strings.TrimFunc(text, isJSSpace) }
