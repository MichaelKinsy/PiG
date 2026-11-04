package ecmaregex

import (
	"math"
	"strings"
	"sync"
)

// Ports the pattern grammar of ECMA-262 (22.2.1) with the Annex B extensions (B.1.2) as V8 reads it.

type nodeKind uint8

const (
	kindEmpty nodeKind = iota
	kindSet
	kindSeq
	kindAlt
	kindCapture
	kindGroup
	kindLook
	kindBackref
	kindAssert
	kindRepeat
)

// A node is a term of a parsed pattern.
type node struct {
	kind nodeKind
	// set is the characters of a kindSet.
	set runeSet
	// children are the terms of a kindSeq, the alternatives of a kindAlt, or the single body or operand of the other kinds that have one.
	children []*node
	// assertion is '^', '$', 'b' or 'B' for a kindAssert; multiline is set on "^" and "$" that also match at line terminators.
	assertion rune
	multiline bool
	// fold is set on a backreference that compares without regard to case, and on "\b" and "\B" that count the characters which fold onto ASCII word characters as word characters.
	fold bool
	// ahead and negated describe a kindLook.
	ahead, negated bool
	// index is the number of a kindCapture.
	index int
	// name is the group name of a named kindBackref, refs the numbers of the capturing groups it names.
	name string
	refs []int
	// min, max and greedy describe a kindRepeat; max is -1 when unbounded.
	min, max int
	greedy   bool
}

// A syntaxError is a pattern V8 rejects with a SyntaxError. Its text is the message of that error: parse knows the reason, and compile adds the source and the flags.
type syntaxError struct{ source, flags, reason string }

func (e *syntaxError) Error() string {
	return "Invalid regular expression: /" + e.source + "/" + e.flags + ": " + e.reason
}

// A nameScope is the state V8 keeps per open group, and for the whole pattern, to tell which groups of one name can both take part in a match. index is the number of the group (for a group that does not capture, the number of the last group started when it opened), and groups numbered first to second, both included, lie in an earlier alternative of the enclosing groups. The interval is empty (0, 0) until a "|" has been read; a group starts with its parent's interval, and each "|" in it sets second to the number of groups started so far, or, when the interval was empty, first to index too.
type nameScope struct {
	index, first, second int
}

// modifiers are the flags that a modifier group can change: i (ignore case), m (multiline) and s (dot matches line terminators). They are off for a whole pattern.
type modifiers struct{ ignoreCase, multiline, dotAll bool }

type parser struct {
	// oneByte makes the parser read the pattern as V8 compiles it for a subject string that holds only code units up to U+00FF (which V8 stores with one byte per unit). There V8 drops a literal character above U+00FF from a case-insensitive scope before it looks up the characters that match it case-insensitively, so (?i:Ÿ) cannot match "ÿ" in such a string though it matches "ÿ" in a wider one; without the u flag a class whose members are all above U+00FF does the same. latin1Matters is set when the pattern holds such a literal or class, which tells that the pattern has a second form.
	oneByte, latin1Matters bool
	flags                  modifiers
	// leakIgnoreCase is the i flag of the group that was opened last, which V8's parser keeps in force after that group closes: \w and \W, wherever they stand, take the two characters that fold onto the ASCII word characters into account when it is set, though the term itself ignores case only inside a modifier group.
	leakIgnoreCase bool
	src            []rune
	pos            int
	// unicode is the u flag: src holds code points and the strict grammar applies. Without it src holds UTF-16 code units and the Annex B grammar applies.
	unicode bool
	// universe is the last character of the strings the pattern runs against.
	universe rune
	// captureCount is the number of capturing groups of the whole pattern and hasNamed tells whether any is named; V8 counts both before it reads an escape.
	captureCount int
	hasNamed     bool
	// groups counts the capturing groups opened so far.
	groups int
	// names maps a group name to the numbers of the groups that carry it; a name may repeat when the groups lie in different alternatives.
	names     map[string][]int
	namedRefs []*node
	// scopes holds the nameScope of the whole pattern and of each open group, innermost last.
	scopes []nameScope
}

// parse reads a whole pattern. src is a slice of code points when unicode is set and of UTF-16 code units otherwise.
func parse(src []rune, unicode, oneByte bool) (root *node, latin1Matters bool, err error) {
	p := &parser{src: src, unicode: unicode, oneByte: oneByte, universe: maxUnit, names: map[string][]int{}, scopes: []nameScope{{}}}
	if unicode {
		p.universe = maxCodePoint
	}
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(*syntaxError)
			if !ok {
				panic(r)
			}
			root, latin1Matters, err = nil, false, e
		}
	}()
	p.scanCaptures()
	root = p.disjunction()
	if p.pos < len(p.src) {
		p.fail("Unmatched ')'")
	}
	for _, ref := range p.namedRefs {
		groups, ok := p.names[ref.name]
		if !ok {
			p.fail("Invalid named capture referenced")
		}
		ref.refs = groups
	}
	return root, p.latin1Matters, nil
}

// failProperty reports an unknown or malformed \p{...}.
func (p *parser) failProperty(inClass bool) {
	if inClass {
		p.fail("Invalid property name in character class")
	}
	p.fail("Invalid property name")
}

func (p *parser) fail(reason string) { panic(&syntaxError{reason: reason}) }

func (p *parser) done() bool { return p.pos >= len(p.src) }

// peek returns the character offset places ahead, or -1 at the end of the pattern.
func (p *parser) peek(offset int) rune {
	if p.pos+offset < len(p.src) {
		return p.src[p.pos+offset]
	}
	return -1
}

func (p *parser) eat(c rune) bool {
	if p.peek(0) == c {
		p.pos++
		return true
	}
	return false
}

// scanCaptures counts the capturing groups of the pattern and finds out whether one is named, skipping escapes and classes.
func (p *parser) scanCaptures() {
	inClass := false
	for i := 0; i < len(p.src); i++ {
		switch p.src[i] {
		case '\\':
			i++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '(':
			if inClass {
				continue
			}
			if i+1 < len(p.src) && p.src[i+1] == '?' {
				if i+2 < len(p.src) && p.src[i+2] == '<' && (i+3 >= len(p.src) || (p.src[i+3] != '=' && p.src[i+3] != '!')) {
					p.hasNamed = true
					p.captureCount++
				}
				continue
			}
			p.captureCount++
		}
	}
}

// disjunction reads alternatives up to an unmatched ")" or the end of the pattern.
func (p *parser) disjunction() *node {
	alternatives := []*node{p.alternative()}
	for p.eat('|') {
		scope := &p.scopes[len(p.scopes)-1]
		if scope.second == 0 {
			scope.first = scope.index
		}
		scope.second = p.groups
		alternatives = append(alternatives, p.alternative())
	}
	if len(alternatives) == 1 {
		return alternatives[0]
	}
	return &node{kind: kindAlt, children: alternatives}
}

func (p *parser) alternative() *node {
	var terms []*node
	for !p.done() && p.peek(0) != '|' && p.peek(0) != ')' {
		terms = append(terms, p.term())
	}
	switch len(terms) {
	case 0:
		return &node{kind: kindEmpty}
	case 1:
		return terms[0]
	}
	return &node{kind: kindSeq, children: terms}
}

// term reads one atom or assertion together with the quantifier that follows it.
func (p *parser) term() *node {
	c := p.src[p.pos]
	var atom *node
	switch c {
	case '^', '$':
		p.pos++
		return &node{kind: kindAssert, assertion: c, multiline: p.flags.multiline}
	case '*', '+', '?':
		p.fail("Nothing to repeat")
	case '{':
		if _, _, _, ok := p.interval(); ok {
			p.fail("Nothing to repeat")
		}
		if p.unicode {
			p.fail("Lone quantifier brackets")
		}
		p.pos++
		atom = p.charNode(c)
	case '}', ']':
		if p.unicode {
			p.fail("Lone quantifier brackets")
		}
		p.pos++
		atom = p.charNode(c)
	case '.':
		p.pos++
		if p.flags.dotAll {
			atom = &node{kind: kindSet, set: runeSet{{0, p.universe}}}
		} else {
			atom = &node{kind: kindSet, set: lineTerminators.complement(p.universe)}
		}
	case '(':
		atom = p.group()
	case '[':
		atom = p.class()
	case '\\':
		var assertion bool
		atom, assertion = p.atomEscape()
		if assertion {
			return atom
		}
	default:
		p.pos++
		atom = p.charNode(c)
	}
	return p.quantified(atom)
}

// charNode is the term for the character c: only c, or with the i flag every character that matches it.
func (p *parser) charNode(c rune) *node {
	if p.flags.ignoreCase && c > 0xFF {
		p.latin1Matters = true
		if p.oneByte {
			return &node{kind: kindSet}
		}
	}
	return p.setNode(singleRune(c))
}

// setNode is the term for a set of characters, widened with the i flag.
func (p *parser) setNode(set runeSet) *node {
	if p.flags.ignoreCase {
		set = fold(set, p.unicode)
	}
	return &node{kind: kindSet, set: set}
}

// quantified reads the quantifier after atom, if there is one.
func (p *parser) quantified(atom *node) *node {
	var lo, hi int
	switch p.peek(0) {
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
		var length int
		var ok bool
		lo, hi, length, ok = p.interval()
		if !ok {
			if p.unicode {
				p.fail("Incomplete quantifier")
			}
			return atom
		}
		if hi >= 0 && hi < lo {
			p.fail("numbers out of order in {} quantifier")
		}
		p.pos += length
	default:
		return atom
	}
	greedy := !p.eat('?')
	if atom.kind == kindLook {
		// Lookbehinds and, with the u flag, lookaheads take no quantifier. A lookahead of Annex B repeats as often as its quantifier demands, but a repeat of it never matches more than once.
		if p.unicode || !atom.ahead {
			p.fail("Invalid quantifier")
		}
		if lo > 0 {
			return atom
		}
		return &node{kind: kindRepeat, children: []*node{atom}, min: 0, max: 0, greedy: greedy}
	}
	return &node{kind: kindRepeat, children: []*node{atom}, min: lo, max: hi, greedy: greedy}
}

// interval reads {n}, {n,} or {n,m} at the current position without consuming it and reports the bounds (hi is -1 when unbounded) and the length of the text.
func (p *parser) interval() (lo, hi, length int, ok bool) {
	if p.peek(0) != '{' {
		return 0, 0, 0, false
	}
	i := 1
	digits := func() (int, bool) {
		start, n := i, 0
		for d := p.peek(i); d >= '0' && d <= '9'; d = p.peek(i) {
			n = min(n*10+int(d-'0'), math.MaxInt32)
			i++
		}
		return n, i > start
	}
	lo, found := digits()
	if !found {
		return 0, 0, 0, false
	}
	hi = lo
	if p.peek(i) == ',' {
		i++
		if p.peek(i) == '}' {
			hi = -1
		} else if hi, found = digits(); !found {
			return 0, 0, 0, false
		}
	}
	if p.peek(i) != '}' {
		return 0, 0, 0, false
	}
	return lo, hi, i + 1, true
}

// group reads a parenthesised term.
func (p *parser) group() *node {
	p.pos++
	if !p.eat('?') {
		p.groups++
		index := p.groups
		return &node{kind: kindCapture, index: index, children: []*node{p.groupBody("", index)}}
	}
	switch {
	case p.eat(':'):
		return &node{kind: kindGroup, children: []*node{p.groupBody("", 0)}}
	case p.peek(0) == 'i' || p.peek(0) == 'm' || p.peek(0) == 's' || p.peek(0) == '-':
		saved := p.flags
		p.flags = p.modifierFlags()
		body := p.groupBody("", 0)
		p.flags = saved
		return &node{kind: kindGroup, children: []*node{body}}
	case p.eat('='):
		return &node{kind: kindLook, ahead: true, children: []*node{p.groupBody("", 0)}}
	case p.eat('!'):
		return &node{kind: kindLook, ahead: true, negated: true, children: []*node{p.groupBody("", 0)}}
	case p.peek(0) == '<':
		p.pos++
		switch {
		case p.eat('='):
			return &node{kind: kindLook, children: []*node{p.groupBody("", 0)}}
		case p.eat('!'):
			return &node{kind: kindLook, negated: true, children: []*node{p.groupBody("", 0)}}
		}
		name := p.groupName()
		p.groups++
		index := p.groups
		return &node{kind: kindCapture, index: index, children: []*node{p.groupBody(name, index)}}
	}
	p.fail("Invalid group")
	return nil
}

// modifierFlags reads the flags of a modifier group after "(?" up to and including the ":" and returns the flags that hold inside the group. V8 takes any of i, m and s before an optional "-" and any after it, no flag twice and none on both sides, and wants a flag on at least one side of a "-".
func (p *parser) modifierFlags() modifiers {
	flags := p.flags
	var added, removed modifiers
	dash := false
	for {
		c := p.peek(0)
		p.pos++
		var target, other *modifiers
		if dash {
			target, other = &removed, &added
		} else {
			target, other = &added, &removed
		}
		var flag, otherFlag *bool
		switch c {
		case 'i':
			flag, otherFlag = &target.ignoreCase, &other.ignoreCase
		case 'm':
			flag, otherFlag = &target.multiline, &other.multiline
		case 's':
			flag, otherFlag = &target.dotAll, &other.dotAll
		case '-':
			if dash {
				p.fail("Multiple dashes in flag group")
			}
			dash = true
			continue
		case ':':
			if dash && added == (modifiers{}) && removed == (modifiers{}) {
				p.fail("Invalid flag group")
			}
			if added.ignoreCase {
				flags.ignoreCase = true
			}
			if added.multiline {
				flags.multiline = true
			}
			if added.dotAll {
				flags.dotAll = true
			}
			if removed.ignoreCase {
				flags.ignoreCase = false
			}
			if removed.multiline {
				flags.multiline = false
			}
			if removed.dotAll {
				flags.dotAll = false
			}
			return flags
		default:
			p.fail("Invalid group")
		}
		if *flag || *otherFlag {
			p.fail("Repeated flag in flag group")
		}
		*flag = true
	}
}

// groupBody reads the disjunction of a group up to and including its ")". name is the name of the group, "" for an unnamed one, and index its number.
func (p *parser) groupBody(name string, index int) *node {
	p.leakIgnoreCase = p.flags.ignoreCase
	parent := p.scopes[len(p.scopes)-1]
	p.scopes = append(p.scopes, nameScope{index: p.groups, first: parent.first, second: parent.second})
	body := p.disjunction()
	scope := p.scopes[len(p.scopes)-1]
	p.scopes = p.scopes[:len(p.scopes)-1]
	if !p.eat(')') {
		p.fail("Unterminated group")
	}
	if name != "" {
		// V8 accepts a repeated name only for groups that its interval of earlier alternatives holds, however the groups nest.
		for _, earlier := range p.names[name] {
			if earlier < scope.first || earlier > scope.second {
				p.fail("Duplicate capture group name")
			}
		}
		p.names[name] = append(p.names[name], index)
	}
	return body
}

var (
	identifierOnce          sync.Once
	identifierStart, idPart runeSet
)

// groupName reads a group name after "<" up to and including the closing ">". Escapes \uXXXX and \u{X} are read as with the u flag whichever flags apply.
func (p *parser) groupName() string {
	identifierOnce.Do(func() { identifierStart, idPart = idStartSet(), idContinueSet() })
	var name strings.Builder
	for first := true; ; first = false {
		if p.done() {
			p.fail("Invalid capture group name")
		}
		c := p.src[p.pos]
		p.pos++
		switch {
		case c == '>':
			if first {
				p.fail("Invalid capture group name")
			}
			return name.String()
		case c == '\\':
			if !p.eat('u') {
				p.fail("Invalid capture group name")
			}
			c = p.unicodeEscape()
		case isLeadSurrogate(c) && isTrailSurrogate(p.peek(0)):
			c = 0x10000 + (c-0xD800)<<10 + (p.src[p.pos] - 0xDC00)
			p.pos++
		}
		switch {
		case c == '$' || c == '_' || identifierStart.contains(c):
		case !first && (c == 0x200C || c == 0x200D || idPart.contains(c)):
		default:
			p.fail("Invalid capture group name")
		}
		name.WriteRune(c)
	}
}

func isLeadSurrogate(c rune) bool  { return c >= 0xD800 && c <= 0xDBFF }
func isTrailSurrogate(c rune) bool { return c >= 0xDC00 && c <= 0xDFFF }

// unicodeEscape reads the rest of \u: four hex digits, a pair of them that spells a surrogate pair, or {hex digits}. Without the u flag only the four digits are an escape and the caller does not come here.
func (p *parser) unicodeEscape() rune {
	if p.eat('{') {
		value, digits := rune(0), 0
		for {
			d, ok := hexValue(p.peek(0))
			if !ok {
				break
			}
			value = value*16 + d
			if value > maxCodePoint {
				p.fail("Invalid Unicode escape")
			}
			digits++
			p.pos++
		}
		if digits == 0 || !p.eat('}') {
			p.fail("Invalid Unicode escape")
		}
		return value
	}
	lead, ok := p.hexDigits(4)
	if !ok {
		p.fail("Invalid Unicode escape")
	}
	if isLeadSurrogate(lead) && p.peek(0) == '\\' && p.peek(1) == 'u' {
		save := p.pos
		p.pos += 2
		if trail, ok := p.hexDigits(4); ok && isTrailSurrogate(trail) {
			return 0x10000 + (lead-0xD800)<<10 + (trail - 0xDC00)
		}
		p.pos = save
	}
	return lead
}

func hexValue(c rune) (rune, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// hexDigits reads exactly n hex digits, or nothing.
func (p *parser) hexDigits(n int) (rune, bool) {
	value := rune(0)
	for i := range n {
		d, ok := hexValue(p.peek(i))
		if !ok {
			return 0, false
		}
		value = value*16 + d
	}
	p.pos += n
	return value, true
}

var (
	digitSet = runeSet{{'0', '9'}}
	wordSet  = runeSet{{'0', '9'}, {'A', 'Z'}, {'_', '_'}, {'a', 'z'}}
	// spaceSet is WhiteSpace and LineTerminator.
	spaceSet = runeSet{{0x09, 0x0D}, {0x20, 0x20}, {0xA0, 0xA0}, {0x1680, 0x1680}, {0x2000, 0x200A}, {0x2028, 0x2029}, {0x202F, 0x202F}, {0x205F, 0x205F}, {0x3000, 0x3000}, {0xFEFF, 0xFEFF}}
	// lineTerminators are the characters "." does not match.
	lineTerminators = runeSet{{'\n', '\n'}, {'\r', '\r'}, {0x2028, 0x2029}}
)

// atomEscape reads an escape outside a class. It reports whether the escape was an assertion, which no quantifier may follow.
func (p *parser) atomEscape() (n *node, assertion bool) {
	p.pos++
	if p.done() {
		p.fail(`\ at end of pattern`)
	}
	c := p.src[p.pos]
	switch {
	case c == 'b' || c == 'B':
		p.pos++
		return &node{kind: kindAssert, assertion: c, fold: p.unicode && p.flags.ignoreCase}, true
	case c >= '1' && c <= '9':
		if index, ok := p.backReferenceIndex(); ok {
			return &node{kind: kindBackref, refs: []int{index}, fold: p.flags.ignoreCase}, false
		}
	case c == 'k' && (p.unicode || p.hasNamed):
		p.pos++
		if !p.eat('<') {
			p.fail("Invalid named reference")
		}
		ref := &node{kind: kindBackref, name: p.groupName(), fold: p.flags.ignoreCase}
		p.namedRefs = append(p.namedRefs, ref)
		return ref, false
	}
	if set, ok := p.classEscape(false); ok {
		return p.setNode(set), false
	}
	return p.charNode(p.characterEscape(false)), false
}

// backReferenceIndex reads the decimal number of \N when it names a capturing group of the pattern.
func (p *parser) backReferenceIndex() (int, bool) {
	i, value := 0, 0
	for d := p.peek(i); d >= '0' && d <= '9'; d = p.peek(i) {
		value = min(value*10+int(d-'0'), math.MaxInt32)
		i++
	}
	if value > p.captureCount {
		return 0, false
	}
	p.pos += i
	return value, true
}

// classEscape reads \d \D \w \W \s \S and, with the u flag, \p{...} and \P{...}, when one starts at the current position (after the backslash).
func (p *parser) classEscape(inClass bool) (runeSet, bool) {
	var set runeSet
	c := p.peek(0)
	switch c {
	case 'd', 'D':
		set = digitSet
	case 'w', 'W':
		set = p.wordChars()
	case 's', 'S':
		set = spaceSet
	case 'p', 'P':
		if !p.unicode {
			return nil, false
		}
		p.pos++
		set = p.property(inClass)
		if c == 'P' {
			set = set.complement(p.universe)
		}
		return set, true
	default:
		return nil, false
	}
	p.pos++
	if c == 'D' || c == 'W' || c == 'S' {
		set = set.complement(p.universe)
	}
	return set, true
}

// wordChars are the characters of \w: the ASCII word characters and, with the u and i flags, the two others that fold onto them (U+017F and U+212A).
func (p *parser) wordChars() runeSet {
	if p.unicode && p.leakIgnoreCase {
		return wordSet.union(runeSet{{0x17F, 0x17F}, {0x212A, 0x212A}})
	}
	return wordSet
}

// property reads {name} or {name=value} after \p or \P.
func (p *parser) property(inClass bool) runeSet {
	if !p.eat('{') {
		p.failProperty(inClass)
	}
	start := p.pos
	for !p.done() && p.src[p.pos] != '}' {
		p.pos++
	}
	if p.done() {
		p.failProperty(inClass)
	}
	expr := string(p.src[start:p.pos])
	p.pos++
	set, ok := propertyRanges(expr)
	if !ok {
		p.failProperty(inClass)
	}
	return set
}

// characterEscape reads the escape of one character after a backslash: the control escapes, \cX, \xHH, \uHHHH, \u{H...} with the u flag, a legacy octal escape and an identity escape. It returns the character.
func (p *parser) characterEscape(inClass bool) rune {
	if p.done() {
		p.fail(`\ at end of pattern`)
	}
	c := p.src[p.pos]
	p.pos++
	switch c {
	case 't':
		return '\t'
	case 'n':
		return '\n'
	case 'v':
		return '\v'
	case 'f':
		return '\f'
	case 'r':
		return '\r'
	case 'c':
		letter := p.peek(0)
		if (letter >= 'a' && letter <= 'z') || (letter >= 'A' && letter <= 'Z') {
			p.pos++
			return letter % 32
		}
		if p.unicode {
			p.fail("Invalid Unicode escape")
		}
		if inClass && ((letter >= '0' && letter <= '9') || letter == '_') {
			p.pos++
			return letter % 32
		}
		// The backslash stands for itself and the "c" is read again as an ordinary character.
		p.pos--
		return '\\'
	case '0':
		if d := p.peek(0); d < '0' || d > '9' {
			return 0
		}
		fallthrough
	case '1', '2', '3', '4', '5', '6', '7':
		if p.unicode {
			if c != '0' && !inClass {
				p.fail("Invalid escape")
			}
			p.fail("Invalid decimal escape")
		}
		p.pos--
		return p.legacyOctal()
	case 'x':
		if value, ok := p.hexDigits(2); ok {
			return value
		}
		if p.unicode {
			p.fail("Invalid escape")
		}
		return 'x'
	case 'u':
		if p.unicode {
			return p.unicodeEscape()
		}
		if value, ok := p.hexDigits(4); ok {
			return value
		}
		return 'u'
	}
	if p.unicode {
		if strings.ContainsRune(`^$\.*+?()[]{}|/`, c) {
			return c
		}
		p.fail("Invalid escape")
	}
	if c == 'k' && p.hasNamed {
		p.fail("Invalid escape")
	}
	return c
}

// legacyOctal reads an octal escape of up to three digits with a value up to 0o377.
func (p *parser) legacyOctal() rune {
	value := rune(0)
	for range 3 {
		d := p.peek(0)
		if d < '0' || d > '7' || value*8+(d-'0') > 0o377 {
			break
		}
		value = value*8 + (d - '0')
		p.pos++
	}
	return value
}

// A classAtom is one member of a class: a character or, for an escape like \d, a set.
type classAtom struct {
	char  rune
	set   runeSet
	isSet bool
}

// class reads a character class.
func (p *parser) class() *node {
	p.pos++
	negated := p.eat('^')
	var members []runeRange
	for {
		if p.done() {
			p.fail("Unterminated character class")
		}
		if p.eat(']') {
			break
		}
		first := p.classAtom()
		if p.peek(0) != '-' {
			members = appendAtom(members, first)
			continue
		}
		p.pos++
		if p.done() {
			p.fail("Unterminated character class")
		}
		if p.peek(0) == ']' {
			members = appendAtom(members, first)
			members = append(members, runeRange{'-', '-'})
			continue
		}
		last := p.classAtom()
		if first.isSet || last.isSet {
			if p.unicode {
				p.fail("Invalid character class")
			}
			members = appendAtom(members, first)
			members = append(members, runeRange{'-', '-'})
			members = appendAtom(members, last)
			continue
		}
		if first.char > last.char {
			p.fail("Range out of order in character class")
		}
		members = append(members, runeRange{first.char, last.char})
	}
	set := newRuneSet(members)
	if p.flags.ignoreCase {
		if !p.unicode && !negated && len(set) > 0 && set[0].lo > 0xFF {
			p.latin1Matters = true
			if p.oneByte {
				set = nil
			}
		}
		set = fold(set, p.unicode)
	}
	if negated {
		set = set.complement(p.universe)
	}
	return &node{kind: kindSet, set: set}
}

func appendAtom(members []runeRange, atom classAtom) []runeRange {
	if atom.isSet {
		return append(members, atom.set...)
	}
	return append(members, runeRange{atom.char, atom.char})
}

func (p *parser) classAtom() classAtom {
	c := p.src[p.pos]
	p.pos++
	if c != '\\' {
		return classAtom{char: c}
	}
	if p.done() {
		p.fail(`\ at end of pattern`)
	}
	switch p.src[p.pos] {
	case 'b':
		p.pos++
		return classAtom{char: '\b'}
	case 'B':
		if p.unicode {
			p.fail("Invalid escape")
		}
	case '-':
		if p.unicode {
			p.pos++
			return classAtom{char: '-'}
		}
	}
	if set, ok := p.classEscape(true); ok {
		return classAtom{set: set, isSet: true}
	}
	return classAtom{char: p.characterEscape(true)}
}
