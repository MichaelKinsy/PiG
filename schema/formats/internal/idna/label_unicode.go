// Ports packages/schema/src/formats/typebox/idna/label/unicode.ts

package idna

import "slices"

// exceedsMaxALabelLength reports whether the A-label of a label with non-ASCII characters is longer than 63 octets (RFC 5890 Section 2.3.2.1). A label of ASCII only has no A-label form.
func exceedsMaxALabelLength(codePoints []rune) bool {
	if slices.ContainsFunc(codePoints, ReNonAscii) {
		return len(encode(codePoints))+4 > 63
	}
	return false
}

// hasInvalidHyphens implements RFC 5891 Section 4.2.3.2: the label does not start or end with a hyphen and has no hyphens in its third and fourth code points.
func hasInvalidHyphens(chars []rune) bool {
	if chars[0] == '-' || chars[len(chars)-1] == '-' {
		return true
	}
	return len(chars) >= 4 && chars[2] == '-' && chars[3] == '-'
}

// IsUnicodeLabel validates a Unicode label (a U-label candidate) per RFC 5891, 5892 and 5893 as Pi implements them.
func IsUnicodeLabel(value string) bool { return isUnicodeLabel([]rune(value)) }

func isUnicodeLabel(chars []rune) bool {
	if exceedsMaxALabelLength(chars) {
		return false
	}
	if hasRightToLeftCharacters(chars) && !satisfiesBidiRule(chars) {
		return false
	}
	length := len(chars)
	// An empty label has no first character to test; JavaScript's chars[0] is undefined there and the hyphen and combining mark tests are false.
	if length > 0 {
		if hasInvalidHyphens(chars) {
			return false
		}
		if ReCombiningMark(chars[0]) {
			return false
		}
	}
	hasJapanese := false
	hasKatakanaMiddleDot := false
	for i, codePoint := range chars {
		if ReRfc5892Disallowed(codePoint) {
			return false
		}
		if !RePermittedCategory(codePoint) {
			return false
		}
		if ReScriptJapanese(codePoint) {
			hasJapanese = true
		}
		hasPrev, hasNext := i > 0, i+1 < length
		var prev, next rune
		if hasPrev {
			prev = chars[i-1]
		}
		if hasNext {
			next = chars[i+1]
		}
		switch codePoint {
		case 0x00b7: // MIDDLE DOT (Catalan)
			if !hasPrev || prev != 0x006c || !hasNext || next != 0x006c {
				return false
			}
		case 0x0375: // Greek KERAIA
			if !hasNext || next == 0 || !ReScriptGreek(next) {
				return false
			}
		case 0x05f3, 0x05f4: // Hebrew GERESH
			if !hasPrev || prev == 0 || !ReScriptHebrew(prev) {
				return false
			}
		case 0x200c: // ZWNJ - RFC 5892 Appendix A.1. Full validation needs a joining type table; only a preceding ASCII code point that is not a Virama is rejected.
			if !hasPrev || prev == 0 || (prev < 0x0080 && !ReVirama(prev)) {
				return false
			}
		case 0x200d: // ZWJ - RFC 5892 Appendix A.2
			if !hasPrev || prev == 0 || !ReVirama(prev) {
				return false
			}
		case 0x30fb: // KATAKANA MIDDLE DOT, checked after the loop through hasJapanese
			hasKatakanaMiddleDot = true
		}
	}
	// RFC 5892 Appendix A.7 - Katakana Middle Dot requirement
	return !hasKatakanaMiddleDot || hasJapanese
}
