package jsstring

import (
	"strings"
	"sync"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// Collator compares strings as `a.localeCompare(b)` does under the root locale (see LocaleCompare). Like the collate.Collator it wraps it keeps scratch buffers, so one Collator must not be shared by goroutines.
type Collator struct{ c *collate.Collator }

// NewCollator returns a Collator that orders strings as Node's root-locale localeCompare does.
func NewCollator() *Collator { return &Collator{c: collate.New(language.Und)} }

// CompareString returns -1, 0 or 1.
func (c *Collator) CompareString(a, b string) int {
	if !strings.Contains(a, "\uFFFD") && !strings.Contains(b, "\uFFFD") {
		return c.c.CompareString(a, b)
	}
	return c.c.CompareString(replacementCharacterLast(a), replacementCharacterLast(b))
}

// replacementCharacterLast rewrites the two characters ICU weighs above every other code point, U+FFFD below U+FFFF, so that the x/text tables order them alike. x/text weighs U+FFFD like a symbol,
// between "-" and "1", and gives U+FFFF the greatest primary weight, so no single character stands between the two: U+FFFD becomes U+FFFF then a tab and a literal U+FFFF becomes two U+FFFF. A
// token then starts with U+FFFF, above every ordinary character, and the second element, tab or U+FFFF, orders U+FFFD before U+FFFF wherever the token falls; the tab is never compared with
// anything but U+FFFF, so its own weight does not matter.
func replacementCharacterLast(s string) string {
	return replacementTokens.Replace(s)
}

var replacementTokens = strings.NewReplacer("\uFFFD", "\uFFFF\t", "\uFFFF", "\uFFFF\uFFFF")

// A collate.Collator keeps scratch buffers and must not be shared by goroutines, so each comparison borrows one.
var collators = sync.Pool{New: func() any { return NewCollator() }}

// LocaleCompare is `a.localeCompare(b)` under the root locale: punctuation and case are weighed as Node weighs them, `a`
// before `A`, accents after base letters, U+FFFD last. The collation tables are golang.org/x/text's (collate.CLDRVersion,
// collate.UnicodeVersion), older than Node's ICU. It is safe for concurrent use and returns -1, 0 or 1.
func LocaleCompare(a, b string) int {
	collator := collators.Get().(*Collator)
	defer collators.Put(collator)
	return collator.CompareString(a, b)
}
