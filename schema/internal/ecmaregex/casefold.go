package ecmaregex

import (
	"slices"
	"sync"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/sortx"
)

// pig divergence (D219): this table widens character sets by Canonicalize, but a backreference is compared by regexp2 (see emit.go).
// Case-insensitive matching (the i flag, which only a modifier group can turn on here) compares characters through ECMA-262's Canonicalize: with the u flag the simple case folding of CaseFolding.txt, without it the upper case of the code unit unless that is more than one unit or turns a non-ASCII unit into an ASCII one. A character matches another when both have the same Canonicalize result, so a set of characters is widened to every character that shares a result with one of its members.

// A foldTable holds every set of two or more characters that share a Canonicalize result, and the position of each member's set.
type foldTable struct {
	classes [][]rune
	index   map[rune]int
}

var (
	foldOnce   [2]sync.Once
	foldTables [2]foldTable
)

// caseTable returns the foldTable for the u flag (index 1) or without it (index 0).
func caseTable(unicodeMode bool) *foldTable {
	i := 0
	if unicodeMode {
		i = 1
	}
	foldOnce[i].Do(func() {
		var candidates runeSet
		candidates = candidates.union(rangeTable(unicode.Lu), rangeTable(unicode.Ll), rangeTable(unicode.Lt),
			rangeTable(unicode.Other_Lowercase), rangeTable(unicode.Other_Uppercase))
		var classes [][]rune
		if unicodeMode {
			classes = simpleFoldClasses(candidates)
		} else {
			classes = upperCaseClasses(candidates.limit(maxUnit))
		}
		table := foldTable{classes: classes, index: map[rune]int{}}
		for n, class := range classes {
			for _, c := range class {
				table.index[c] = n
			}
		}
		foldTables[i] = table
	})
	return &foldTables[i]
}

// simpleFoldClasses groups the characters by their orbit under unicode.SimpleFold, which is the equivalence of simple case folding.
func simpleFoldClasses(candidates runeSet) [][]rune {
	var classes [][]rune
	for _, r := range candidates {
		for c := r.lo; c <= r.hi; c++ {
			orbit := []rune{c}
			for f := unicode.SimpleFold(c); f != c; f = unicode.SimpleFold(f) {
				orbit = append(orbit, f)
			}
			if len(orbit) > 1 && c == slices.Min(orbit) {
				slices.Sort(orbit)
				classes = append(classes, orbit)
			}
		}
	}
	return classes
}

// upperCaseClasses groups the code units by Canonicalize without the u flag.
func upperCaseClasses(candidates runeSet) [][]rune {
	groups := map[rune][]rune{}
	add := func(key, c rune) {
		if !slices.Contains(groups[key], c) {
			groups[key] = append(groups[key], c)
		}
	}
	for _, r := range candidates {
		for c := r.lo; c <= r.hi; c++ {
			key := canonicalizeUnit(c)
			add(key, c)
			add(key, key)
		}
	}
	var classes [][]rune
	for _, members := range groups {
		if len(members) > 1 {
			slices.Sort(members)
			classes = append(classes, members)
		}
	}
	sortx.Func(classes, func(a, b []rune) int { return int(a[0]) - int(b[0]) })
	return classes
}

var multiCharUppercaseSet = sync.OnceValue(func() runeSet { return parseRangeList(multiCharUppercase) })

// canonicalizeUnit is Canonicalize of a code unit without the u flag. Go's unicode.ToUpper is the simple upper case mapping; the units whose full upper case is several units keep themselves, as ECMA-262 says.
func canonicalizeUnit(c rune) rune {
	if multiCharUppercaseSet().contains(c) {
		return c
	}
	upper := unicode.ToUpper(c)
	if c >= 128 && upper < 128 {
		return c
	}
	return upper
}

// fold widens set to every character that matches one of its members under the i flag.
func fold(set runeSet, unicodeMode bool) runeSet {
	table := caseTable(unicodeMode)
	if len(set) == 1 && set[0].lo == set[0].hi {
		if n, ok := table.index[set[0].lo]; ok {
			return newRuneSet(slices.Collect(func(yield func(runeRange) bool) {
				for _, c := range table.classes[n] {
					if !yield(runeRange{c, c}) {
						return
					}
				}
			}))
		}
		return set
	}
	var extra []runeRange
	for _, class := range table.classes {
		if !slices.ContainsFunc(class, set.contains) {
			continue
		}
		for _, c := range class {
			if !set.contains(c) {
				extra = append(extra, runeRange{c, c})
			}
		}
	}
	if len(extra) == 0 {
		return set
	}
	return set.union(newRuneSet(extra))
}
