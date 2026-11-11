package minimatch

// Ports @isaacs/nonrecursive-glob-match 1.0.3 parse-class.js: a posix bracket expression of a glob, as a test of one code point.

import (
	"unicode"
)

// runeRange is an inclusive range of code points; a single code point has lo == hi.
type runeRange struct{ lo, hi rune }

// runeSet is the union of code point ranges and Unicode categories one side of a class names.
type runeSet struct {
	ranges []runeRange
	// tables are Unicode general-category tables, and cats the categories that no Go table spells (\p{C} includes the unassigned code points).
	tables []*unicode.RangeTable
	// unassigned adds the code points that belong to no category, which the category C of ECMAScript has and Go's does not.
	unassigned bool
}

func (s *runeSet) has(r rune) bool {
	for _, rr := range s.ranges {
		if rr.lo <= r && r <= rr.hi {
			return true
		}
	}
	for _, t := range s.tables {
		if unicode.Is(t, r) {
			return true
		}
	}
	return s.unassigned && isUnassigned(r)
}

var assignedCategories = []*unicode.RangeTable{unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.Z, unicode.C}

// isUnassigned is \p{Cn}: a code point that no general category in Go's tables holds.
func isUnassigned(r rune) bool { return !unicode.In(r, assignedCategories...) }

// found reports whether the set holds r or, with fold, a code point that ECMAScript's Canonicalize (Unicode simple case folding, the `u` flag) maps with it: a character class matches when some member of the class has the same canonical form as the tested code point.
func (s *runeSet) found(r rune, fold bool) bool {
	if s.has(r) {
		return true
	}
	if !fold {
		return false
	}
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if s.has(f) {
			return true
		}
	}
	return false
}

// foldEqual is the match of a case-insensitive RegExp of one escaped code point (`iu`): both have the same simple case folding.
func foldEqual(a, b rune) bool {
	if a == b {
		return true
	}
	for f := unicode.SimpleFold(a); f != a; f = unicode.SimpleFold(f) {
		if f == b {
			return true
		}
	}
	return false
}

// class is the RegExp parseClass returns: `^(sranges|snegs)` over one code point, or one that never matches.
type class struct {
	never bool
	// negate is a class that starts with `!` or `^`.
	negate bool
	// hasRanges and hasNegs say whether any entry went into ranges (the members of the class) or negs (the members of the negated POSIX classes [:graph:] and [:print:]); an entry can leave a set empty.
	hasRanges, hasNegs bool
	ranges, negs       runeSet
	// invertRanges is a ranges class that the regular expression source reads as negated: parseClass joins the entries unescaped, so a class that does not start with `!` or `^` but whose first entry starts with `^` becomes `[^...]`.
	invertRanges bool
	fold         bool
}

// test is re.test(c) for the one code point c.
func (c *class) test(r rune) bool {
	if c.never {
		return false
	}
	// sranges is [R] or [^R], snegs is [^N] or, with a negated class, [N].
	inRanges := c.hasRanges && c.ranges.found(r, c.fold) != (c.negate || c.invertRanges)
	inNegs := c.hasNegs && c.negs.found(r, c.fold) == c.negate
	return inRanges || inNegs
}

type posixClass struct {
	name   string
	tables []*unicode.RangeTable
	ranges []runeRange
	// unassigned is set for the classes that name \p{C}.
	unassigned bool
	negated    bool
}

var (
	asciiRange = runeRange{0, 0x7f}
	tabRange   = runeRange{'\t', '\t'}
)

// posixClasses are the POSIX classes translated to Unicode properties, in the order parse-class.js tries them.
func posixClassList() []posixClass {
	return []posixClass{
		{name: "[:alnum:]", tables: []*unicode.RangeTable{unicode.L, unicode.Nl, unicode.Nd}},
		{name: "[:alpha:]", tables: []*unicode.RangeTable{unicode.L, unicode.Nl}},
		{name: "[:ascii:]", ranges: []runeRange{asciiRange}},
		{name: "[:blank:]", tables: []*unicode.RangeTable{unicode.Zs}, ranges: []runeRange{tabRange}},
		{name: "[:cntrl:]", tables: []*unicode.RangeTable{unicode.Cc}},
		{name: "[:digit:]", tables: []*unicode.RangeTable{unicode.Nd}},
		{name: "[:graph:]", tables: []*unicode.RangeTable{unicode.Z, unicode.C}, unassigned: true, negated: true},
		{name: "[:lower:]", tables: []*unicode.RangeTable{unicode.Ll}},
		{name: "[:print:]", tables: []*unicode.RangeTable{unicode.C}, unassigned: true, negated: true},
		{name: "[:punct:]", tables: []*unicode.RangeTable{unicode.P, unicode.S}},
		{name: "[:space:]", tables: []*unicode.RangeTable{unicode.Z}, ranges: []runeRange{{'\t', '\r'}}},
		{name: "[:upper:]", tables: []*unicode.RangeTable{unicode.Lu}},
		{name: "[:word:]", tables: []*unicode.RangeTable{unicode.L, unicode.Nl, unicode.Nd, unicode.Pc}},
		{name: "[:xdigit:]", ranges: []runeRange{{'A', 'F'}, {'a', 'f'}, {'0', '9'}}},
	}
}

var posixClasses = posixClassList()

func hasPrefixAt(glob []rune, prefix string, at int) bool {
	i := at
	for _, r := range prefix {
		if i >= len(glob) || glob[i] != r {
			return false
		}
		i++
	}
	return true
}

// parseClass takes a glob at a posix bracket expression and returns the test of one code point and the number of glob code points consumed. If the class is not closed, consumed is 0 and the `[` is a literal. Out of order ranges are dropped; if nothing is left, the class never matches and the rest of the glob is consumed.
func parseClass(glob []rune, position int, nocase bool) (*class, int) {
	noMatch := &class{never: true}
	pos := position
	var ranges, negs runeSet
	var negate, sawStart, escaping, hasRanges, hasNegs, invert bool
	endPos := pos
	var rangeStart rune
	haveRangeStart := false
	// addSingle adds one code point as an entry. The first entry of a class that does not start with `!` or `^` is read as a negation when it is `^`, and then holds no member.
	addSingle := func(c rune) {
		if !hasRanges && !negate && c == '^' {
			invert = true
		} else {
			ranges.ranges = append(ranges.ranges, runeRange{c, c})
		}
		hasRanges = true
	}
	i := pos + 1
loop:
	for i < len(glob) {
		c := glob[i]
		if (c == '!' || c == '^') && i == pos+1 {
			negate = true
			i++
			continue
		}
		if c == ']' && sawStart && !escaping {
			endPos = i + 1
			break
		}
		sawStart = true
		if c == '\\' && !escaping {
			escaping = true
			i++
			continue
		}
		if c == '[' && !escaping {
			// either a posix class, a collation equivalent, or just a [
			for _, p := range posixClasses {
				if !hasPrefixAt(glob, p.name, i) {
					continue
				}
				// invalid, [a-[] is fine, but not [a-[:alpha]]
				if haveRangeStart {
					return noMatch, len(glob) - pos
				}
				i += len([]rune(p.name))
				target := &ranges
				if p.negated {
					target = &negs
					hasNegs = true
				} else {
					hasRanges = true
				}
				target.tables = append(target.tables, p.tables...)
				target.ranges = append(target.ranges, p.ranges...)
				target.unassigned = target.unassigned || p.unassigned
				continue loop
			}
		}
		// now it's just a normal character, effectively
		escaping = false
		if haveRangeStart {
			// throw this range away if it's not valid, but others can still match.
			if c > rangeStart {
				if first := !hasRanges && !negate && rangeStart == '^'; first {
					// the source reads `^-c` as a negated class of `-` and c
					invert = true
					ranges.ranges = append(ranges.ranges, runeRange{'-', '-'}, runeRange{c, c})
				} else {
					ranges.ranges = append(ranges.ranges, runeRange{rangeStart, c})
				}
				hasRanges = true
			} else if c == rangeStart {
				addSingle(c)
			}
			haveRangeStart = false
			i++
			continue
		}
		// now might be the start of a range. It can be either c-d or c-] or c<more...>] or c] at this point
		if hasPrefixAt(glob, "-]", i+1) {
			addSingle(c)
			ranges.ranges = append(ranges.ranges, runeRange{'-', '-'})
			i += 2
			continue
		}
		if hasPrefixAt(glob, "-", i+1) {
			rangeStart = c
			haveRangeStart = true
			i += 2
			continue
		}
		// not the start of a range, just a single character
		addSingle(c)
		i++
	}
	if endPos < i {
		// didn't see the end of the class, not a valid class, but might still be valid as a literal match.
		return noMatch, 0
	}
	// if we got no ranges and no negates, then we have a range that cannot possibly match anything, and that poisons the whole glob
	if !hasRanges && !hasNegs {
		return noMatch, len(glob) - pos
	}
	return &class{negate: negate, hasRanges: hasRanges, hasNegs: hasNegs, ranges: ranges, negs: negs, invertRanges: invert, fold: nocase}, endPos - pos
}
