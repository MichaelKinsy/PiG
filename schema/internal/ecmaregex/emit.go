package ecmaregex

import (
	"strconv"
	"strings"
)

// Translates a parsed pattern into the syntax of github.com/dlclark/regexp2 compiled with regexp2.ECMAScript: that mode lets a reference to a group that holds no capture match the empty string, as ECMA-262 does. The result runs over UTF-16 code units, like V8's own matcher. Every character is written as an escape, so no character of the source can be read as syntax, and every set is written as explicit ranges, so no class of regexp2 is relied on; a set is never written as a negated class, which regexp2 misjudges for some code points above U+FFFF. With the u flag a character above U+FFFF is a lead and a trail unit, and a set that holds such characters becomes an alternation of a class of BMP units and one class pair per group of leads; a lone surrogate is a unit that the neighbouring unit does not complete into a pair, which a guard checks.

// Word characters are the ASCII letters, digits and "_"; \b and \B with the u and i flags add the two characters that fold onto them.
const (
	wordClass     = `[\u0030-\u0039A-Z\u005Fa-z]`
	foldWordClass = `[\u0030-\u0039A-Z\u005Fa-z\u017f\u212a]`
	// lineTerminatorClass is the class of the characters "^" and "$" see as line ends when they are multiline.
	lineTerminatorClass = `[\u000a\u000d\u2028\u2029]`
)

type emitter struct {
	out strings.Builder
	// unicode is the u flag.
	unicode bool
	// marker counts the classes that needed a marker member.
	marker rune
}

func emit(root *node, unicode bool) string {
	e := &emitter{unicode: unicode}
	e.node(root)
	return e.out.String()
}

func (e *emitter) node(n *node) {
	switch n.kind {
	case kindEmpty:
	case kindSet:
		e.set(n.set)
	case kindSeq:
		for _, child := range n.children {
			e.node(child)
		}
	case kindAlt:
		for i, child := range n.children {
			if i > 0 {
				e.out.WriteByte('|')
			}
			e.node(child)
		}
	case kindCapture:
		e.out.WriteByte('(')
		e.node(n.children[0])
		e.out.WriteByte(')')
	case kindGroup:
		e.out.WriteString("(?:")
		e.node(n.children[0])
		e.out.WriteByte(')')
	case kindLook:
		e.out.WriteString("(?")
		if !n.ahead {
			e.out.WriteByte('<')
		}
		if n.negated {
			e.out.WriteByte('!')
		} else {
			e.out.WriteByte('=')
		}
		e.node(n.children[0])
		e.out.WriteByte(')')
	case kindBackref:
		e.backref(n)
	case kindAssert:
		e.assertion(n)
	case kindRepeat:
		e.repeat(n)
	}
}

func (e *emitter) assertion(n *node) {
	word := wordClass
	if n.fold {
		word = foldWordClass
	}
	switch {
	case n.assertion == '^' && n.multiline:
		e.out.WriteString(`(?:\A|(?<=` + lineTerminatorClass + `))`)
	case n.assertion == '^':
		e.out.WriteString(`\A`)
	case n.assertion == '$' && n.multiline:
		e.out.WriteString(`(?:\z|(?=` + lineTerminatorClass + `))`)
	case n.assertion == '$':
		e.out.WriteString(`\z`)
	case n.assertion == 'b':
		e.out.WriteString(`(?:(?<=` + word + `)(?!` + word + `)|(?<!` + word + `)(?=` + word + `))`)
	default:
		e.out.WriteString(`(?:(?<=` + word + `)(?=` + word + `)|(?<!` + word + `)(?!` + word + `))`)
	}
}

// pig divergence (D219): a backreference that ignores case is compared by regexp2 with unicode.ToLower, where V8 compares by Canonicalize; the three Annex B
// patterns and the one-byte-storage results of TestKnownGaps differ from V8 for the reasons D219 gives.
// backref writes a reference to the group refs[0] or, when several groups share the name, to whichever of them holds a capture, and to nothing when none does. A reference that ignores case is read by regexp2's own case-insensitive comparison.
func (e *emitter) backref(n *node) {
	reference := func(group int) string {
		if n.fold {
			return `(?i:\` + strconv.Itoa(group) + ")"
		}
		return `\` + strconv.Itoa(group)
	}
	refs := n.refs
	for _, ref := range refs[:len(refs)-1] {
		e.out.WriteString("(?(" + strconv.Itoa(ref) + ")")
		e.reference(reference(ref))
		e.out.WriteString("|")
	}
	e.reference(reference(refs[len(refs)-1]))
	e.out.WriteString(strings.Repeat(")", len(refs)-1))
}

// reference writes the match of a reference. With the u flag a reference never starts or ends inside a surrogate pair, so a lone lead surrogate that a group captured cannot match the lead of a pair.
func (e *emitter) reference(text string) {
	if !e.unicode {
		e.out.WriteString(text)
		return
	}
	e.out.WriteString("(?:")
	e.pairBoundary()
	e.out.WriteString(text)
	e.pairBoundary()
	e.out.WriteByte(')')
}

// pairBoundary asserts that the position is not between the two halves of a surrogate pair.
func (e *emitter) pairBoundary() {
	e.out.WriteString("(?!(?<=")
	e.class([]runeRange{{0xD800, 0xDBFF}})
	e.out.WriteByte(')')
	e.class([]runeRange{{0xDC00, 0xDFFF}})
	e.out.WriteByte(')')
}

func (e *emitter) repeat(n *node) {
	body := n.children[0]
	// Every iteration of a repeat starts with all the groups of its body unset, but regexp2 keeps the captures of earlier iterations. A balancing group (?<-N>) removes the last capture of group N, and the atomic group keeps the removal from being backtracked into.
	var groups []int
	if n.max < 0 || n.max > 1 {
		groups = captureIndexes(body, nil)
	}
	switch {
	case len(groups) > 0:
		e.out.WriteString("(?:(?>")
		for _, group := range groups {
			e.out.WriteString("(?<-" + strconv.Itoa(group) + ">)?")
		}
		e.out.WriteByte(')')
		e.node(body)
		e.out.WriteByte(')')
	case body.kind == kindLook:
		e.out.WriteString("(?:")
		e.node(body)
		e.out.WriteByte(')')
	default:
		e.node(body)
	}
	switch {
	case n.min == 0 && n.max < 0:
		e.out.WriteByte('*')
	case n.min == 1 && n.max < 0:
		e.out.WriteByte('+')
	case n.min == 0 && n.max == 1:
		e.out.WriteByte('?')
	case n.max < 0:
		e.out.WriteString("{" + strconv.Itoa(n.min) + ",}")
	case n.min == n.max:
		e.out.WriteString("{" + strconv.Itoa(n.min) + "}")
	default:
		e.out.WriteString("{" + strconv.Itoa(n.min) + "," + strconv.Itoa(n.max) + "}")
	}
	if !n.greedy {
		e.out.WriteByte('?')
	}
}

// captureIndexes appends the numbers of the capturing groups inside n.
func captureIndexes(n *node, out []int) []int {
	if n.kind == kindCapture {
		out = append(out, n.index)
	}
	for _, child := range n.children {
		out = captureIndexes(child, out)
	}
	return out
}

// unit writes one UTF-16 code unit as a form regexp2 reads as that unit, inside or outside a class.
func (e *emitter) unit(c rune) {
	const hex = "0123456789abcdef"
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
		e.out.WriteRune(c)
		return
	}
	e.out.WriteString(`\u`)
	e.out.WriteByte(hex[c>>12])
	e.out.WriteByte(hex[c>>8&0xF])
	e.out.WriteByte(hex[c>>4&0xF])
	e.out.WriteByte(hex[c&0xF])
}

// class writes ranges of code units as one class, or as the unit itself when there is only one.
//
// regexp2 keeps its sets and literal strings in tables keyed by a string of their runes, and a surrogate becomes U+FFFD there, so two different sets or strings of surrogates would share one entry. A class with a surrogate end therefore gets one more member, a character above U+FFFF that no unit can be, which is different in each class and keeps the class apart.
func (e *emitter) class(ranges []runeRange) {
	surrogate := false
	for _, r := range ranges {
		surrogate = surrogate || (r.lo >= 0xD800 && r.lo <= 0xDFFF) || (r.hi >= 0xD800 && r.hi <= 0xDFFF)
	}
	if len(ranges) == 1 && ranges[0].lo == ranges[0].hi && !surrogate {
		e.unit(ranges[0].lo)
		return
	}
	e.out.WriteByte('[')
	for _, r := range ranges {
		e.unit(r.lo)
		if r.hi != r.lo {
			e.out.WriteByte('-')
			e.unit(r.hi)
		}
	}
	if surrogate {
		e.marker++
		e.out.WriteRune(0x10000 + e.marker)
	}
	e.out.WriteByte(']')
}

// between is the part of s from lo to hi.
func between(s runeSet, lo, hi rune) runeSet {
	var outside runeSet
	if lo > 0 {
		outside = append(outside, runeRange{0, lo - 1})
	}
	if hi < maxCodePoint {
		outside = append(outside, runeRange{hi + 1, maxCodePoint})
	}
	return s.minus(outside)
}

// set writes a set of characters as one atom. With the u flag a lead surrogate that no trail surrogate follows is a character of its own, and so is a trail surrogate that no lead surrogate precedes; a pair of them is one character above U+FFFF.
func (e *emitter) set(s runeSet) {
	if len(s) == 0 {
		e.out.WriteString("(?!)")
		return
	}
	if !e.unicode {
		e.class(s)
		return
	}
	leads, trails := between(s, 0xD800, 0xDBFF), between(s, 0xDC00, 0xDFFF)
	bmp := between(s, 0, 0xD7FF).union(between(s, 0xE000, 0xFFFF))
	pairs := astralPairs(s.minus(runeSet{{0, maxUnit}}))
	if len(leads) == 0 && len(trails) == 0 && len(pairs) == 0 {
		e.class(bmp)
		return
	}
	var alternatives []func()
	if len(bmp) > 0 {
		alternatives = append(alternatives, func() { e.class(bmp) })
	}
	if len(leads) > 0 {
		// The guard reads the unit after the lead in either direction: when the atom is matched backwards it runs first.
		alternatives = append(alternatives, func() {
			e.class(leads)
			e.out.WriteString("(?!")
			e.class([]runeRange{{0xDC00, 0xDFFF}})
			e.out.WriteByte(')')
		})
	}
	if len(trails) > 0 {
		alternatives = append(alternatives, func() {
			e.out.WriteString("(?<!")
			e.class([]runeRange{{0xD800, 0xDBFF}})
			e.out.WriteByte(')')
			e.class(trails)
		})
	}
	switch len(pairs) {
	case 0:
	case 1:
		alternatives = append(alternatives, func() {
			e.class(pairs[0].leads)
			e.class(pairs[0].trails)
		})
	default:
		// One class of every lead first, so that a unit that is no lead fails at once; the trail class that fits the lead is then chosen by what lies behind it.
		alternatives = append(alternatives, func() {
			var all []runeRange
			for _, pair := range pairs {
				all = append(all, pair.leads...)
			}
			e.class(newRuneSet(all))
			e.out.WriteString("(?:")
			for i, pair := range pairs {
				if i > 0 {
					e.out.WriteByte('|')
				}
				e.out.WriteString("(?<=")
				e.class(pair.leads)
				e.out.WriteByte(')')
				e.class(pair.trails)
			}
			e.out.WriteByte(')')
		})
	}
	e.out.WriteString("(?:")
	for i, alternative := range alternatives {
		if i > 0 {
			e.out.WriteByte('|')
		}
		alternative()
	}
	e.out.WriteByte(')')
}

// A surrogatePair is the characters above U+FFFF whose lead unit is in leads and whose trail unit is in trails.
type surrogatePair struct{ leads, trails []runeRange }

// astralPairs splits ranges of characters above U+FFFF into pairs of a class of leads and a class of trails, with as few pairs as the leads allow.
func astralPairs(s runeSet) []surrogatePair {
	// trails[i] holds the trail units that follow the lead unit 0xD800+i.
	var trails [1024][]runeRange
	for _, r := range s {
		firstLead, firstTrail := (r.lo-0x10000)>>10, (r.lo-0x10000)&0x3FF
		lastLead, lastTrail := (r.hi-0x10000)>>10, (r.hi-0x10000)&0x3FF
		for lead := firstLead; lead <= lastLead; lead++ {
			lo, hi := rune(0), rune(0x3FF)
			if lead == firstLead {
				lo = firstTrail
			}
			if lead == lastLead {
				hi = lastTrail
			}
			list := trails[lead]
			if n := len(list); n > 0 && list[n-1].hi+1 == 0xDC00+lo {
				list[n-1].hi = 0xDC00 + hi
			} else {
				list = append(list, runeRange{0xDC00 + lo, 0xDC00 + hi})
			}
			trails[lead] = list
		}
	}
	// Leads whose trails are the same share a pair.
	var pairs []surrogatePair
	byTrails := map[string]int{}
	for lead := range trails {
		if len(trails[lead]) == 0 {
			continue
		}
		var key strings.Builder
		for _, r := range trails[lead] {
			key.WriteString(strconv.Itoa(int(r.lo)) + "-" + strconv.Itoa(int(r.hi)) + ",")
		}
		i, ok := byTrails[key.String()]
		if !ok {
			i = len(pairs)
			byTrails[key.String()] = i
			pairs = append(pairs, surrogatePair{trails: trails[lead]})
		}
		unit := rune(0xD800 + lead)
		leads := pairs[i].leads
		if n := len(leads); n > 0 && leads[n-1].hi+1 == unit {
			leads[n-1].hi = unit
		} else {
			leads = append(leads, runeRange{unit, unit})
		}
		pairs[i].leads = leads
	}
	return pairs
}
