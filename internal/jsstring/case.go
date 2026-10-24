package jsstring

import (
	"strings"
	"unicode"
)

// ToLower is String.prototype.toLowerCase: the full Unicode lowercase mapping. Besides the simple per-code-point mapping it maps U+0130 to
// "i" plus U+0307 and applies the Final_Sigma rule, which lowercases a capital sigma to the final form at the end of a word.
func ToLower(s string) string {
	if isASCIIOnly(s) {
		return strings.ToLower(s)
	}
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range runes {
		switch r {
		case 0x0130:
			b.WriteString("i\u0307")
		case 0x03A3:
			if isFinalSigma(runes, i) {
				b.WriteRune('ς')
			} else {
				b.WriteRune('σ')
			}
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// LowerContains is strings.Contains(ToLower(s), lowerNeedle) for a lowerNeedle that is already lowercase. An ASCII-only s, the common case of a model
// id or name, lowercases only A to Z, so it is searched without building its lowercase copy.
func LowerContains(s, lowerNeedle string) bool {
	if !isASCIIOnly(s) {
		return strings.Contains(ToLower(s), lowerNeedle)
	}
	for i := 0; i+len(lowerNeedle) <= len(s); i++ {
		if asciiLowerPrefix(s[i:], lowerNeedle) {
			return true
		}
	}
	return false
}

// LowerEquals is ToLower(s) == lowerText for a lowerText that is already lowercase, without the lowercase copy of an ASCII-only s.
func LowerEquals(s, lowerText string) bool {
	if !isASCIIOnly(s) {
		return ToLower(s) == lowerText
	}
	return len(s) == len(lowerText) && asciiLowerPrefix(s, lowerText)
}

// asciiLowerPrefix reports whether the ASCII-lowercased start of s is lowerPrefix; s holds at least len(lowerPrefix) bytes.
func asciiLowerPrefix(s, lowerPrefix string) bool {
	for i := 0; i < len(lowerPrefix); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lowerPrefix[i] {
			return false
		}
	}
	return true
}

func isASCIIOnly(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// isFinalSigma is the Final_Sigma condition of SpecialCasing.txt: a cased letter, then any case-ignorable characters, precede the sigma, and no
// cased letter follows it after case-ignorable characters.
func isFinalSigma(runes []rune, i int) bool {
	before := false
	for j := i - 1; j >= 0; j-- {
		if isCaseIgnorable(runes[j]) {
			continue
		}
		before = isCased(runes[j])
		break
	}
	if !before {
		return false
	}
	for j := i + 1; j < len(runes); j++ {
		if isCaseIgnorable(runes[j]) {
			continue
		}
		return !isCased(runes[j])
	}
	return true
}

func isCased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) || unicode.Is(unicode.Other_Lowercase, r) || unicode.Is(unicode.Other_Uppercase, r)
}

// isCaseIgnorable is the Case_Ignorable property: nonspacing and enclosing marks, format characters, modifier letters and symbols, and the
// word-internal punctuation of the Word_Break MidLetter, MidNumLet and Single_Quote classes.
func isCaseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', 0x00B7, 0x0387, 0x055F, 0x05F4, 0x2018, 0x2019, 0x2024, 0x2027, 0xFE13, 0xFE52, 0xFE55, 0xFF07, 0xFF0E, 0xFF1A:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}
