// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

// A small backtracking matcher for the case-insensitive JavaScript patterns of pi-ai's overflow and retry classifiers
// (utils/overflow.ts, utils/retry.ts). The core cannot import regexp (it uses sync), so the subset those patterns use is
// implemented here: literals, `.`, `\d`, `\s`, character classes of ranges, `\d` and `\w`, groups `(?:…)`, the negative
// lookbehind `(?<!…)` and lookahead `(?!…)`, alternation, the quantifiers `*`, `+` and `?`, and the `^` and `$` anchors. Matching is unanchored search over code points, folding ASCII letters
// only, as the non-Unicode `i` flag does for a pattern of ASCII letters.

type rxKind uint8

const (
	rxChar rxKind = iota
	rxAny
	rxDigit
	rxSpace
	rxClass
	rxGroup
	rxStart
	rxEnd
	rxNotBehind // (?<!…): no match of alts ends at the position
	rxNotAhead  // (?!…): no match of alts starts at the position
)

type rxNode struct {
	kind     rxKind
	ch       rune
	ranges   []rune // rxClass: lo, hi pairs
	digits   bool   // rxClass includes \d
	word     bool   // rxClass includes \w
	alts     [][]rxNode
	min, max int // max < 0: unbounded
	width    int // rxNotBehind: the most code points a match of alts spans; < 0: unbounded
}

type rxProgram struct{ alts [][]rxNode }

// compileRx parses the pattern; it panics on a pattern outside the subset, which only a code change can introduce.
func compileRx(pattern string) *rxProgram {
	p := rxParser{s: []rune(pattern)}
	alts := p.alternation(false)
	if p.i != len(p.s) {
		panic("turn: unsupported pattern " + pattern)
	}
	return &rxProgram{alts: alts}
}

type rxParser struct {
	s []rune
	i int
}

func (p *rxParser) hasPrefix(prefix string) bool {
	rest := p.s[p.i:]
	if len(rest) < len(prefix) {
		return false
	}
	for i, r := range prefix {
		if rest[i] != r {
			return false
		}
	}
	return true
}

// alternation parses `|`-separated sequences; anchored ends each with a `$`, so a lookbehind matches over the text cut at its position.
func (p *rxParser) alternation(anchored bool) [][]rxNode {
	var alts [][]rxNode
	for {
		alts = append(alts, p.sequence(anchored))
		if p.i < len(p.s) && p.s[p.i] == '|' {
			p.i++
			continue
		}
		return alts
	}
}

func (p *rxParser) sequence(anchored bool) []rxNode {
	var seq []rxNode
	for done := false; !done; {
		var n rxNode
		if p.i < len(p.s) && p.s[p.i] != '|' && p.s[p.i] != ')' {
			n = p.atom()
		} else if done = true; anchored {
			n = rxNode{kind: rxEnd}
		} else {
			break
		}
		n.min, n.max = 1, 1
		if !done && p.i < len(p.s) {
			switch p.s[p.i] {
			case '*':
				n.min, n.max = 0, -1
				p.i++
			case '+':
				n.min, n.max = 1, -1
				p.i++
			case '?':
				n.min, n.max = 0, 1
				p.i++
			}
		}
		seq = append(seq, n)
	}
	return seq
}

func (p *rxParser) atom() rxNode {
	c := p.s[p.i]
	p.i++
	switch c {
	case '^':
		return rxNode{kind: rxStart}
	case '$':
		return rxNode{kind: rxEnd}
	case '.':
		return rxNode{kind: rxAny}
	case '(':
		kind := rxGroup
		switch {
		case p.hasPrefix("?:"):
			p.i += 2
		case p.hasPrefix("?<!"):
			p.i += 3
			kind = rxNotBehind
		case p.hasPrefix("?!"):
			p.i += 2
			kind = rxNotAhead
		}
		alts := p.alternation(kind == rxNotBehind)
		p.i++ // ')'
		return rxNode{kind: kind, alts: alts, width: altsWidth(alts)}
	case '[':
		n := rxNode{kind: rxClass}
		for p.s[p.i] != ']' {
			lo := p.s[p.i]
			p.i++
			if lo == '\\' {
				e := p.s[p.i]
				p.i++
				if e == 'd' {
					n.digits = true
					continue
				}
				if e == 'w' {
					n.word = true
					continue
				}
				lo = e
			}
			hi := lo
			if p.s[p.i] == '-' && p.s[p.i+1] != ']' {
				hi = p.s[p.i+1]
				p.i += 2
			}
			n.ranges = append(n.ranges, lo, hi)
		}
		p.i++
		return n
	case '\\':
		e := p.s[p.i]
		p.i++
		switch e {
		case 'd':
			return rxNode{kind: rxDigit}
		case 's':
			return rxNode{kind: rxSpace}
		}
		return rxNode{kind: rxChar, ch: e}
	}
	return rxNode{kind: rxChar, ch: c}
}

func foldASCII(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + 'a' - 'A'
	}
	return r
}

// jsSpace is the JavaScript \s set.
func jsSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\v', '\f', '\r', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

func (n *rxNode) one(r rune) bool {
	switch n.kind {
	case rxChar:
		return foldASCII(r) == foldASCII(n.ch)
	case rxAny:
		return r != '\n' && r != '\r' && r != 0x2028 && r != 0x2029
	case rxDigit:
		return r >= '0' && r <= '9'
	case rxSpace:
		return jsSpace(r)
	case rxClass:
		if n.digits && r >= '0' && r <= '9' {
			return true
		}
		if n.word && (r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_') {
			return true
		}
		f := foldASCII(r)
		for i := 0; i < len(n.ranges); i += 2 {
			lo, hi := n.ranges[i], n.ranges[i+1]
			if (r >= lo && r <= hi) || (f >= foldASCII(lo) && f <= foldASCII(hi) && lo < 128 && hi < 128) {
				return true
			}
		}
	}
	return false
}

// matchRx reports whether the pattern matches anywhere in s.
func (prog *rxProgram) matchRx(s string) bool {
	text := []rune(s)
	for start := 0; start <= len(text); start++ {
		if matchAlts(prog.alts, text, start, func(int) bool { return true }) {
			return true
		}
	}
	return false
}

func matchAlts(alts [][]rxNode, text []rune, pos int, k func(int) bool) bool {
	for _, seq := range alts {
		if matchSeq(seq, text, pos, k) {
			return true
		}
	}
	return false
}

func matchSeq(seq []rxNode, text []rune, pos int, k func(int) bool) bool {
	if len(seq) == 0 {
		return k(pos)
	}
	n := &seq[0]
	rest := seq[1:]
	if n.kind == rxStart {
		return pos == 0 && matchSeq(rest, text, pos, k)
	}
	if n.kind == rxEnd {
		return pos == len(text) && matchSeq(rest, text, pos, k)
	}
	if n.kind == rxNotAhead {
		return !matchAlts(n.alts, text, pos, func(int) bool { return true }) && matchSeq(rest, text, pos, k)
	}
	if n.kind == rxNotBehind {
		return !matchesEndingAt(n.alts, text, pos, n.width) && matchSeq(rest, text, pos, k)
	}
	var try func(count, pos int) bool
	try = func(count, pos int) bool {
		// Greedy: take another repetition first, then continue with the rest.
		if n.max < 0 || count < n.max {
			// An empty repetition (a group matching `$`) still counts toward the minimum, and ends the loop once it is met.
			more := func(next int) bool { return (next != pos || count < n.min) && try(count+1, next) }
			if n.kind == rxGroup {
				if matchAlts(n.alts, text, pos, more) {
					return true
				}
			} else if pos < len(text) && n.one(text[pos]) && try(count+1, pos+1) {
				return true
			}
		}
		return count >= n.min && matchSeq(rest, text, pos, k)
	}
	return try(0, pos)
}

// matchesEndingAt reports whether some match of alts ends at pos. Without captures or backreferences, a match found right to left
// exists exactly when one found left to right does, so the start is tried from pos back over the widest span a match can have.
// The alternatives end in `$` (see alternation), so matching the text cut at pos requires the match to end there.
func matchesEndingAt(alts [][]rxNode, text []rune, pos, width int) bool {
	for start := pos; start >= 0 && (width < 0 || pos-start <= width); start-- {
		if matchAlts(alts, text[:pos], start, func(int) bool { return true }) {
			return true
		}
	}
	return false
}

// altsWidth is the most code points a match of alts spans, or -1 when a repetition is unbounded.
func altsWidth(alts [][]rxNode) int {
	widest := 0
	for _, seq := range alts {
		total := 0
		for i := range seq {
			n := &seq[i]
			w := 0
			switch n.kind {
			case rxGroup:
				if w = altsWidth(n.alts); w < 0 {
					return -1
				}
			case rxStart, rxEnd, rxNotBehind, rxNotAhead:
			default:
				w = 1
			}
			if n.max < 0 && w > 0 {
				return -1
			}
			total += w * n.max
		}
		widest = max(widest, total)
	}
	return widest
}
