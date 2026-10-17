package hljs

import "strings"

// reach describes how far a pattern reads past the start of a match attempt. Lines counts the atoms that can match a line break, each one line. A repeated atom that can match a line break and something other than white space reads without bound (unbounded); one that matches only white space reads to the next character that is not (spaceRun).
type reach struct {
	lines     int
	unbounded bool
	spaceRun  bool
}

// patternReach is the reach of a JavaScript pattern. It is conservative: an atom it cannot classify counts as unbounded when repeated.
func patternReach(source string) reach {
	p := reachParser{src: []rune(source)}
	return p.sequence(0)
}

type reachParser struct {
	src []rune
	pos int
	// spaceOnly is set by the last atom read: it can match only white space.
	spaceOnly bool
}

func (a reach) add(b reach) reach {
	return reach{lines: a.lines + b.lines, unbounded: a.unbounded || b.unbounded, spaceRun: a.spaceRun || b.spaceRun}
}

// sequence reads up to the closing parenthesis of the group at depth.
func (p *reachParser) sequence(depth int) (total reach) {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		var atom reach
		p.spaceOnly = false
		switch c {
		case ')':
			if depth > 0 {
				p.pos++
				return total
			}
			p.pos++
			continue
		case '|', '^', '$':
			p.pos++
			continue
		case '(':
			p.pos++
			if p.pos+1 < len(p.src) && p.src[p.pos] == '?' {
				p.pos++
				switch {
				case p.src[p.pos] == ':' || p.src[p.pos] == '=' || p.src[p.pos] == '!':
					p.pos++
				case p.src[p.pos] == '<' && p.pos+1 < len(p.src) && (p.src[p.pos+1] == '=' || p.src[p.pos+1] == '!'):
					p.pos += 2
				case p.src[p.pos] == '<':
					for p.pos < len(p.src) && p.src[p.pos] != '>' {
						p.pos++
					}
					p.pos++
				}
			}
			atom = p.sequence(depth + 1)
			p.spaceOnly = false
		case '[':
			atom.lines = p.class()
		case '\\':
			atom.lines = p.escape()
		case '.':
			p.pos++
		case '*', '+', '?', '{':
			// A quantifier with no atom, a literal brace: nothing to repeat.
			p.pos++
			continue
		default:
			if c == '\n' || c == '\r' || c == 0x2028 || c == 0x2029 {
				atom.lines = 1
				p.spaceOnly = true
			}
			p.pos++
		}
		if atom.lines > 0 && p.repeated() {
			if p.spaceOnly {
				atom.spaceRun = true
			} else {
				atom.unbounded = true
			}
		}
		total = total.add(atom)
	}
	return total
}

// repeated consumes a quantifier and reports whether it can match its atom more than once.
func (p *reachParser) repeated() bool {
	if p.pos >= len(p.src) {
		return false
	}
	repeats := false
	switch p.src[p.pos] {
	case '*', '+':
		p.pos++
		repeats = true
	case '?':
		p.pos++
	case '{':
		end := p.pos
		for end < len(p.src) && p.src[end] != '}' {
			end++
		}
		body := string(p.src[p.pos+1 : min(end, len(p.src))])
		if end < len(p.src) && body != "" && strings.Trim(body, "0123456789,") == "" {
			p.pos = end + 1
			repeats = body != "0" && body != "1" && body != "0,1" && body != "1,1"
		}
	}
	if p.pos < len(p.src) && p.src[p.pos] == '?' {
		p.pos++
	}
	return repeats
}

// escape consumes an escape outside a class and returns 1 when it can match a line break (or a backreference, whose text is unknown).
func (p *reachParser) escape() int {
	p.pos++
	if p.pos >= len(p.src) {
		return 0
	}
	e := p.src[p.pos]
	p.pos++
	switch e {
	case 'n', 'r', 's':
		p.spaceOnly = true
		return 1
	case 'W', 'D', 'x', 'u', 'c', 'k', 'v', 'f', 'p', 'P':
		return 1
	}
	if e >= '0' && e <= '9' {
		return 1
	}
	return 0
}

// class consumes a character class and returns 1 when it can match a line break.
func (p *reachParser) class() int {
	p.pos++
	negated := p.pos < len(p.src) && p.src[p.pos] == '^'
	if negated {
		p.pos++
	}
	// excludesNewline is set by a member that matches "\n": a negated class with one cannot match a line break. Every other line
	// terminator only sets includesBreak, since a class negating "\r" alone still matches "\n".
	excludesNewline, includesBreak, onlySpace := false, false, true
	var previous rune = -1
	for p.pos < len(p.src) && p.src[p.pos] != ']' {
		c := p.src[p.pos]
		switch {
		case c == '\\':
			p.pos++
			if p.pos >= len(p.src) {
				break
			}
			e := p.src[p.pos]
			p.pos++
			previous = -1
			switch e {
			case 'n', 's':
				excludesNewline, includesBreak = true, true
			case 'r':
				includesBreak = true
			case 't', 'b':
			case 'W', 'D', 'x', 'u', 'c', 'v', 'f', 'p', 'P', 'k':
				includesBreak, onlySpace = true, false
			case 'S':
				onlySpace = false
			default:
				onlySpace = false
				if e >= '0' && e <= '9' {
					includesBreak = true
				}
			}
		case c == '-' && previous >= 0 && p.pos+1 < len(p.src) && p.src[p.pos+1] != ']' && p.src[p.pos+1] != '\\':
			high := p.src[p.pos+1]
			for _, t := range [...]rune{'\n', '\r', 0x2028, 0x2029} {
				if previous <= t && t <= high {
					includesBreak = true
				}
			}
			onlySpace = false
			previous = -1
			p.pos += 2
		case c == '-' && previous >= 0 && p.pos+1 < len(p.src) && p.src[p.pos+1] != ']':
			// A range with an escaped end: unknown.
			includesBreak, onlySpace = true, false
			p.pos++
		default:
			switch {
			case c == '\n':
				excludesNewline, includesBreak = true, true
			case c == '\r' || c == 0x2028 || c == 0x2029:
				includesBreak = true
			case c != ' ' && c != '\t':
				onlySpace = false
			}
			previous = c
			p.pos++
		}
	}
	p.pos++
	if negated {
		if excludesNewline {
			return 0
		}
		return 1
	}
	if includesBreak {
		p.spaceOnly = onlySpace
		return 1
	}
	return 0
}
