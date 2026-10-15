package packagecontent

import (
	"errors"
	"math/bits"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// jsRegExp is a RegExp that minimatch builds for a glob segment, parsed as V8 parses its source with the given i and u flags. Without the u flag it reads and matches UTF-16 code units; with it, code points.
type jsRegExp struct {
	root       *reNode
	ignoreCase bool
	unicode    bool
}

// errUnsupportedRegExp reports a construct that minimatch never puts in a source, such as a backreference or \d, so this port does not parse it.
var errUnsupportedRegExp = errors.New("unsupported regular expression construct")

// jsSyntaxError is the SyntaxError of new RegExp(source, flags).
type jsSyntaxError struct{ message string }

func (e *jsSyntaxError) Error() string { return e.message }

type reOp uint8

const (
	reEmpty reOp = iota
	reChar
	reDot
	reClass
	reSeq
	reAlt
	reRepeat
	reLookahead
	reStart
	reEnd
)

type reNode struct {
	op    reOp
	char  rune
	class *reCharSet
	subs  []*reNode
	// min and max bound a repeat; max < 0 has no bound.
	min, max int
	// negate marks a negative lookahead.
	negate bool
}

type reCharSet struct {
	negate bool
	items  []reClassItem
}

// reClassItem is a range of a class, or a property escape when has is set.
type reClassItem struct {
	lo, hi rune
	has    func(rune) bool
}

func (item reClassItem) contains(r rune) bool {
	if item.has != nil {
		return item.has(r)
	}
	return item.lo <= r && r <= item.hi
}

// compileJSRegExp is new RegExp(source, flags) for flags made of "i" and "u".
func compileJSRegExp(source []uint16, flags string) (*jsRegExp, error) {
	p := &reParser{unicode: strings.Contains(flags, "u")}
	if p.unicode {
		p.chars = codePoints(source)
	} else {
		p.chars = make([]rune, len(source))
		for i, unit := range source {
			p.chars[i] = rune(unit)
		}
	}
	root, err := p.disjunction(true)
	if err != nil {
		if syntax, ok := errors.AsType[*jsSyntaxError](err); ok {
			syntax.message = "Invalid regular expression: /" + jsstring.FromUTF16(source) + "/" + flags + ": " + syntax.message
		}
		return nil, err
	}
	return &jsRegExp{root: root, ignoreCase: strings.Contains(flags, "i"), unicode: p.unicode}, nil
}

// codePoints joins the surrogate pairs of units, as a RegExp with the u flag reads its source and its input.
func codePoints(units []uint16) []rune {
	out := make([]rune, 0, len(units))
	for i := 0; i < len(units); i++ {
		r := rune(units[i])
		if utf16.IsSurrogate(r) && r < 0xDC00 && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
			r = utf16.DecodeRune(r, rune(units[i+1]))
			i++
		}
		out = append(out, r)
	}
	return out
}

type reParser struct {
	chars   []rune
	pos     int
	unicode bool
}

func syntaxError(message string) error { return &jsSyntaxError{message: message} }

func (p *reParser) more() bool { return p.pos < len(p.chars) }

func (p *reParser) at(offset int) rune {
	if i := p.pos + offset; i < len(p.chars) {
		return p.chars[i]
	}
	return -1
}

// disjunction parses alternatives up to the end of the source, or of a group when top is false.
func (p *reParser) disjunction(top bool) (*reNode, error) {
	var alternatives []*reNode
	for {
		alternative, err := p.alternative()
		if err != nil {
			return nil, err
		}
		alternatives = append(alternatives, alternative)
		if p.at(0) != '|' {
			break
		}
		p.pos++
	}
	switch {
	case p.more() && top:
		return nil, syntaxError("Unmatched ')'")
	case !p.more() && !top:
		return nil, syntaxError("Unterminated group")
	}
	if len(alternatives) == 1 {
		return alternatives[0], nil
	}
	return &reNode{op: reAlt, subs: alternatives}, nil
}

// alternative parses terms as V8's ParseDisjunction does: an assertion takes no quantifier, so a quantifier after it, at the start, after "(" or after "|" has nothing to repeat.
func (p *reParser) alternative() (*reNode, error) {
	var terms []*reNode
	for p.more() && p.at(0) != '|' && p.at(0) != ')' {
		var atom *reNode
		lookaround := false
		switch c := p.at(0); c {
		case '^', '$':
			p.pos++
			terms = append(terms, &reNode{op: map[rune]reOp{'^': reStart, '$': reEnd}[c]})
			continue
		case '.':
			p.pos++
			atom = &reNode{op: reDot}
		case '(':
			p.pos++
			var err error
			if atom, lookaround, err = p.group(); err != nil {
				return nil, err
			}
		case '[':
			class, err := p.class()
			if err != nil {
				return nil, err
			}
			atom = &reNode{op: reClass, class: class}
		case '\\':
			var err error
			if atom, err = p.atomEscape(); err != nil {
				return nil, err
			}
		case '*', '+', '?':
			return nil, syntaxError("Nothing to repeat")
		case '{':
			if _, _, ok := p.interval(); ok {
				return nil, syntaxError("Nothing to repeat")
			}
			if p.unicode {
				return nil, syntaxError("Lone quantifier brackets")
			}
			p.pos++
			atom = &reNode{op: reChar, char: c}
		case '}', ']':
			if p.unicode {
				return nil, syntaxError("Lone quantifier brackets")
			}
			p.pos++
			atom = &reNode{op: reChar, char: c}
		default:
			p.pos++
			atom = &reNode{op: reChar, char: c}
		}
		term, err := p.quantifier(atom, lookaround)
		if err != nil {
			return nil, err
		}
		terms = append(terms, term)
	}
	switch len(terms) {
	case 0:
		return &reNode{op: reEmpty}, nil
	case 1:
		return terms[0], nil
	}
	return &reNode{op: reSeq, subs: terms}, nil
}

// quantifier parses the quantifier after atom, if any. A lazy quantifier matches the same strings, so test does not tell it apart. With the u flag a lookahead takes no quantifier.
func (p *reParser) quantifier(atom *reNode, lookaround bool) (*reNode, error) {
	var lo, hi int
	switch p.at(0) {
	case '*':
		lo, hi = 0, -1
		p.pos++
	case '+':
		lo, hi = 1, -1
		p.pos++
	case '?':
		lo, hi = 0, 1
		p.pos++
	case '{':
		var ok bool
		if lo, hi, ok = p.interval(); !ok {
			if p.unicode {
				return nil, syntaxError("Incomplete quantifier")
			}
			return atom, nil
		}
		if hi >= 0 && hi < lo {
			return nil, syntaxError("numbers out of order in {} quantifier")
		}
		p.pos += p.intervalLength()
	default:
		return atom, nil
	}
	if p.at(0) == '?' {
		p.pos++
	}
	if lookaround && p.unicode {
		return nil, syntaxError("Invalid quantifier")
	}
	return &reNode{op: reRepeat, subs: []*reNode{atom}, min: lo, max: hi}, nil
}

// interval reads a {n}, {n,} or {n,m} quantifier at the position without moving it.
func (p *reParser) interval() (lo, hi int, ok bool) {
	i := p.pos + 1
	number := func() (int, bool) {
		start, value := i, 0
		for i < len(p.chars) && p.chars[i] >= '0' && p.chars[i] <= '9' {
			value = min(value*10+int(p.chars[i]-'0'), 1<<31-1)
			i++
		}
		return value, i > start
	}
	if lo, ok = number(); !ok {
		return 0, 0, false
	}
	hi = lo
	if i < len(p.chars) && p.chars[i] == ',' {
		i++
		if hi, ok = number(); !ok {
			hi = -1
		}
	}
	if i >= len(p.chars) || p.chars[i] != '}' {
		return 0, 0, false
	}
	return lo, hi, true
}

func (p *reParser) intervalLength() int {
	i := p.pos
	for p.chars[i] != '}' {
		i++
	}
	return i + 1 - p.pos
}

// group parses a group after its "(": a capturing group, (?:, or a lookahead. V8 reads "(?" followed by anything else as an invalid group; the lookbehinds, named groups and modifier groups it accepts never occur in minimatch's sources.
func (p *reParser) group() (*reNode, bool, error) {
	lookahead, negate := false, false
	if p.at(0) == '?' {
		switch p.at(1) {
		case ':':
		case '=', '!':
			lookahead, negate = true, p.at(1) == '!'
		case '<', '-', 'i', 'm', 's':
			return nil, false, errUnsupportedRegExp
		default:
			return nil, false, syntaxError("Invalid group")
		}
		p.pos += 2
	}
	body, err := p.disjunction(false)
	if err != nil {
		return nil, false, err
	}
	p.pos++
	if lookahead {
		return &reNode{op: reLookahead, subs: []*reNode{body}, negate: negate}, true, nil
	}
	return body, false, nil
}

// class parses a class as V8's ParseCharacterClass does.
func (p *reParser) class() (*reCharSet, error) {
	p.pos++
	class := &reCharSet{}
	if p.at(0) == '^' {
		class.negate = true
		p.pos++
	}
	add := func(char rune, items []reClassItem) {
		if items != nil {
			class.items = append(class.items, items...)
		} else {
			class.items = append(class.items, reClassItem{lo: char, hi: char})
		}
	}
	for p.more() && p.at(0) != ']' {
		char1, items1, err := p.classAtom()
		if err != nil {
			return nil, err
		}
		if p.at(0) != '-' {
			add(char1, items1)
			continue
		}
		p.pos++
		if !p.more() {
			break
		}
		if p.at(0) == ']' {
			add(char1, items1)
			add('-', nil)
			break
		}
		char2, items2, err := p.classAtom()
		if err != nil {
			return nil, err
		}
		if items1 != nil || items2 != nil {
			if p.unicode {
				return nil, syntaxError("Invalid character class")
			}
			add(char1, items1)
			add('-', nil)
			add(char2, items2)
			continue
		}
		if char1 > char2 {
			return nil, syntaxError("Range out of order in character class")
		}
		class.items = append(class.items, reClassItem{lo: char1, hi: char2})
	}
	if !p.more() {
		return nil, syntaxError("Unterminated character class")
	}
	p.pos++
	return class, nil
}

// classAtom parses one class member: a character, or the items of a property escape.
func (p *reParser) classAtom() (rune, []reClassItem, error) {
	c := p.at(0)
	if c != '\\' {
		p.pos++
		return c, nil, nil
	}
	switch next := p.at(1); {
	case next < 0:
		return 0, nil, syntaxError("\\ at end of pattern")
	case next == 'b':
		p.pos += 2
		return '\b', nil, nil
	case next == '-' && p.unicode:
		p.pos += 2
		return '-', nil, nil
	case strings.ContainsRune("dDsSwW", next):
		return 0, nil, errUnsupportedRegExp
	case (next == 'p' || next == 'P') && p.unicode:
		items, err := p.property("Invalid property name in character class")
		return 0, items, err
	}
	char, err := p.characterEscape()
	return char, nil, err
}

// atomEscape parses an escape outside a class.
func (p *reParser) atomEscape() (*reNode, error) {
	switch next := p.at(1); {
	case next < 0:
		return nil, syntaxError("\\ at end of pattern")
	case strings.ContainsRune("bBdDsSwW0123456789", next):
		return nil, errUnsupportedRegExp
	case next == 'k' && p.unicode:
		return nil, errUnsupportedRegExp
	case (next == 'p' || next == 'P') && p.unicode:
		items, err := p.property("Invalid property name")
		if err != nil {
			return nil, err
		}
		return &reNode{op: reClass, class: &reCharSet{items: items}}, nil
	}
	char, err := p.characterEscape()
	if err != nil {
		return nil, err
	}
	return &reNode{op: reChar, char: char}, nil
}

// characterEscape is V8's ParseCharacterEscape: a control letter, \x and two hex digits, or an identity escape, which the u flag allows only for a syntax character or "/".
func (p *reParser) characterEscape() (rune, error) {
	p.pos++
	c := p.at(0)
	switch c {
	case 'f', 'n', 'r', 't', 'v':
		p.pos++
		return map[rune]rune{'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v'}[c], nil
	case 'c', 'u', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return 0, errUnsupportedRegExp
	case 'x':
		p.pos++
		if hi, lo := hexValue(p.at(0)), hexValue(p.at(1)); hi >= 0 && lo >= 0 {
			p.pos += 2
			return rune(hi<<4 | lo), nil
		}
		if p.unicode {
			return 0, syntaxError("Invalid escape")
		}
		return 'x', nil
	}
	if !p.unicode || strings.ContainsRune(`^$\.*+?()[]{}|/`, c) {
		p.pos++
		return c, nil
	}
	return 0, syntaxError("Invalid escape")
}

func hexValue(r rune) int {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0')
	case r >= 'a' && r <= 'f':
		return int(r-'a') + 10
	case r >= 'A' && r <= 'F':
		return int(r-'A') + 10
	}
	return -1
}

// regExpProperty is the General_Category table of a \p{Name} in minimatch's POSIX classes. Go's C table, like V8's \p{C}, includes the unassigned code points. A switch keeps the tables out of package initialization.
func regExpProperty(name string) (*unicode.RangeTable, bool) {
	switch name {
	case "L":
		return unicode.L, true
	case "Nl":
		return unicode.Nl, true
	case "Nd":
		return unicode.Nd, true
	case "Zs":
		return unicode.Zs, true
	case "Cc":
		return unicode.Cc, true
	case "Z":
		return unicode.Z, true
	case "C":
		return unicode.C, true
	case "Ll":
		return unicode.Ll, true
	case "P":
		return unicode.P, true
	case "Lu":
		return unicode.Lu, true
	case "Pc":
		return unicode.Pc, true
	}
	return nil, false
}

// property parses \p{Name} or \P{Name} with the u flag. invalid is V8's message for a name it rejects; a name outside regExpProperty that V8 might accept is unsupported.
func (p *reParser) property(invalid string) ([]reClassItem, error) {
	negated := p.at(1) == 'P'
	p.pos += 2
	if p.at(0) != '{' {
		return nil, syntaxError(invalid)
	}
	end := p.pos + 1
	for end < len(p.chars) && p.chars[end] != '}' {
		end++
	}
	if end >= len(p.chars) {
		return nil, syntaxError(invalid)
	}
	table, ok := regExpProperty(string(p.chars[p.pos+1 : end]))
	if !ok {
		return nil, errUnsupportedRegExp
	}
	p.pos = end + 1
	if negated {
		return []reClassItem{{has: func(r rune) bool { return !unicode.Is(table, r) }}}, nil
	}
	return []reClassItem{{has: func(r rune) bool { return unicode.Is(table, r) }}}, nil
}

// test is RegExp.prototype.test: whether the RegExp matches name at any position. It returns what V8's backtracking returns, since it needs only whether some match exists and minimatch's sources have no backreference: it follows the set of positions each node can reach.
func (re *jsRegExp) test(name string) bool {
	units := jsstring.ToUTF16(name)
	var input []rune
	if re.unicode {
		input = codePoints(units)
	} else {
		input = make([]rune, len(units))
		for i, unit := range units {
			input[i] = rune(unit)
		}
	}
	m := &reMatcher{re: re, input: input, lookaheads: make(map[reLookaheadKey]bool)}
	return !m.eval(re.root, m.full()).empty()
}

type reMatcher struct {
	re         *jsRegExp
	input      []rune
	lookaheads map[reLookaheadKey]bool
}

type reLookaheadKey struct {
	node *reNode
	pos  int
}

// rePositions is a set of positions in the input, from 0 to its length.
type rePositions []uint64

func (m *reMatcher) none() rePositions { return make(rePositions, len(m.input)/64+1) }

func (m *reMatcher) full() rePositions {
	s := m.none()
	for i := range len(m.input) + 1 {
		s.add(i)
	}
	return s
}

func (s rePositions) add(i int) { s[i/64] |= 1 << (i % 64) }

func (s rePositions) empty() bool {
	return !slices.ContainsFunc(s, func(w uint64) bool { return w != 0 })
}

func (s rePositions) clone() rePositions { return slices.Clone(s) }

func (s rePositions) each(yield func(int)) {
	for w, word := range s {
		for word != 0 {
			yield(w*64 + bits.TrailingZeros64(word))
			word &= word - 1
		}
	}
}

// eval returns the positions where node can end a match that starts at one of from.
func (m *reMatcher) eval(node *reNode, from rePositions) rePositions {
	switch node.op {
	case reEmpty:
		return from
	case reChar, reDot, reClass:
		out := m.none()
		from.each(func(i int) {
			if i < len(m.input) && m.matchesChar(node, m.input[i]) {
				out.add(i + 1)
			}
		})
		return out
	case reSeq:
		for _, sub := range node.subs {
			from = m.eval(sub, from)
		}
		return from
	case reAlt:
		out := m.none()
		for _, sub := range node.subs {
			for w, word := range m.eval(sub, from) {
				out[w] |= word
			}
		}
		return out
	case reStart, reEnd, reLookahead:
		out := m.none()
		from.each(func(i int) {
			if m.assertion(node, i) {
				out.add(i)
			}
		})
		return out
	case reRepeat:
		return m.repeat(node, from)
	}
	return m.none()
}

func (m *reMatcher) assertion(node *reNode, i int) bool {
	switch node.op {
	case reStart:
		return i == 0
	case reEnd:
		return i == len(m.input)
	}
	key := reLookaheadKey{node, i}
	matched, ok := m.lookaheads[key]
	if !ok {
		start := m.none()
		start.add(i)
		matched = !m.eval(node.subs[0], start).empty()
		m.lookaheads[key] = matched
	}
	return matched != node.negate
}

// repeat follows a quantifier: min iterations, then further ones to a fixed point or max. An iteration that matches the empty string adds no position, so the set is what ECMAScript's RepeatMatcher reaches.
func (m *reMatcher) repeat(node *reNode, from rePositions) rePositions {
	for range node.min {
		if from = m.eval(node.subs[0], from); from.empty() {
			return from
		}
	}
	reached, frontier := from.clone(), from
	for count := node.min; !frontier.empty() && (node.max < 0 || count < node.max); count++ {
		next := m.eval(node.subs[0], frontier)
		for w := range next {
			next[w] &^= reached[w]
			reached[w] |= next[w]
		}
		frontier = next
	}
	return reached
}

func (m *reMatcher) matchesChar(node *reNode, c rune) bool {
	switch node.op {
	case reDot:
		return !isLineTerminator(c)
	case reChar:
		return node.char == c || m.re.ignoreCase && sameCase(node.char, c, m.re.unicode)
	}
	class := node.class
	hit := func(r rune) bool {
		return slices.ContainsFunc(class.items, func(item reClassItem) bool { return item.contains(r) })
	}
	found := hit(c)
	if !found && m.re.ignoreCase {
		found = caseEquivalents(c, m.re.unicode, hit)
	}
	return found != class.negate
}

// sameCase reports whether a and b match ignoring case: the same Canonicalize without the u flag, and the same simple case folding with it (ECMAScript RegExp semantics).
func sameCase(a, b rune, unicodeMode bool) bool {
	if unicodeMode {
		for r := unicode.SimpleFold(a); r != a; r = unicode.SimpleFold(r) {
			if r == b {
				return true
			}
		}
		return false
	}
	return canonicalizeRune(a) == canonicalizeRune(b)
}

// caseEquivalents calls yield with every character that matches c ignoring case and could be a class member that makes c match: the characters with c's Canonicalize without the u flag, and c's case-folding orbit with it.
func caseEquivalents(c rune, unicodeMode bool, yield func(rune) bool) bool {
	if unicodeMode {
		for r := unicode.SimpleFold(c); r != c; r = unicode.SimpleFold(r) {
			if yield(r) {
				return true
			}
		}
		return false
	}
	canonical := canonicalizeRune(c)
	r := canonical
	for {
		if canonicalizeRune(r) == canonical && yield(r) {
			return true
		}
		if r = unicode.SimpleFold(r); r == canonical {
			return false
		}
	}
}
