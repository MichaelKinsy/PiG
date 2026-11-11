// Ports packages/schema/src/formats/typebox/idna/label/puny.ts

package idna

import (
	"slices"
	"strings"
)

// IsPunyLabel reports whether value is an "xn--" label whose payload decodes to a valid Unicode label.
func IsPunyLabel(value string) bool {
	if !IsAcePrefixed(value) {
		return false
	}
	body, ok := LowerAceBody(value)
	if !ok {
		return false
	}
	// A payload consisting of only a hyphen, or starting with the delimiter (like "-9uc"), has 0 basic ASCII characters preceding it and is malformed.
	if strings.LastIndexByte(body, '-') == 0 {
		return false
	}
	decoded, ok := Decode(body)
	if !ok {
		return false
	}
	// RFC 5890 Section 2.3.2.1: a U-label must contain at least one non-ASCII character.
	hasNonAscii := slices.ContainsFunc(decoded, ReNonAscii)
	if !hasNonAscii {
		return false
	}
	return isUnicodeLabel(decoded)
}
