package hljs

// lexemeScanner finds the lexemes of a mode's keyword pattern without running the regular expression, for a pattern of one of two forms: C+ or C D*, where C and D are classes of ASCII units. Such a pattern has one way to match: the leftmost unit of C, and the units of the second class (or of C) after it as far as they go. The default pattern \w+ and the identifier patterns of most grammars are of these forms; every word of the text of a mode with keywords is a lexeme, so the regular expression otherwise runs, and returns a match object, once per word.
type lexemeScanner struct {
	first, rest [2]uint64
}

func (s *lexemeScanner) has(set *[2]uint64, u rune) bool {
	return u >= 0 && u < 128 && set[u>>6]&(1<<(u&63)) != 0
}

// next is the first lexeme at or after from: keywordPatternRe.exec with lastIndex from.
func (s *lexemeScanner) next(buffer []rune, from int) (start, end int, ok bool) {
	for i := max(from, 0); i < len(buffer); i++ {
		if !s.has(&s.first, buffer[i]) {
			continue
		}
		end = i + 1
		for end < len(buffer) && s.has(&s.rest, buffer[end]) {
			end++
		}
		return i, end, true
	}
	return 0, 0, false
}

// nextLexeme is the next match of the mode's keyword pattern in buffer at or after from.
func (c *compiledProps) nextLexeme(buffer []rune, from int) (start, end int, ok bool) {
	if c.lexemes != nil {
		return c.lexemes.next(buffer, from)
	}
	match, ok := c.keywordPatternRe.exec(buffer, from)
	return match.index, match.end, ok
}

// newLexemeScanner is the scanner of a keyword pattern of the form C+ or C D*, or nil for any other pattern, which the regular expression matches. Under ignoreCase a letter of a class stands for both its cases; a unit outside ASCII has no ASCII letter as its canonical form, so it stays outside every class here.
func newLexemeScanner(source string, ignoreCase bool) *lexemeScanner {
	p := lexemeParser{src: source, ignoreCase: ignoreCase}
	var first, rest [2]uint64
	if !p.class(&first) {
		return nil
	}
	if p.src[p.pos:] == "+" {
		return &lexemeScanner{first: first, rest: first}
	}
	if !p.class(&rest) || p.src[p.pos:] != "*" {
		return nil
	}
	return &lexemeScanner{first: first, rest: rest}
}

type lexemeParser struct {
	src        string
	pos        int
	ignoreCase bool
}

func (p *lexemeParser) add(set *[2]uint64, lo, hi byte) {
	for c := int(lo); c <= int(hi); c++ {
		set[c>>6] |= 1 << (c & 63)
		if p.ignoreCase {
			switch {
			case c >= 'a' && c <= 'z':
				set[(c-32)>>6] |= 1 << ((c - 32) & 63)
			case c >= 'A' && c <= 'Z':
				set[(c+32)>>6] |= 1 << ((c + 32) & 63)
			}
		}
	}
}

// escape reads the class escape at pos, a backslash: \w, \d, or an escaped ASCII punctuation mark, which stands for itself. Any other escape is not read.
func (p *lexemeParser) escape(set *[2]uint64) bool {
	if p.pos+1 >= len(p.src) {
		return false
	}
	c := p.src[p.pos+1]
	switch {
	case c == 'w':
		p.add(set, 'a', 'z')
		p.add(set, 'A', 'Z')
		p.add(set, '0', '9')
		p.add(set, '_', '_')
	case c == 'd':
		p.add(set, '0', '9')
	case c < 0x80 && c > ' ' && !wordByte(c):
		p.add(set, c, c)
	default:
		return false
	}
	p.pos += 2
	return true
}

// wordByte reports whether c is a unit of \w.
func wordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// class reads the class at pos into set: \w, \d, or a bracketed class of ASCII units that is not negated, whose members are units, ranges of units, \w, \d and escaped punctuation.
func (p *lexemeParser) class(set *[2]uint64) bool {
	if p.pos >= len(p.src) {
		return false
	}
	if p.src[p.pos] == '\\' {
		c := p.src[min(p.pos+1, len(p.src)-1)]
		return (c == 'w' || c == 'd') && p.escape(set)
	}
	if p.src[p.pos] != '[' {
		return false
	}
	p.pos++
	if p.pos < len(p.src) && p.src[p.pos] == '^' {
		return false
	}
	members := 0
	for {
		if p.pos >= len(p.src) {
			return false
		}
		c := p.src[p.pos]
		switch {
		case c == ']':
			p.pos++
			return members > 0
		case c == '\\':
			if !p.escape(set) {
				return false
			}
			// A class escape is not the low end of a range: \w-x is three members in JavaScript, and is left to the regular expression here.
			if p.pos+1 < len(p.src) && p.src[p.pos] == '-' && p.src[p.pos+1] != ']' {
				return false
			}
		case c >= 0x80 || c < ' ':
			return false
		case p.pos+2 < len(p.src) && p.src[p.pos+1] == '-' && p.src[p.pos+2] != ']':
			hi := p.src[p.pos+2]
			if hi == '\\' || hi >= 0x80 || hi < c {
				return false
			}
			p.add(set, c, hi)
			p.pos += 3
		default:
			p.add(set, c, c)
			p.pos++
		}
		members++
	}
}
