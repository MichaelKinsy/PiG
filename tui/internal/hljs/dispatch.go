package hljs

import (
	"math"
	"math/bits"
	"slices"
)

// branchDispatch scans a joined alternation one position at a time and runs only the branches whose first set holds the unit at that position. The joined regular expression tries every branch at every position, which costs microseconds per scanned unit on a grammar with dozens of branches. A branch outside the set cannot match at the position, and the branches left keep their order, so the first one that matches is the one the joined expression picks.
type branchDispatch struct {
	rules    []multiRegexRule
	ascii    [128]uint64
	nonASCII uint64
	always   uint64
	atEnd    uint64
	full     uint64
	// empty holds the branches that match the empty string everywhere (matchesEmptyEverywhere), and plain those whose pattern is a plain text (literals).
	empty      uint64
	plain      uint64
	literals   []*dispatchLiteral
	ignoreCase bool
	variants   map[uint64]*dispatchVariant
}

// dispatchVariant is the joined expression of the branches of one mask, in their order, and the number of each branch's group in it.
type dispatchVariant struct {
	re     *jsRegExp
	groups []int
}

// dispatchEnabled is false only in tests that compare dispatch with the joined expression; it is read when a matcher compiles.
var dispatchEnabled = true

// dispatchMaskBranches is the branches a uint64 mask holds.
var dispatchMaskBranches = bits.OnesCount64(math.MaxUint64)

// newBranchDispatch is the dispatch of a matcher's rules, whose groups are numbered (multiRegex.compile).
func newBranchDispatch(rules []multiRegexRule, ignoreCase bool) *branchDispatch {
	if len(rules) < 2 || len(rules) > dispatchMaskBranches {
		return nil
	}
	d := &branchDispatch{rules: rules, ignoreCase: ignoreCase, variants: map[uint64]*dispatchVariant{}, literals: make([]*dispatchLiteral, len(rules))}
	for i, rule := range rules {
		source, _ := rule.re.source()
		bit := uint64(1) << i
		d.full |= bit
		if source == matchesEmptyEverywhere {
			d.empty |= bit
		}
		if d.literals[i] = newDispatchLiteral(source, ignoreCase); d.literals[i] != nil {
			d.plain |= bit
		}
		f := patternFirst(source, ignoreCase)
		if f.nullable {
			d.always |= bit
		}
		if f.atEnd {
			d.atEnd |= bit
		}
		if f.nonASCII {
			d.nonASCII |= bit
		}
		for c := range rune(128) {
			if f.has(c) {
				d.ascii[c] |= bit
			}
		}
	}
	return d
}

// exec is exec(re, start) for the joined expression re of the branches: the leftmost match at or after start, the branch that matched and the number of its group in the expression the match is of.
func (d *branchDispatch) exec(re *jsRegExp, input []rune, start int) (match jsMatch, rule, group int, ok bool) {
	for p := start; p <= len(input); p++ {
		mask := d.always
		if p == len(input) {
			mask |= d.atEnd
		} else if c := input[p]; c < 128 {
			mask |= d.ascii[c]
		} else {
			mask |= d.nonASCII
		}
		if mask == 0 {
			continue
		}
		if mask&^(d.empty|d.plain) == 0 {
			// Every branch left is a plain text or matches the empty string: the first of them that matches is found by comparing units, and no expression is run.
			for rest := mask; rest != 0; rest &= rest - 1 {
				rule = bits.TrailingZeros64(rest)
				if literal := d.literals[rule]; literal == nil {
					return emptyMatch(p), rule, d.rules[rule].group, true
				} else if literal.at(input, p) {
					return jsMatch{index: p, end: p + len(literal.units)}, rule, d.rules[rule].group, true
				}
			}
			continue
		}
		if mask == d.full {
			if m, found := re.execAt(input, p); found {
				rule, group = matchedRule(m, d.rules)
				return m, rule, group, true
			}
			continue
		}
		variant := d.variant(mask)
		m, found := variant.re.execAt(input, p)
		if !found {
			continue
		}
		if mask&(mask-1) == 0 {
			return m, bits.TrailingZeros64(mask), variant.groups[0], true
		}
		n := 0
		for rest := mask; rest != 0; rest &= rest - 1 {
			if _, _, took := m.span(variant.groups[n]); took {
				return m, bits.TrailingZeros64(rest), variant.groups[n], true
			}
			n++
		}
		return m, -1, 0, true
	}
	return jsMatch{}, -1, 0, false
}

// variant is the joined expression of the branches in mask, in their order; backreferences are renumbered by joinRules.
func (d *branchDispatch) variant(mask uint64) *dispatchVariant {
	if variant := d.variants[mask]; variant != nil {
		return variant
	}
	var subset []multiRegexRule
	for rest := mask; rest != 0; rest &= rest - 1 {
		subset = append(subset, d.rules[bits.TrailingZeros64(rest)])
	}
	source, groups := joinRules(subset)
	re, err := newAnchoredJSRegExp(source, d.ignoreCase)
	if err != nil {
		throw(err.Error())
	}
	variant := &dispatchVariant{re: re, groups: groups}
	d.variants[mask] = variant
	return variant
}

// dispatchLiteral is a branch whose pattern is a plain text: units is the text and folds, under ignoreCase, the other units that each of its units matches (caseClass, which is what the pattern's expression holds for the unit).
type dispatchLiteral struct {
	units []rune
	folds [][]rune
}

// newDispatchLiteral is the plain text of a pattern that is one: units that stand for themselves and escaped ASCII punctuation, nothing else. Any other pattern is nil.
func newDispatchLiteral(source string, ignoreCase bool) *dispatchLiteral {
	src := toUnits(source)
	units := make([]rune, 0, len(src))
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch c {
		case '^', '$', '.', '|', '?', '*', '+', '(', ')', '[', ']', '{', '}':
			return nil
		case '\\':
			i++
			if i >= len(src) {
				return nil
			}
			c = src[i]
			if c <= ' ' || c >= 0x7f || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' {
				return nil
			}
		}
		units = append(units, c)
	}
	if len(units) == 0 {
		return nil
	}
	literal := &dispatchLiteral{units: units}
	if ignoreCase {
		literal.folds = make([][]rune, len(units))
		for i, c := range units {
			literal.folds[i] = caseClass(c)
		}
	}
	return literal
}

// at reports whether the units of input at p spell the text.
func (l *dispatchLiteral) at(input []rune, p int) bool {
	if len(input)-p < len(l.units) {
		return false
	}
	for i, c := range l.units {
		if u := input[p+i]; u != c && (l.folds == nil || !slices.Contains(l.folds[i], u)) {
			return false
		}
	}
	return true
}
