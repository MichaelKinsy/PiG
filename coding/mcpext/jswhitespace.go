package mcpext

import "unicode"

// isJSWhitespace reports whether r is JavaScript WhiteSpace or LineTerminator, the set String.prototype.trim removes. It
// differs from unicode.IsSpace in U+FEFF (JS whitespace) and U+0085 (not JS whitespace).
func isJSWhitespace(r rune) bool {
	switch r {
	case '\uFEFF':
		return true
	case '\u0085':
		return false
	}
	return unicode.IsSpace(r)
}
