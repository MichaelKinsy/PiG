package ecmaregex

import (
	"strconv"
	"strings"
)

// Translates a parsed pattern into the syntax of github.com/dlclark/regexp2 compiled with regexp2.ECMAScript: that mode reads \b as the ASCII word boundary and lets a reference to a group that holds no capture match the empty string, as ECMA-262 does. Every character is written as an escape, so no character of the source can be read as syntax, and every set is written as explicit ranges, so no class of regexp2 is relied on; a set is never written as a negated class, which regexp2 misjudges for some code points above U+FFFF.

// wordClass is the ASCII word characters; regexp2's own \b follows the Unicode definition even in ECMAScript mode, so \b and \B are written with lookarounds around this class.
const wordClass = `[\u0030-\u0039A-Z\u005Fa-z]`

type emitter struct {
	out strings.Builder
}

func emit(root *node) string {
	e := &emitter{}
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
		e.backref(n.refs)
	case kindAssert:
		switch n.assertion {
		case '^':
			e.out.WriteString(`\A`)
		case '$':
			e.out.WriteString(`\z`)
		case 'b':
			e.out.WriteString(`(?:(?<=` + wordClass + `)(?!` + wordClass + `)|(?<!` + wordClass + `)(?=` + wordClass + `))`)
		default:
			e.out.WriteString(`(?:(?<=` + wordClass + `)(?=` + wordClass + `)|(?<!` + wordClass + `)(?!` + wordClass + `))`)
		}
	case kindRepeat:
		e.repeat(n)
	}
}

// backref writes a reference to the group refs[0] or, when several groups share the name, to whichever of them holds a capture, and to nothing when none does.
func (e *emitter) backref(refs []int) {
	for _, ref := range refs[:len(refs)-1] {
		e.out.WriteString("(?(" + strconv.Itoa(ref) + `)\` + strconv.Itoa(ref) + "|")
	}
	e.out.WriteString(`\` + strconv.Itoa(refs[len(refs)-1]))
	e.out.WriteString(strings.Repeat(")", len(refs)-1))
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

// char writes one character as a form regexp2 reads as that character, inside or outside a class.
func (e *emitter) char(c rune) {
	const hex = "0123456789abcdef"
	switch {
	case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
		e.out.WriteRune(c)
	case c < 0x10000:
		e.out.WriteString(`\u`)
		e.out.WriteByte(hex[c>>12])
		e.out.WriteByte(hex[c>>8&0xF])
		e.out.WriteByte(hex[c>>4&0xF])
		e.out.WriteByte(hex[c&0xF])
	default:
		// regexp2 has no escape for a code point above U+FFFF; the character itself is no syntax.
		e.out.WriteRune(c)
	}
}

func (e *emitter) set(s runeSet) {
	if len(s) == 0 {
		e.out.WriteString("(?!)")
		return
	}
	if len(s) == 1 && s[0].lo == s[0].hi {
		e.char(s[0].lo)
		return
	}
	e.out.WriteByte('[')
	e.ranges(s)
	e.out.WriteByte(']')
}

func (e *emitter) ranges(s runeSet) {
	for _, r := range s {
		e.char(r.lo)
		if r.hi != r.lo {
			e.out.WriteByte('-')
			e.char(r.hi)
		}
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
