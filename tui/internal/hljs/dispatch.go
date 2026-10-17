package hljs

import (
	"math"
	"math/bits"
)

// branchDispatch scans a joined alternation one position at a time and runs only the branches whose first set holds the unit at that position. The joined regular expression tries every branch at every position, which costs microseconds per scanned unit on a grammar with dozens of branches. A branch outside the set cannot match at the position, and the branches left keep their order, so the first one that matches is the one the joined expression picks.
type branchDispatch struct {
	branches   []dispatchBranch
	ascii      [128]uint64
	nonASCII   uint64
	always     uint64
	atEnd      uint64
	full       uint64
	ignoreCase bool
	variants   map[uint64]*jsRegExp
	sources    []string
}

type dispatchBranch struct {
	// start is the number of the branch's own group in the joined expression, groups the capturing groups it holds besides it.
	start, groups int
}

// dispatchEnabled is false only in tests that compare dispatch with the joined expression; it is read when a matcher compiles.
var dispatchEnabled = true

// dispatchMaskBranches is the branches a uint64 mask holds.
var dispatchMaskBranches = bits.OnesCount64(math.MaxUint64)

func newBranchDispatch(rules []multiRegexRule, ignoreCase bool) *branchDispatch {
	if len(rules) < 2 || len(rules) > dispatchMaskBranches {
		return nil
	}
	d := &branchDispatch{ignoreCase: ignoreCase, variants: map[uint64]*jsRegExp{}}
	group := 1
	for i, rule := range rules {
		source, _ := rule.re.source()
		groups := countMatchGroups(rule.re)
		d.branches = append(d.branches, dispatchBranch{start: group, groups: groups})
		d.sources = append(d.sources, source)
		group += groups + 1
		bit := uint64(1) << i
		d.full |= bit
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

// exec is exec(re, start) for the joined expression re of the branches: the leftmost match at or after start.
func (d *branchDispatch) exec(re *jsRegExp, input []rune, start, groups int) *jsMatch {
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
		matcher := re
		if mask != d.full {
			matcher = d.variant(mask)
		}
		m := matcher.execAt(input, p)
		if m == nil {
			continue
		}
		if mask == d.full {
			return m
		}
		return d.remap(mask, m, groups)
	}
	return nil
}

// variant is the joined expression of the branches in mask, in their order; backreferences are renumbered by join.
func (d *branchDispatch) variant(mask uint64) *jsRegExp {
	if re := d.variants[mask]; re != nil {
		return re
	}
	var subset []pattern
	for rest := mask; rest != 0; rest &= rest - 1 {
		subset = append(subset, stringPattern(d.sources[bits.TrailingZeros64(rest)]))
	}
	re, err := newAnchoredJSRegExp(join(subset), d.ignoreCase)
	if err != nil {
		throw(err.Error())
	}
	d.variants[mask] = re
	return re
}

// remap renumbers the groups of a match of the branches in mask to the numbers they have in the joined expression of every branch, which holds groups capturing groups.
func (d *branchDispatch) remap(mask uint64, m *jsMatch, groups int) *jsMatch {
	spans := make([]int, 2*(groups+1))
	for i := range spans {
		spans[i] = -1
	}
	spans[0], spans[1] = m.spans[0], m.spans[1]
	from := 1
	for rest := mask; rest != 0; rest &= rest - 1 {
		b := d.branches[bits.TrailingZeros64(rest)]
		n := b.groups + 1
		copy(spans[2*b.start:2*(b.start+n)], m.spans[2*from:2*(from+n)])
		from += n
	}
	return &jsMatch{index: m.index, spans: spans}
}
