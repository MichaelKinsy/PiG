package minimatch

import (
	"strings"
	"sync"
	"unicode"
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
	upper := strings.ToUpper(string(r))
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
			b.WriteByte('[')
			for _, m := range members {
				b.WriteString(braceEscape(string(m)))
			}
			b.WriteByte(']')
			return b.String()
		}
	}
	return regexpEscape(string(r))
}
