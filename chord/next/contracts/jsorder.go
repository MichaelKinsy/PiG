package contracts

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Helpers shared by the files of this package: JavaScript's string order and key enumeration, and errors.

// fail is `new Error(message)`: the message is upstream's text.
func fail(format string, args ...any) error {
	if len(args) == 0 {
		return errors.New(format)
	}
	return fmt.Errorf(format, args...)
}

// compare16 is `a < b ? -1 : a > b ? 1 : 0` over JavaScript strings: UTF-16 code unit order. It differs from byte
// order only where a supplementary character meets one of U+E000 to U+FFFF.
func compare16(a, b string) int {
	for len(a) > 0 && len(b) > 0 {
		ra, sizeA := utf8.DecodeRuneInString(a)
		rb, sizeB := utf8.DecodeRuneInString(b)
		if ra != rb {
			if c := unit16(ra) - unit16(rb); c != 0 {
				return sign(c)
			}
			return sign(int(ra) - int(rb))
		}
		a, b = a[sizeA:], b[sizeB:]
	}
	return sign(len(a) - len(b))
}

// unit16 is the first UTF-16 code unit of r.
func unit16(r rune) int {
	if r >= 0x10000 {
		return 0xD800 + int((r-0x10000)>>10)
	}
	return int(r)
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// sortStrings is `[...names].sort()`: UTF-16 order.
func sortStrings(names []string) []string {
	out := slices.Clone(names)
	slices.SortFunc(out, compare16)
	return out
}

// ownKeyOrder is the order JavaScript enumerates an object's own string keys: array-index keys first in ascending
// order, then the others in insertion order. It orders entries stably by name.
func ownKeyOrder[T any](entries []T, name func(T) string) {
	jsstring.SortOwnKeys(entries, name)
}

// quote is JSON.stringify of a string.
func quote(text string) string {
	out, err := jsstring.MarshalJSON(text)
	if err != nil {
		return text
	}
	return string(out)
}

// itoa is the decimal text of n.
func itoa(n int) string { return strconv.Itoa(n) }
