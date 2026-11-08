package runtime

import "strings"

// localeCompare is JavaScript's `a.localeCompare(b)` for the ASCII names p2 sorts by (plugin names, registry keys): ICU's
// root collation orders punctuation before digits before letters, compares letters without case first and breaks a
// tie with lowercase before uppercase. Text outside printable ASCII compares by code point after it.
func localeCompare(a, b string) int {
	if c := compareKeys(a, b, true); c != 0 {
		return c
	}
	return compareKeys(a, b, false)
}

// icuPunctuation is the order of ASCII punctuation in the root collation (non-ignorable).
const icuPunctuation = "_-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$"

func rank(r rune) (class int, order int) {
	switch {
	case r >= '0' && r <= '9':
		return 2, int(r - '0')
	case r >= 'a' && r <= 'z':
		return 3, int(r - 'a')
	case r >= 'A' && r <= 'Z':
		return 3, int(r - 'A')
	case r == ' ':
		return 0, 0
	}
	if i := strings.IndexRune(icuPunctuation, r); i >= 0 {
		return 1, i
	}
	return 4, int(r)
}

func compareKeys(a, b string, primary bool) int {
	ar, br := []rune(a), []rune(b)
	for i := 0; i < len(ar) && i < len(br); i++ {
		ac, ao := rank(ar[i])
		bc, bo := rank(br[i])
		if ac != bc {
			return sign(ac - bc)
		}
		if ao != bo {
			return sign(ao - bo)
		}
		if !primary && ar[i] != br[i] {
			// Lowercase before uppercase.
			if ar[i] >= 'a' && ar[i] <= 'z' {
				return -1
			}
			return 1
		}
	}
	return sign(len(ar) - len(br))
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

// LocaleCompare is `a.localeCompare(b)` (see localeCompare for what it covers): for the plugins that sort names as Pi's
// code does.
func LocaleCompare(a, b string) int { return localeCompare(a, b) }
