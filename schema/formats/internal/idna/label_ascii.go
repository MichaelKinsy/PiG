// Ports packages/schema/src/formats/typebox/idna/label/ascii.ts

package idna

// IsAsciiLabel validates an ASCII LDH label per RFC 5891 Section 4.2.3.1.
func IsAsciiLabel(value string) bool {
	return ReRuleHyphenPlacement(value) && ReRuleNotReservedAce(value) && ReAsciiLdh(value)
}
