package minimatch

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// A case-insensitive JavaScript regular expression without the u flag compares the Canonicalize of each code unit: its toUpperCase, unless that
// is longer than one unit or would turn a non-ASCII character into an ASCII one. .NET (regexp2) lower-cases instead, so the two disagree for the
// characters that have more than two case variants (σ, ς and Σ; µ and μ and Μ; the Dž digraph forms). A literal non-ASCII character of a
// case-insensitive pattern is therefore emitted as the class of its Canonicalize equivalents.

var (
	canonicalOnce    sync.Once
	canonicalClasses map[rune][]rune
)

func canonicalize(r rune) rune {
	upper := jsstring.ToUpper(string(r))
	runes := []rune(upper)
	if len(runes) != 1 || (r >= 128 && runes[0] < 128) {
		return r
	}
	return runes[0]
}

func canonicalMembers(r rune) []rune {
	canonicalOnce.Do(func() {
		canonicalClasses = map[rune][]rune{}
		for c := rune(0x80); c <= 0xFFFF; c++ {
			if c >= 0xD800 && c <= 0xDFFF || !unicode.IsLetter(c) && !unicode.IsUpper(c) && !unicode.IsLower(c) && !unicode.IsTitle(c) {
				continue
			}
			canon := canonicalize(c)
			canonicalClasses[canon] = append(canonicalClasses[canon], c)
		}
	})
	canon := canonicalize(r)
	members := canonicalClasses[canon]
	if canon < 128 {
		return nil
	}
	return members
}

// literalChar is the regular expression source of one literal character.
func literalChar(r rune, nocase bool) string {
	if nocase && r >= 128 && r <= 0xFFFF {
		if members := canonicalMembers(r); len(members) > 1 {
			var b strings.Builder
			b.WriteString("(?-i:[")
			for _, m := range members {
				b.WriteString(braceEscape(string(m)))
			}
			b.WriteString("])")
			return b.String()
		}
	}
	return regexpEscape(string(r))
}

// exactNonASCII spells out the letters of a case-insensitive source outside a character class: each non-ASCII literal in a case-sensitive group, each ASCII letter as its two cases. regexp2 lower-cases both sides, so İ would also match i, but its Canonicalize class (toUpperCase, kept when it would become ASCII) is just itself.
func exactNonASCII(src string) string {
	var b strings.Builder
	inClass := false
	for i := 0; i < len(src); {
		r, size := utf8.DecodeRuneInString(src[i:])
		switch {
		case strings.HasPrefix(src[i:], "(?-i:"):
			// A group literalChar already made case-sensitive: copy it through its closing parenthesis.
			end := i + len("(?-i:")
			for end < len(src) && src[end] != ')' {
				if src[end] == '\\' {
					end++
				}
				end++
			}
			end = min(end+1, len(src))
			b.WriteString(src[i:end])
			size = end - i
		case r == '\\' && i+1 < len(src):
			_, escaped := utf8.DecodeRuneInString(src[i+1:])
			b.WriteString(src[i : i+1+escaped])
			size = 1 + escaped
		case inClass:
			inClass = r != ']'
			b.WriteString(src[i : i+size])
		case r == '[':
			inClass = true
			b.WriteString(src[i : i+size])
		case r >= 128:
			b.WriteString("(?-i:")
			b.WriteString(src[i : i+size])
			b.WriteByte(')')
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			// A letter of the pattern matches only its two ASCII cases: regexp2 would also match İ (to i) and the Kelvin sign (to k).
			b.WriteString("(?-i:[" + strings.ToLower(string(r)) + strings.ToUpper(string(r)) + "])")
		default:
			b.WriteString(src[i : i+size])
		}
		i += size
	}
	return b.String()
}
