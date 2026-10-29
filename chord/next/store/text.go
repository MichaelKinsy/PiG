package store

import (
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/store/text.ts
//
// Text in the store (decision "Text is plain UTF-8, in code point order"): Chord compares strings by code point, as
// SQLite's BINARY collation compares their UTF-8 bytes; every stored string is well-formed and free of NUL, and a
// prefix becomes a range by its upper bound.
//
// Go forced difference: a Go string is UTF-8 bytes, so code point order IS byte order and CompareText needs no
// UTF-16 rank; a lone surrogate (WTF-8, see internal/jsstring) is replaced by U+FFFD in StoredText.

// CompareText compares a and b by code point: negative, zero or positive (the sign is the contract; the magnitude
// is not Pi's code-unit difference).
func CompareText(a, b string) int { return strings.Compare(a, b) }

// StoredText is text as the store keeps it: a lone surrogate replaced by U+FFFD (copied only when one occurs), NUL
// refused invalid/args (what names the value). Plugin strings are a trust boundary.
func StoredText(text string, what string) (string, error) {
	if strings.IndexByte(text, 0) >= 0 {
		return "", data.Invalid(data.ReasonArgs, what+": text may not contain NUL")
	}
	if utf8.ValidString(text) {
		return text, nil
	}
	return jsstring.StreamText(text), nil
}

// PrefixUpperBound is the smallest string above every string starting with prefix, in code point order: the next
// code point after its last, carrying past U+10FFFF; ok is false when there is none ("" and all-U+10FFFF prefixes).
// prefix is well-formed.
func PrefixUpperBound(prefix string) (upper string, ok bool) {
	for len(prefix) > 0 {
		last, size := utf8.DecodeLastRuneInString(prefix)
		prefix = prefix[:len(prefix)-size]
		if last == 0x10ffff {
			continue
		}
		next := last + 1
		// The surrogates are no code points of well-formed text: after U+D7FF comes U+E000.
		if last == 0xd7ff {
			next = 0xe000
		}
		return prefix + string(next), true
	}
	return "", false
}
