package jsstring

import "slices"

// IndexOf implements String.indexOf with UTF-16 positions, including searches for a surrogate half inside a pair.
func IndexOf(text, search string, from int) int {
	units, needle := ToUTF16(text), ToUTF16(search)
	from = max(0, min(from, len(units)))
	for pos := from; pos+len(needle) <= len(units); pos++ {
		if slices.Equal(units[pos:pos+len(needle)], needle) {
			return pos
		}
	}
	return -1
}

// LastIndexOf implements String.lastIndexOf, whose from argument bounds the start of the match and clamps negative positions to zero.
func LastIndexOf(text, search string, from int) int {
	units, needle := ToUTF16(text), ToUTF16(search)
	from = min(max(0, min(from, len(units))), len(units)-len(needle))
	for pos := from; pos >= 0; pos-- {
		if slices.Equal(units[pos:pos+len(needle)], needle) {
			return pos
		}
	}
	return -1
}
