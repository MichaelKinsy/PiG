// Ports packages/schema/src/formats/typebox/idna/format/bidi.ts

package idna

// bidiClass is the bidirectional class GetBidiClass assigns to a code point.
type bidiClass uint8

const (
	bidiON bidiClass = iota
	bidiL
	bidiR
	bidiAL
	bidiAN
	bidiEN
	bidiNSM
)

// HasBidiChars decodes a punycode label to its Unicode form (if it is one) and reports whether the result is an RTL label per RFC 5893 Section 1.4.
func HasBidiChars(value string) bool {
	if IsAcePrefixed(value) {
		body, ok := LowerAceBody(value)
		if !ok {
			return false
		}
		decoded, ok := Decode(body)
		if !ok {
			return false
		}
		return hasRightToLeftCharacters(decoded)
	}
	return HasRightToLeftCharacters(value)
}

func getBidiClass(codePoint rune) bidiClass {
	switch {
	case ReEuropeanNumber(codePoint):
		return bidiEN
	case ReArabicIndicDigit(codePoint):
		return bidiAN
	case ReMarkNonspacing(codePoint):
		return bidiNSM
	case ReScriptHebrew(codePoint):
		return bidiR
	case ReScriptArabicLetter(codePoint):
		return bidiAL
	case ReLetter(codePoint):
		return bidiL
	}
	return bidiON
}

// HasRightToLeftCharacters reports whether value has a character of class R, AL or AN (RFC 5893 Section 1.4).
func HasRightToLeftCharacters(value string) bool {
	for _, r := range value {
		if isRtlClass(getBidiClass(r)) {
			return true
		}
	}
	return false
}

func hasRightToLeftCharacters(codePoints []rune) bool {
	for _, r := range codePoints {
		if isRtlClass(getBidiClass(r)) {
			return true
		}
	}
	return false
}

func isRtlClass(class bidiClass) bool { return class == bidiR || class == bidiAL || class == bidiAN }

// isLtrAllowed is /^(?:L|EN|ES|CS|ET|ON|BN|NSM)$/ on a class GetBidiClass can return.
func isLtrAllowed(class bidiClass) bool {
	return class == bidiL || class == bidiEN || class == bidiON || class == bidiNSM
}

// isRtlAllowed is /^(?:R|AL|AN|EN|ES|CS|ET|ON|BN|NSM)$/ on a class GetBidiClass can return.
func isRtlAllowed(class bidiClass) bool {
	return class == bidiR || class == bidiAL || class == bidiAN || class == bidiEN || class == bidiON || class == bidiNSM
}

// SatisfiesBidiRule reports whether value passes the Bidi Rule checks Pi implements.
func SatisfiesBidiRule(value string) bool { return satisfiesBidiRule([]rune(value)) }

func satisfiesBidiRule(codePoints []rune) bool {
	isRtl := false
	allowed := isLtrAllowed
	sawEN := false
	sawAN := false
	isFirst := true
	for _, r := range codePoints {
		class := getBidiClass(r)
		if isFirst {
			// The first character must be a character with Bidi property L, R, or AL.
			if class != bidiL && class != bidiR && class != bidiAL {
				return false
			}
			isRtl = class == bidiR || class == bidiAL
			if isRtl {
				allowed = isRtlAllowed
			} else {
				allowed = isLtrAllowed
			}
			isFirst = false
		}
		if !allowed(class) {
			return false
		}
		switch class {
		case bidiEN:
			sawEN = true
		case bidiAN:
			sawAN = true
		}
	}
	return !isRtl || !sawEN || !sawAN
}
