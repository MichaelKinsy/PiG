// Ports packages/schema/src/formats/typebox/idna/pattern/pattern.ts

// Package idna validates hostnames and internationalized hostnames with Pi's own IDNA checks (RFC 5890, 5891, 5892, 5893). Strings are Go strings (UTF-8); where Pi indexes UTF-16 units, the functions here keep the observable result, and Pi's lone surrogates, which a punycode decode can produce, are kept as code points in the 0xD800 to 0xDFFF range of a []rune.
package idna

import (
	"unicode"
	"unicode/utf16"
)

// ReRuleHyphenPlacement is /^(?!-).*(?<!-)$/: no leading or trailing hyphen, and no line terminator because `.` does not match one.
func ReRuleHyphenPlacement(value string) bool {
	if len(value) > 0 && (value[0] == '-' || value[len(value)-1] == '-') {
		return false
	}
	for _, r := range value {
		if isLineTerminator(r) {
			return false
		}
	}
	return true
}

// ReRuleNotReservedAce is /^(?!..--)/: the third and fourth UTF-16 units are not both hyphens when the first two are not line terminators.
func ReRuleNotReservedAce(value string) bool {
	units := make([]uint16, 0, 4)
	for _, r := range value {
		units = utf16.AppendRune(units, r)
		if len(units) >= 4 {
			break
		}
	}
	if len(units) < 4 {
		return true
	}
	return isLineTerminatorUnit(units[0]) || isLineTerminatorUnit(units[1]) || units[2] != '-' || units[3] != '-'
}

// ReAsciiLdh is /^[a-zA-Z0-9-]*$/.
func ReAsciiLdh(value string) bool {
	for i := range len(value) {
		c := value[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

func isLineTerminator(r rune) bool { return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 }

func isLineTerminatorUnit(u uint16) bool { return isLineTerminator(rune(u)) }

// ReNonAscii is /[^\p{ASCII}]/u on one code point.
func ReNonAscii(r rune) bool { return r > 0x7f }

// ReAsciiDigit is /[0-9]/ on one code point.
func ReAsciiDigit(r rune) bool { return r >= '0' && r <= '9' }

// ReArabicIndicDigit is /[\u{0660}-\u{0669}]/u on one code point.
func ReArabicIndicDigit(r rune) bool { return r >= 0x0660 && r <= 0x0669 }

// ReExtArabicIndicDigit is /[\u{06f0}-\u{06f9}]/u on one code point.
func ReExtArabicIndicDigit(r rune) bool { return r >= 0x06f0 && r <= 0x06f9 }

// ReCommonSeparator is /[\u{002e}\u{002c}\u{003a}\u{002f}]/u on one code point.
func ReCommonSeparator(r rune) bool { return r == '.' || r == ',' || r == ':' || r == '/' }

// ReEuropeanSeparator is /[\u{002d}\u{002b}]/u on one code point.
func ReEuropeanSeparator(r rune) bool { return r == '-' || r == '+' }

// ReMarkNonspacing is /\p{Mn}/u on one code point.
func ReMarkNonspacing(r rune) bool { return unicode.Is(unicode.Mn, r) }

// ReMarkSpacingCombining is /\p{Mc}/u on one code point.
func ReMarkSpacingCombining(r rune) bool { return unicode.Is(unicode.Mc, r) }

// ReMarkEnclosing is /\p{Me}/u on one code point.
func ReMarkEnclosing(r rune) bool { return unicode.Is(unicode.Me, r) }

// ReCombiningMark is /[\p{Mn}\p{Mc}\p{Me}]/u on one code point.
func ReCombiningMark(r rune) bool {
	return ReMarkNonspacing(r) || ReMarkSpacingCombining(r) || ReMarkEnclosing(r)
}

// ReLetter is /\p{L}/u on one code point.
func ReLetter(r rune) bool { return unicode.Is(unicode.L, r) }

// ReNumberDecimal is /\p{Nd}/u on one code point.
func ReNumberDecimal(r rune) bool { return unicode.Is(unicode.Nd, r) }

// ReScriptGreek is /\p{Script=Greek}/u on one code point.
func ReScriptGreek(r rune) bool { return unicode.Is(unicode.Greek, r) }

// ReScriptHebrew is /\p{Script=Hebrew}/u on one code point.
func ReScriptHebrew(r rune) bool { return unicode.Is(unicode.Hebrew, r) }

// ReScriptJapanese is /[\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Han}]/u on one code point.
func ReScriptJapanese(r rune) bool {
	return unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Han, r)
}

// ReScriptArabicLetter is /[\p{Script=Arabic}\p{Script=Syriac}\p{Script=Thaana}\p{Script=Mandaic}]/u on one code point.
func ReScriptArabicLetter(r rune) bool {
	return unicode.Is(unicode.Arabic, r) || unicode.Is(unicode.Syriac, r) || unicode.Is(unicode.Thaana, r) || unicode.Is(unicode.Mandaic, r)
}

// ReVirama is the set of Virama code points of RFC 5892 Appendix A.1 and A.2, on one code point.
func ReVirama(r rune) bool {
	switch r {
	case 0x094d, 0x09cd, 0x0a4d, 0x0acd, 0x0b4d, 0x0bcd, 0x0c4d, 0x0ccd, 0x0d3b, 0x0d3c, 0x0d4d, 0x0dca, 0x1b44, 0x1baa, 0x1bab, 0xa9c0,
		0x11046, 0x1107f, 0x110b9, 0x11133, 0x11134, 0x111c0, 0x11235, 0x1134d, 0x11442, 0x114c2, 0x115bf, 0x1163f, 0x116b6, 0x11c3f, 0x11d44, 0x11d45:
		return true
	}
	return false
}

// ReRfc5892Disallowed is the set of code points RFC 5892 disallows although their general category permits them, on one code point.
func ReRfc5892Disallowed(r rune) bool {
	switch r {
	case 0x0640, 0x07fa, 0x302e, 0x302f, 0x3031, 0x3032, 0x3033, 0x3034, 0x3035, 0x303b:
		return true
	}
	return false
}

// ReContextoExceptions is the set of CONTEXTO and CONTEXTJ code points, on one code point.
func ReContextoExceptions(r rune) bool {
	switch r {
	case 0x00b7, 0x0375, 0x05f3, 0x05f4, 0x200c, 0x200d, 0x30fb:
		return true
	}
	return false
}

// RePvalidExceptions is the set of code points RFC 5892 Section 2.6 lists as PVALID, on one code point.
func RePvalidExceptions(r rune) bool {
	switch r {
	case 0x00df, 0x03c2, 0x06fd, 0x06fe, 0x0f0b, 0x3007:
		return true
	}
	return false
}

// ReEuropeanNumber is /[0-9]|[\u{06f0}-\u{06f9}]/u on one code point.
func ReEuropeanNumber(r rune) bool { return ReAsciiDigit(r) || ReExtArabicIndicDigit(r) }

// RePermittedCategory is the union of the letter, separator, decimal digit, mark and exception sets, on one code point.
func RePermittedCategory(r rune) bool {
	return ReLetter(r) || ReEuropeanSeparator(r) || ReCommonSeparator(r) || ReNumberDecimal(r) || ReMarkNonspacing(r) ||
		ReMarkSpacingCombining(r) || ReContextoExceptions(r) || RePvalidExceptions(r)
}
