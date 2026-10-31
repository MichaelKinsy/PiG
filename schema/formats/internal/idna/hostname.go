// Ports packages/schema/src/formats/typebox/idna/hostname.ts

package idna

import (
	"strings"
	"unicode/utf16"
)

// utf16Length is the length of value as a JavaScript string.
func utf16Length(value string) int {
	n := 0
	for _, r := range value {
		n += utf16.RuneLen(r)
	}
	return n
}

func isValidLabelLength(value string) bool {
	n := utf16Length(value)
	return n > 0 && n <= 63
}

func isHostnameLabel(value string) bool {
	return isValidLabelLength(value) && (IsPunyLabel(value) || IsAsciiLabel(value))
}

// IsHostname reports whether value is a valid hostname.
func IsHostname(value string) bool {
	n := utf16Length(value)
	if n == 0 || n > 253 {
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
