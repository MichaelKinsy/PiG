// Ports packages/schema/src/formats/typebox/idna/hostname.ts

package idna

import (
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// wellFormed returns value with adjacent surrogate halves joined into the code point they make in JavaScript, and reports whether the result has no lone surrogate. A Go string holds a lone surrogate as three WTF-8 bytes, so it is not valid UTF-8. No hostname with a lone surrogate passes: the label that has it is checked on its own, IsPunyLabel needs an ASCII payload, IsAsciiLabel only letters, digits and hyphens, and a Unicode label needs every code point in RePermittedCategory, which a surrogate is not in.
func wellFormed(value string) (string, bool) {
	value = jsstring.Canonical(value)
	return value, utf8.ValidString(value)
}

func isValidLabelLength(value string) bool {
	n := jsstring.Length(value)
	return n > 0 && n <= 63
}

func isHostnameLabel(value string) bool {
	return isValidLabelLength(value) && (IsPunyLabel(value) || IsAsciiLabel(value))
}

// IsHostname reports whether value is a valid hostname.
func IsHostname(value string) bool {
	value, ok := wellFormed(value)
	n := jsstring.Length(value)
	if !ok || n == 0 || n > 253 {
		return false
	}
	if value[len(value)-1] == '.' {
		return false
	}
	for label := range strings.SplitSeq(value, ".") {
		if !isHostnameLabel(label) {
			return false
		}
	}
	return true
}
