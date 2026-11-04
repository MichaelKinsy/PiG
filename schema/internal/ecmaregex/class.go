package ecmaregex

import (
	"slices"
	"strconv"
	"strings"
)

// A runeRange is the inclusive range of code points lo..hi.
type runeRange struct{ lo, hi rune }

// A runeSet is a set of code points as sorted ranges that neither overlap nor touch. A set is never modified once built.
type runeSet []runeRange

// maxCodePoint is the last code point of a u-mode string; maxUnit is the last UTF-16 code unit, the last character of an Annex B string.
const (
	maxCodePoint rune = 0x10FFFF
	maxUnit      rune = 0xFFFF
)

// newRuneSet sorts and merges ranges. It reuses the slice.
func newRuneSet(ranges []runeRange) runeSet {
	slices.SortFunc(ranges, func(a, b runeRange) int { return int(a.lo) - int(b.lo) })
	out := ranges[:0]
	for _, r := range ranges {
		if n := len(out); n > 0 && out[n-1].hi+1 >= r.lo {
			out[n-1].hi = max(out[n-1].hi, r.hi)
			continue
		}
		out = append(out, r)
	}
	return out
}

func singleRune(r rune) runeSet { return runeSet{{r, r}} }

func (s runeSet) union(others ...runeSet) runeSet {
	all := slices.Clone(s)
	for _, o := range others {
		all = append(all, o...)
	}
	return newRuneSet(all)
}

// complement is the set of code points of 0..limit that are not in s.
func (s runeSet) complement(limit rune) runeSet {
	var out runeSet
	next := rune(0)
	for _, r := range s {
		if r.lo > limit {
			break
		}
		if r.lo > next {
			out = append(out, runeRange{next, r.lo - 1})
		}
		next = r.hi + 1
	}
	if next <= limit {
		out = append(out, runeRange{next, limit})
	}
	return out
}

// minus is the set of code points of s that are not in other.
func (s runeSet) minus(others ...runeSet) runeSet {
	removed := runeSet(nil).union(others...)
	out := make(runeSet, 0, len(s))
	j := 0
	for _, r := range s {
		lo := r.lo
		for j < len(removed) && removed[j].hi < lo {
			j++
		}
		k := j
		for lo <= r.hi {
			if k >= len(removed) || removed[k].lo > r.hi {
				out = append(out, runeRange{lo, r.hi})
				break
			}
			if removed[k].lo > lo {
				out = append(out, runeRange{lo, removed[k].lo - 1})
			}
			lo = removed[k].hi + 1
			k++
		}
	}
	return out
}

// limit drops the code points above max.
func (s runeSet) limit(last rune) runeSet {
	out := make(runeSet, 0, len(s))
	for _, r := range s {
		if r.lo > last {
			break
		}
		out = append(out, runeRange{r.lo, min(r.hi, last)})
	}
	return out
}

func (s runeSet) contains(c rune) bool {
	_, found := slices.BinarySearchFunc(s, c, func(r runeRange, c rune) int {
		switch {
		case r.hi < c:
			return -1
		case r.lo > c:
			return 1
		}
		return 0
	})
	return found
}

// parseRangeList reads the hex range list of property_data.go: "41-5a,61".
func parseRangeList(list string) runeSet {
	if list == "" {
		return nil
	}
	parts := strings.Split(list, ",")
	out := make([]runeRange, 0, len(parts))
	for _, part := range parts {
		lo, hi, _ := strings.Cut(part, "-")
		// A code point is at most 21 bits (U+10FFFF), so a rune holds every value of the list.
		first, _ := strconv.ParseUint(lo, 16, 21)
		last := first
		if hi != "" {
			last, _ = strconv.ParseUint(hi, 16, 21)
		}
		out = append(out, runeRange{rune(first), rune(last)})
	}
	return newRuneSet(out)
}
