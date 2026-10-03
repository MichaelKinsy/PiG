package delta

import "unicode/utf16"

// Ports packages/chord/src/delta/index.ts (overlap).

const (
	overlapProbe         = 64
	overlapMaxCandidates = 8
)

// Overlap returns the longest suffix of a that is a prefix of b, in UTF-16 code units, looking only at the last scan units of a. The result n always satisfies a[len-n:] == b[:n].
//
// A probe of length h can only find overlaps of at least h, because the head must occur in a. It tries a long head first (few candidates, and it catches the large overlaps a rolling window produces), then one unit, which finds any overlap at the cost of more candidates. Candidates are bounded because repetitive output makes a long head match at thousands of positions; giving up returns 0, which emits a set: larger, never wrong.
func Overlap(a, b string, scan int) int {
	return overlapUnits(utf16.Encode([]rune(a)), utf16.Encode([]rune(b)), scan)
}

func overlapUnits(a, b []uint16, scan int) int {
	if len(a) == 0 || len(b) == 0 || scan == 0 {
		return 0
	}
	tail := a
	if len(a) > scan {
		tail = a[len(a)-scan:]
	}
	for _, probe := range [2]int{min(overlapProbe, len(b)), 1} {
		head := b[:probe]
		tried := 0
		for at := indexUnits(tail, head, 0); at != -1; at = indexUnits(tail, head, at+1) {
			tried++
			if tried > overlapMaxCandidates {
				break
			}
			shared := len(tail) - at
			if shared <= len(b) && equalUnits(tail[at:], b[:shared]) {
				return shared
			}
		}
		if probe == 1 {
			break
		}
	}
	return 0
}

func indexUnits(haystack, needle []uint16, from int) int {
	for at := from; at+len(needle) <= len(haystack); at++ {
		if haystack[at] == needle[0] && equalUnits(haystack[at:at+len(needle)], needle) {
			return at
		}
	}
	return -1
}

func equalUnits(left, right []uint16) bool {
	if len(left) != len(right) {
		return false
	}
	for at := range left {
		if left[at] != right[at] {
			return false
		}
	}
	return true
}
