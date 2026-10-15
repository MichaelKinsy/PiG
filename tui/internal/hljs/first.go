package hljs

// firstSet over-approximates the code units a pattern can consume first: ascii has a bit per unit below 128, nonASCII stands for every other unit, and nullable is set when the pattern can match without consuming. A pattern can match at position p only if it is nullable or its set holds the unit at p, so a joined alternation tries at p only the branches whose set holds the unit there.
type firstSet struct {
	ascii    [2]uint64
	nonASCII bool
	nullable bool
	// atEnd is set when a pattern with a non-nullable condition on the next unit (a line end, a lookahead) can still match at the end of the text; it is only set by a top-level sequence.
	atEnd bool
	// zero marks a zero-width assertion, and filter one whose condition on the next unit is the set: a lookahead of a non-nullable pattern, or $. They constrain a top-level sequence that starts with them.
	zero, filter bool
}

func (f *firstSet) add(c rune) {
	if c < 0 || c >= 128 {
		f.nonASCII = true
		return
	}
	f.ascii[c>>6] |= 1 << (c & 63)
}

func (f *firstSet) addRange(lo, hi rune) {
	for c := lo; c <= hi && c < 128; c++ {
		f.add(c)
	}
	if hi >= 128 {
		f.nonASCII = true
	}
}

func (f *firstSet) has(c rune) bool { return f.ascii[c>>6]&(1<<(c&63)) != 0 }

// merge adds the units of o; nullable is left alone.
func (f *firstSet) merge(o firstSet) {
	f.ascii[0] |= o.ascii[0]
	f.ascii[1] |= o.ascii[1]
	f.nonASCII = f.nonASCII || o.nonASCII
}

func (f *firstSet) intersect(o firstSet) {
	f.ascii[0] &= o.ascii[0]
	f.ascii[1] &= o.ascii[1]
	f.nonASCII = f.nonASCII && o.nonASCII
}

func (f *firstSet) complement() {
	f.ascii[0], f.ascii[1] = ^f.ascii[0], ^f.ascii[1]
	f.nonASCII = true
}

func anyFirst() firstSet {
	return firstSet{ascii: [2]uint64{^uint64(0), ^uint64(0)}, nonASCII: true, nullable: true}
}

// patternFirst is the first set of a JavaScript pattern source, anyFirst when the analysis does not understand the pattern.
func patternFirst(source string, ignoreCase bool) firstSet {
	p := &firstParser{src: toUnits(source), ignoreCase: ignoreCase}
	f := p.alternation()
	if p.bad || p.pos != len(p.src) {
		return anyFirst()
	}
	return f
}

type firstParser struct {
	depth      int
	src        []rune
	pos        int
	ignoreCase bool
	bad        bool
}

func (p *firstParser) more() bool { return p.pos < len(p.src) }

func (p *firstParser) alternation() firstSet {
	f := p.sequence()
	for p.more() && p.src[p.pos] == '|' {
		p.pos++
		g := p.sequence()
		f.merge(g)
		f.nullable = f.nullable || g.nullable
		f.atEnd = f.atEnd || g.atEnd
	}
	return f
}

func (p *firstParser) sequence() firstSet {
	f := firstSet{nullable: true}
	var condition *firstSet
	zero := true
	for p.more() && !p.bad {
		if c := p.src[p.pos]; c == '|' || c == ')' {
			break
		}
		t := p.term()
		if p.depth == 0 && zero && t.filter {
			if condition == nil {
				c := firstSet{ascii: t.ascii, nonASCII: t.nonASCII, atEnd: t.atEnd}
				condition = &c
			} else {
				condition.intersect(t)
				condition.atEnd = condition.atEnd && t.atEnd
			}
		}
		zero = zero && t.zero
		if f.nullable && !t.filter {
			f.merge(t)
		}
		f.nullable = f.nullable && t.nullable
	}
	if condition != nil {
		if f.nullable {
			f.ascii, f.nonASCII, f.atEnd, f.nullable = condition.ascii, condition.nonASCII, condition.atEnd, false
		} else {
			f.intersect(*condition)
		}
	}
	return f
}

func (p *firstParser) term() firstSet {
	t := p.atom()
	if !p.more() {
		return t
	}
	switch p.src[p.pos] {
	case '*', '?':
		p.pos++
		t.nullable = true
	case '+':
		p.pos++
	case '{':
		min, ok := p.braceQuantifier()
		if !ok {
			return t
		}
		if min == 0 {
			t.nullable = true
		}
	default:
		return t
	}
	t.zero, t.filter = false, false
	if p.more() && p.src[p.pos] == '?' {
		p.pos++
	}
	return t
}

// braceQuantifier consumes {n}, {n,} or {n,m} and returns n. Anything else is a literal brace (Annex B) and is left for atom.
func (p *firstParser) braceQuantifier() (int, bool) {
	i := p.pos + 1
	n, digits := 0, 0
	for i < len(p.src) && p.src[i] >= '0' && p.src[i] <= '9' {
		n = min(n*10+int(p.src[i]-'0'), 1<<20)
		i++
		digits++
	}
	if digits == 0 {
		return 0, false
	}
	if i < len(p.src) && p.src[i] == ',' {
		i++
		for i < len(p.src) && p.src[i] >= '0' && p.src[i] <= '9' {
			i++
		}
	}
	if i >= len(p.src) || p.src[i] != '}' {
		return 0, false
	}
	p.pos = i + 1
	return n, true
}

func (p *firstParser) literal(c rune) firstSet {
	f := firstSet{}
	f.add(c)
	if p.ignoreCase && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
		f.add(c ^ 0x20)
		f.nonASCII = true
	}
	return f
}

func (p *firstParser) atom() firstSet {
	c := p.src[p.pos]
	switch c {
	case '(':
		return p.group()
	case '^':
		p.pos++
		return firstSet{nullable: true, zero: true}
	case '$':
		p.pos++
		f := firstSet{nullable: true, zero: true, filter: true, atEnd: true, nonASCII: true}
		f.add('\n')
		f.add('\r')
		return f
	case '.':
		p.pos++
		f := firstSet{nonASCII: true}
		f.addRange(0, 127)
		f.ascii[0] &^= 1<<'\n' | 1<<'\r'
		return f
	case '[':
		return p.class()
	case '\\':
		return p.escape()
	}
	p.pos++
	return p.literal(c)
}

func (p *firstParser) group() firstSet {
	p.pos++
	lookaround, ahead := false, false
	if p.pos+1 < len(p.src) && p.src[p.pos] == '?' {
		switch p.src[p.pos+1] {
		case ':':
			p.pos += 2
		case '=', '!':
			ahead = p.src[p.pos+1] == '='
			p.pos += 2
			lookaround = true
		case '<':
			if p.pos+2 < len(p.src) && (p.src[p.pos+2] == '=' || p.src[p.pos+2] == '!') {
				p.pos += 3
				lookaround = true
			} else {
				for p.more() && p.src[p.pos] != '>' {
					p.pos++
				}
				p.pos++
			}
		default:
			p.bad = true
			return anyFirst()
		}
	}
	p.depth++
	f := p.alternation()
	p.depth--
	if !p.more() || p.src[p.pos] != ')' {
		p.bad = true
		return anyFirst()
	}
	p.pos++
	if lookaround {
		if ahead && !f.nullable {
			return firstSet{ascii: f.ascii, nonASCII: f.nonASCII, nullable: true, zero: true, filter: true}
		}
		return firstSet{nullable: true, zero: true}
	}
	return f
}

// classEscape is the set an escape outside or inside a class stands for, and ok false for an escape that stands for one unit, which unit reports.
func (p *firstParser) classEscape(e rune) (set firstSet, unit rune, isSet bool) {
	switch e {
	case 'd', 'D':
		set.addRange('0', '9')
	case 'w', 'W':
		set.addRange('0', '9')
		set.addRange('a', 'z')
		set.addRange('A', 'Z')
		set.add('_')
	case 's', 'S':
		for _, c := range "\t\n\v\f\r " {
			set.add(c)
		}
		set.nonASCII = true
	default:
		return set, 0, false
	}
	if e == 'D' || e == 'W' {
		set.complement()
	}
	if e == 'S' {
		set.complement()
	}
	return set, 0, true
}

// escapeUnit reads the escape after a backslash that stands for a single unit, or reports ok false.
func (p *firstParser) escapeUnit(e rune) (rune, bool) {
	switch e {
	case 'n':
		return '\n', true
	case 'r':
		return '\r', true
	case 't':
		return '\t', true
	case 'v':
		return '\v', true
	case 'f':
		return '\f', true
	case '0':
		return 0, true
	case 'x':
		if v, ok := p.hex(2); ok {
			return v, true
		}
		return 'x', true
	case 'u':
		if v, ok := p.hex(4); ok {
			return v, true
		}
		return 'u', true
	case 'c':
		if p.more() && (p.src[p.pos] >= 'a' && p.src[p.pos] <= 'z' || p.src[p.pos] >= 'A' && p.src[p.pos] <= 'Z') {
			v := p.src[p.pos] % 32
			p.pos++
			return v, true
		}
		return 0, false
	}
	if e >= '1' && e <= '9' || e == 'k' {
		return 0, false
	}
	return e, true
}

func (p *firstParser) hex(n int) (rune, bool) {
	if p.pos+n > len(p.src) {
		return 0, false
	}
	var v rune
	for _, c := range p.src[p.pos : p.pos+n] {
		switch {
		case c >= '0' && c <= '9':
			v = v*16 + c - '0'
		case c >= 'a' && c <= 'f':
			v = v*16 + c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v = v*16 + c - 'A' + 10
		default:
			return 0, false
		}
	}
	p.pos += n
	return v, true
}

func (p *firstParser) escape() firstSet {
	p.pos++
	if !p.more() {
		p.bad = true
		return anyFirst()
	}
	e := p.src[p.pos]
	p.pos++
	switch e {
	case 'b', 'B':
		return firstSet{nullable: true, zero: true}
	}
	if set, _, ok := p.classEscape(e); ok {
		return set
	}
	if u, ok := p.escapeUnit(e); ok {
		return p.literal(u)
	}
	return anyFirst()
}

func (p *firstParser) class() firstSet {
	p.pos++
	negated := p.more() && p.src[p.pos] == '^'
	if negated {
		p.pos++
	}
	f := firstSet{}
	for p.more() && p.src[p.pos] != ']' {
		lo, set, isSet := p.classAtom()
		if isSet {
			f.merge(set)
			continue
		}
		if p.pos+1 < len(p.src) && p.src[p.pos] == '-' && p.src[p.pos+1] != ']' {
			p.pos++
			hi, hset, hIsSet := p.classAtom()
			if hIsSet {
				f.add(lo)
				f.add('-')
				f.merge(hset)
				continue
			}
			if hi < lo {
				p.bad = true
				return anyFirst()
			}
			f.addRange(lo, hi)
			continue
		}
		f.add(lo)
	}
	if !p.more() {
		p.bad = true
		return anyFirst()
	}
	p.pos++
	if p.ignoreCase {
		for c := rune('a'); c <= 'z'; c++ {
			if f.has(c) || f.has(c-32) {
				f.add(c)
				f.add(c - 32)
				f.nonASCII = true
			}
		}
	}
	if negated {
		f.complement()
	}
	return f
}

// classAtom reads one class member: a unit, or a set for \d, \w and \s forms.
func (p *firstParser) classAtom() (unit rune, set firstSet, isSet bool) {
	if !p.more() {
		p.bad = true
		return 0, set, false
	}
	c := p.src[p.pos]
	p.pos++
	if c != '\\' {
		return c, set, false
	}
	if !p.more() {
		p.bad = true
		return 0, set, false
	}
	e := p.src[p.pos]
	p.pos++
	if e == 'b' {
		return '\b', set, false
	}
	if s, _, ok := p.classEscape(e); ok {
		return 0, s, true
	}
	if u, ok := p.escapeUnit(e); ok {
		return u, set, false
	}
	p.bad = true
	return 0, set, false
}
