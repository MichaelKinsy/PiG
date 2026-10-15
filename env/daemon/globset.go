package daemon

// Ports the glob dialect of globset 0.4.20 (glob.rs: GlobBuilder with literal_separator and backslash_escape on, case
// sensitive), which the `ignore` crate compiles gitignore patterns with. A pattern becomes a regular expression over bytes:
// the haystack is the path's bytes, each held as the code point of the same value, so `?`, `*` and a class stand for one
// byte, as in globset's `(?-u)` regexes.

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

type globTokenKind int

const (
	tokenLiteral globTokenKind = iota
	tokenAny
	tokenZeroOrMore
	tokenRecursivePrefix
	tokenRecursiveSuffix
	tokenRecursiveZeroOrMore
	tokenClass
	tokenAlternates
)

type globToken struct {
	kind    globTokenKind
	literal rune
	negated bool
	ranges  [][2]rune
	options [][]globToken
}

type globParser struct {
	chars              []rune
	next               int
	prev, cur          rune
	hasPrev, hasCur    bool
	alternatesStack    []int
	branches           [][]globToken
	foundUnclosedClass bool
}

var (
	errUnopenedAlternates = errors.New("unopened alternate group; missing '{' (maybe escape '}' with '[}]'?)")
	errUnclosedAlternates = errors.New("unclosed alternate group; missing '}' (maybe escape '{' with '[{]'?)")
	errDanglingEscape     = errors.New(`dangling '\'`)
)

func isSeparator(c rune) bool { return c == '/' }

func (p *globParser) bump() (rune, bool) {
	p.prev, p.hasPrev = p.cur, p.hasCur
	if p.next >= len(p.chars) {
		p.cur, p.hasCur = 0, false
		return 0, false
	}
	p.cur, p.hasCur = p.chars[p.next], true
	p.next++
	return p.cur, true
}

func (p *globParser) peek() (rune, bool) {
	if p.next >= len(p.chars) {
		return 0, false
	}
	return p.chars[p.next], true
}

func (p *globParser) pushToken(token globToken) error {
	if len(p.branches) == 0 {
		return errUnopenedAlternates
	}
	last := len(p.branches) - 1
	p.branches[last] = append(p.branches[last], token)
	return nil
}

func (p *globParser) pushTwice(token globToken) error {
	if err := p.pushToken(token); err != nil {
		return err
	}
	return p.pushToken(token)
}

func (p *globParser) popToken() (globToken, error) {
	if len(p.branches) == 0 {
		return globToken{}, errUnopenedAlternates
	}
	last := len(p.branches) - 1
	branch := p.branches[last]
	token := branch[len(branch)-1]
	p.branches[last] = branch[:len(branch)-1]
	return token, nil
}

func (p *globParser) haveTokens() (bool, error) {
	if len(p.branches) == 0 {
		return false, errUnopenedAlternates
	}
	return len(p.branches[len(p.branches)-1]) > 0, nil
}

func (p *globParser) parse() error {
	for {
		c, ok := p.bump()
		if !ok {
			return nil
		}
		var err error
		switch {
		case c == '?':
			err = p.pushToken(globToken{kind: tokenAny})
		case c == '*':
			err = p.parseStar()
		case c == '[' && !p.foundUnclosedClass:
			err = p.parseClass()
		case c == '{':
			p.alternatesStack = append(p.alternatesStack, len(p.branches))
			p.branches = append(p.branches, nil)
		case c == '}':
			err = p.popAlternate()
		case c == ',':
			if len(p.alternatesStack) == 0 {
				err = p.pushToken(globToken{kind: tokenLiteral, literal: ','})
			} else {
				p.branches = append(p.branches, nil)
			}
		case c == '\\':
			if escaped, ok := p.bump(); ok {
				err = p.pushToken(globToken{kind: tokenLiteral, literal: escaped})
			} else {
				err = errDanglingEscape
			}
		default:
			err = p.pushToken(globToken{kind: tokenLiteral, literal: c})
		}
		if err != nil {
			return err
		}
	}
}

func (p *globParser) popAlternate() error {
	if len(p.alternatesStack) == 0 {
		return errUnopenedAlternates
	}
	start := p.alternatesStack[len(p.alternatesStack)-1]
	p.alternatesStack = p.alternatesStack[:len(p.alternatesStack)-1]
	options := append([][]globToken(nil), p.branches[start:]...)
	p.branches = p.branches[:start]
	return p.pushToken(globToken{kind: tokenAlternates, options: options})
}

func (p *globParser) parseStar() error {
	prev, hasPrev := p.prev, p.hasPrev
	if next, ok := p.peek(); !ok || next != '*' {
		return p.pushToken(globToken{kind: tokenZeroOrMore})
	}
	p.bump()
	have, err := p.haveTokens()
	if err != nil {
		return err
	}
	if !have {
		if next, ok := p.peek(); ok && !isSeparator(next) {
			return p.pushTwice(globToken{kind: tokenZeroOrMore})
		}
		if err := p.pushToken(globToken{kind: tokenRecursivePrefix}); err != nil {
			return err
		}
		p.bump()
		return nil
	}
	if !hasPrev || !isSeparator(prev) {
		if len(p.branches) <= 1 || !hasPrev || (prev != ',' && prev != '{') {
			return p.pushTwice(globToken{kind: tokenZeroOrMore})
		}
	}
	var isSuffix bool
	next, ok := p.peek()
	switch {
	case !ok:
		p.bump()
		isSuffix = true
	case (next == ',' || next == '}') && len(p.branches) >= 2:
		isSuffix = true
	case isSeparator(next):
		p.bump()
	default:
		return p.pushTwice(globToken{kind: tokenZeroOrMore})
	}
	popped, err := p.popToken()
	if err != nil {
		return err
	}
	switch {
	case popped.kind == tokenRecursivePrefix:
		return p.pushToken(globToken{kind: tokenRecursivePrefix})
	case popped.kind == tokenRecursiveSuffix:
		return p.pushToken(globToken{kind: tokenRecursiveSuffix})
	case isSuffix:
		return p.pushToken(globToken{kind: tokenRecursiveSuffix})
	default:
		return p.pushToken(globToken{kind: tokenRecursiveZeroOrMore})
	}
}

// parseClass parses `[...]` after its `[`; a class that never closes is a literal `[` (allow_unclosed_class).
func (p *globParser) parseClass() error {
	savedNext, savedPrev, savedHasPrev, savedCur, savedHasCur := p.next, p.prev, p.hasPrev, p.cur, p.hasCur
	var ranges [][2]rune
	negated := false
	if c, ok := p.peek(); ok && (c == '!' || c == '^') {
		p.bump()
		negated = true
	}
	first, inRange := true, false
	addToLastRange := func(add rune) error {
		last := &ranges[len(ranges)-1]
		last[1] = add
		if last[1] < last[0] {
			return fmt.Errorf("invalid range; '%c' > '%c'", last[0], last[1])
		}
		return nil
	}
	for {
		c, ok := p.bump()
		if !ok {
			p.next, p.prev, p.hasPrev, p.cur, p.hasCur = savedNext, savedPrev, savedHasPrev, savedCur, savedHasCur
			p.foundUnclosedClass = true
			return p.pushToken(globToken{kind: tokenLiteral, literal: '['})
		}
		switch c {
		case ']':
			if !first {
				if inRange {
					ranges = append(ranges, [2]rune{'-', '-'})
				}
				return p.pushToken(globToken{kind: tokenClass, negated: negated, ranges: ranges})
			}
			ranges = append(ranges, [2]rune{']', ']'})
		case '-':
			switch {
			case first:
				ranges = append(ranges, [2]rune{'-', '-'})
			case inRange:
				if err := addToLastRange('-'); err != nil {
					return err
				}
				inRange = false
			default:
				inRange = true
			}
		default:
			if inRange {
				if err := addToLastRange(c); err != nil {
					return err
				}
			} else {
				ranges = append(ranges, [2]rune{c, c})
			}
			inRange = false
		}
		first = false
	}
}

// escapedLiteral is regex_syntax::escape for an ASCII byte and `\xNN` for another, the literal globset writes.
func escapedLiteral(character rune) string {
	var text strings.Builder
	var encoded [utf8.UTFMax]byte
	for _, b := range encoded[:utf8.EncodeRune(encoded[:], character)] {
		if b > 0x7f {
			fmt.Fprintf(&text, `\x%02x`, b)
			continue
		}
		if strings.IndexByte(`\.+*?()|[]{}^$#&-~`, b) >= 0 {
			text.WriteByte('\\')
		}
		text.WriteByte(b)
	}
	return text.String()
}

func tokensToRegex(tokens []globToken, re *strings.Builder) {
	for _, token := range tokens {
		switch token.kind {
		case tokenLiteral:
			re.WriteString(escapedLiteral(token.literal))
		case tokenAny:
			re.WriteString("[^/]")
		case tokenZeroOrMore:
			re.WriteString("[^/]*")
		case tokenRecursivePrefix:
			re.WriteString("(?:/?|.*/)")
		case tokenRecursiveSuffix:
			re.WriteString("/.*")
		case tokenRecursiveZeroOrMore:
			re.WriteString("(?:/|/.*/)")
		case tokenClass:
			re.WriteByte('[')
			if token.negated {
				re.WriteByte('^')
			}
			for _, r := range token.ranges {
				re.WriteString(escapedLiteral(r[0]))
				if r[0] != r[1] {
					re.WriteByte('-')
					re.WriteString(escapedLiteral(r[1]))
				}
			}
			re.WriteByte(']')
		case tokenAlternates:
			var parts []string
			for _, option := range token.options {
				var alternative strings.Builder
				tokensToRegex(option, &alternative)
				if alternative.Len() > 0 {
					parts = append(parts, alternative.String())
				}
			}
			if len(parts) > 0 {
				re.WriteString("(?:" + strings.Join(parts, "|") + ")")
			}
		}
	}
}

// requiredLiteral is the longest run of literal characters outside an alternation or a class: every path the glob matches
// contains it, so a path without it needs no regular expression.
func requiredLiteral(tokens []globToken) string {
	var best, run strings.Builder
	for _, token := range tokens {
		if token.kind == tokenLiteral {
			run.WriteRune(token.literal)
			continue
		}
		if run.Len() > best.Len() {
			best.Reset()
			best.WriteString(run.String())
		}
		run.Reset()
	}
	if run.Len() > best.Len() {
		return run.String()
	}
	return best.String()
}

// compileGlob is GlobBuilder::new(glob).literal_separator(true).backslash_escape(true).allow_unclosed_class(true).build():
// the matcher over a path's bytes (see byteString) and the literal every matching path contains, or the parse error that
// makes the `ignore` crate drop the pattern.
func compileGlob(glob string) (*regexp.Regexp, string, error) {
	parser := &globParser{chars: []rune(glob), branches: [][]globToken{nil}}
	if err := parser.parse(); err != nil {
		return nil, "", err
	}
	if len(parser.branches) > 1 {
		return nil, "", errUnclosedAlternates
	}
	tokens := parser.branches[0]
	var re strings.Builder
	re.WriteString("(?s)^")
	if len(tokens) == 1 && tokens[0].kind == tokenRecursivePrefix {
		re.WriteString(".*$")
	} else {
		tokensToRegex(tokens, &re)
		re.WriteByte('$')
	}
	compiled, err := regexp.Compile(re.String()) //perf:ok each .gitignore line's pattern is compiled once when the file is loaded, not per match
	if err != nil {
		return nil, "", err
	}
	return compiled, requiredLiteral(tokens), nil
}

// byteString holds each byte of text as the code point of the same value, the haystack compileGlob's expressions match.
func byteString(text string) string {
	runes := make([]rune, len(text))
	for i := range len(text) {
		runes[i] = rune(text[i])
	}
	return string(runes)
}
