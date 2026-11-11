// Ports packages/schema/src/formats/typebox/idna/idn-hostname.ts

package idna

import (
	"slices"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func isIdnHostnameLabel(value string) bool {
	return isValidLabelLength(value) && (IsPunyLabel(value) || IsUnicodeLabel(value))
}

// normalizeHostname maps fullwidth code points to their ASCII equivalent (U+FF41 maps to "a"), normalizes to NFC, removes the code points IDNA mapping ignores, and converts full stop variants to ASCII "." so the hostname can be split into labels.
func normalizeHostname(value string) string {
	mapped := strings.Map(func(r rune) rune {
		if r >= 0xff01 && r <= 0xff5e {
			return r - 0xfee0
		}
		return r
	}, value)
	normalized := norm.NFC.String(mapped)
	return strings.Map(func(r rune) rune {
		switch {
		case r == 0x00ad, r == 0x034f, r >= 0x180b && r <= 0x180d, r == 0x200b, r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0100 && r <= 0xe01ef:
			return -1
		case r == 0x3002, r == 0xff0e, r == 0xff61:
			return '.'
		}
		return r
	}, normalized)
}

// IsIdnHostname reports whether value is a valid internationalized (IDN) hostname.
func IsIdnHostname(value string) bool {
	value, ok := wellFormed(value)
	if !ok || len(value) == 0 || strings.Contains(value, " ") {
		return false
	}
	normalized := normalizeHostname(value)
	if jsstring.Length(normalized) > 253 {
		return false
	}
	labels := strings.Split(normalized, ".")
	hasBidiChars := slices.ContainsFunc(labels, HasBidiChars)
	for _, label := range labels {
		if !isIdnHostnameLabel(label) || hasBidiChars && !SatisfiesBidiRule(label) {
			return false
		}
	}
	return true
}
