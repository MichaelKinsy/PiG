package codingagent

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// translateJSRegExp reads pattern as V8 reads the source of new RegExp(pattern, "i"): the Annex B grammar of a pattern without the u flag. It returns the same pattern in regexp2's syntax, or V8's message when the pattern is a SyntaxError (jsRegExpErrorMessage adds the "Invalid regular expression" prefix).
func translateJSRegExp(pattern string) (string, string) {
	// Without the u flag a pattern is a sequence of UTF-16 code units, and so is the text it is matched against: an astral character is two units, and a quantifier binds to the second.
	units := jsstring.ToUTF16(pattern)
	in := make([]rune, len(units))
	for i, unit := range units {
		in[i] = rune(unit)
	}
	p := &jsRegExpTranslator{in: in, dotAll: []bool{false}, multiline: []bool{false}, ignoreCase: []bool{true}}
	p.scanCaptures()
	p.nameFrames = []*nameFrame{{base: map[string]bool{}, own: map[string]bool{}, union: map[string]bool{}}}
	p.disjunction()
	if p.err == "" && p.pos < len(p.in) {
		p.err = "Unmatched ')'"
	}
	if p.err == "" && p.duplicateName {
		p.err = "Duplicate capture group name"
	}
	if p.err == "" && p.undefinedReference {
		p.err = "Invalid named capture referenced"
	}
	if p.err != "" {
		return "", p.err
	}
	return p.out.String(), ""
}

// jsRegExpErrorMessage is the message of the SyntaxError thrown by new RegExp(pattern, "i") for a V8 reason.
func jsRegExpErrorMessage(pattern, reason string) string {
	return "Invalid regular expression: /" + pattern + "/i: " + reason
}

// nameFrame records the capture group names around one open group: base is every name declared before the group along the path from the pattern, own the names of the alternative being read, union those of its earlier alternatives.
type nameFrame struct {
	base, own, union map[string]bool
	// alternative counts the "|" the group has passed.
	alternative int
}

type jsRegExpTranslator struct {
	in  []rune
	pos int
	out strings.Builder
	err string
	// captures counts every capturing group of the pattern; names holds their names. V8 scans the whole pattern for both before it parses an escape.
	captures int
	names    map[string]bool
	// dotAll, multiline and ignoreCase are the (?s:), (?m:) and (?i:) flags in force, innermost last.
	dotAll, multiline, ignoreCase []bool
	// nameFrames holds the group names of each open group and of the whole pattern, innermost last.
	nameFrames []*nameFrame
	// undefinedReference is set by a \k<name> that names no group.
	undefinedReference bool
	// duplicateName is set by a group whose name only an enclosing alternative holds; V8 reports it once the whole pattern has parsed, while a name its own alternative holds is reported at the closing parenthesis.
	duplicateName bool
}

func (p *jsRegExpTranslator) fail(reason string) {
	if p.err == "" {
		p.err = reason
	}
}

func (p *jsRegExpTranslator) more() bool { return p.err == "" && p.pos < len(p.in) }

func (p *jsRegExpTranslator) peek(offset int) rune {
	if p.pos+offset < len(p.in) {
		return p.in[p.pos+offset]
	}
	return -1
}

func (p *jsRegExpTranslator) hasPrefix(s string) bool {
	for i, r := range []rune(s) {
		if p.peek(i) != r {
			return false
		}
	}
	return true
}

// scanCaptures counts the capturing groups and collects the names, skipping escapes and classes.
func (p *jsRegExpTranslator) scanCaptures() {
	p.names = map[string]bool{}
	inClass := false
	for i := 0; i < len(p.in); i++ {
		switch c := p.in[i]; {
		case c == '\\':
			i++
		case c == '[':
			inClass = true
		case c == ']':
			inClass = false
		case c == '(' && !inClass:
			if i+1 < len(p.in) && p.in[i+1] == '?' {
				if i+2 < len(p.in) && p.in[i+2] == '<' && (i+3 >= len(p.in) || (p.in[i+3] != '=' && p.in[i+3] != '!')) {
					p.captures++
					end := i + 3
					for end < len(p.in) && p.in[end] != '>' {
						end++
					}
					p.names[string(p.in[i+3:min(end, len(p.in))])] = true
				}
				continue
			}
			p.captures++
		}
	}
}

// disjunction translates alternatives up to an unmatched ")" or the end.
func (p *jsRegExpTranslator) disjunction() {
	for p.more() {
		switch p.in[p.pos] {
		case ')':
			return
		case '|':
			p.out.WriteByte('|')
			p.pos++
			frame := p.nameFrames[len(p.nameFrames)-1]
			for name := range frame.own {
				frame.union[name] = true
			}
			frame.own = map[string]bool{}
			frame.alternative++
		default:
			p.term()
		}
	}
}

func (p *jsRegExpTranslator) term() {
	c := p.in[p.pos]
	switch c {
	case '^':
		p.pos++
		if p.multiline[len(p.multiline)-1] {
			p.out.WriteString(`(?<![^\n\r\u2028\u2029])`)
		} else {
			p.out.WriteString(`\A`)
		}
		return
	case '$':
		p.pos++
		if p.multiline[len(p.multiline)-1] {
			p.out.WriteString(`(?![^\n\r\u2028\u2029])`)
		} else {
			p.out.WriteString(`\z`)
		}
		return
	case '*', '+', '?':
		p.fail("Nothing to repeat")
		return
	case '{':
		if _, _, _, ok := p.interval(); ok {
			p.fail("Nothing to repeat")
			return
		}
		p.pos++
		p.out.WriteString(`\{`)
	case '}', ']':
		p.pos++
		p.out.WriteString(`\` + string(c))
	case '.':
		p.pos++
		if p.dotAll[len(p.dotAll)-1] {
			p.out.WriteString(`[\s\S]`)
		} else {
			p.out.WriteString(`[^\n\r\u2028\u2029]`)
		}
	case '(':
		if !p.group() {
			return
		}
	case '[':
		p.class()
	case '\\':
		if p.escape() {
			return
		}
	default:
		p.pos++
		p.literal(c, false)
	}
	if p.err == "" {
		p.quantifier()
	}
}

// jsWordClass is the ASCII word class \b is defined by, and jsSpaceSet the members of \s (WhiteSpace and LineTerminator).
const (
	jsWordClass = `(?-i:[A-Za-z0-9_])`
	jsSpaceSet  = `\t\n\x0b\f\r\x20\xa0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff`
)

// unitEscape is one UTF-16 code unit in a form regexp2 reads as that unit.
func unitEscape(r rune) string {
	switch {
	case r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
		return string(r)
	case r < 0x100:
		return fmt.Sprintf(`\x%02x`, r)
	default:
		return fmt.Sprintf(`\u%04x`, r&0xFFFF)
	}
}

// literal writes one character outside a class. With the i flag it matches every unit that JavaScript's Canonicalize (the upper case of the unit, unless that is a single unit and not ASCII for a non-ASCII unit) maps to the same unit, which differs from regexp2's folding for U+0130, U+0131, U+017F, U+212A and others.
func (p *jsRegExpTranslator) literal(r rune, inClass bool) {
	if inClass || !p.ignoreCase[len(p.ignoreCase)-1] {
		p.out.WriteString(unitEscape(r))
		return
	}
	set := canonicalEquivalents(r)
	p.out.WriteString("(?-i:")
	if len(set) == 1 {
		p.out.WriteString(unitEscape(r))
	} else {
		p.out.WriteByte('[')
		for _, member := range set {
			p.out.WriteString(unitEscape(member))
		}
		p.out.WriteByte(']')
	}
	p.out.WriteByte(')')
}

var (
	canonicalClassesOnce sync.Once
	// canonicalClasses holds every set of two or more units that share a Canonicalize result, and canonicalClassOf the index of a unit's set.
	canonicalClasses [][]rune
	canonicalClassOf = map[rune]int{}
)

func buildCanonicalClasses() {
	groups := map[rune][]rune{}
	for r := range rune(0x10000) {
		canon := r
		if r < 0xD800 || r > 0xDFFF {
			upper := []rune(jsstring.ToUpper(string(r)))
			if len(upper) == 1 && (r < 128 || upper[0] >= 128) {
				canon = upper[0]
			}
		}
		groups[canon] = append(groups[canon], r)
	}
	for _, canon := range slices.Sorted(func(yield func(rune) bool) {
		for k := range groups {
			if !yield(k) {
				return
			}
		}
	}) {
		if members := groups[canon]; len(members) > 1 {
			for _, m := range members {
				canonicalClassOf[m] = len(canonicalClasses)
			}
			canonicalClasses = append(canonicalClasses, members)
		}
	}
}

// canonicalEquivalents is every unit that matches r under the i flag, r included.
func canonicalEquivalents(r rune) []rune {
	canonicalClassesOnce.Do(buildCanonicalClasses)
	if index, ok := canonicalClassOf[r]; ok {
		return canonicalClasses[index]
	}
	return []rune{r}
}

// canonicalClosure adds to the units of [low, high] every unit that matches one of them under the i flag.
func canonicalClosure(low, high rune) []rune {
	canonicalClassesOnce.Do(buildCanonicalClasses)
	var extra []rune
	for _, members := range canonicalClasses {
		inside := slices.ContainsFunc(members, func(m rune) bool { return m >= low && m <= high })
		if inside {
			for _, m := range members {
				if m < low || m > high {
					extra = append(extra, m)
				}
			}
		}
	}
	return extra
}

// interval reads {n}, {n,} or {n,m} at the current position without consuming it and reports its bounds and length.
func (p *jsRegExpTranslator) interval() (low, high, length int, ok bool) {
	if p.peek(0) != '{' {
		return 0, 0, 0, false
	}
	i := 1
	digits := func() (int, bool) {
		start := i
		n := 0
		for d := p.peek(i); d >= '0' && d <= '9'; d = p.peek(i) {
			if n < 1<<30 {
				n = n*10 + int(d-'0')
			}
			i++
		}
		return n, i > start
	}
	low, found := digits()
	if !found {
		return 0, 0, 0, false
	}
	high = low
	if p.peek(i) == ',' {
		i++
		if p.peek(i) == '}' {
			high = -1
		} else if high, found = digits(); !found {
			return 0, 0, 0, false
		}
	}
	if p.peek(i) != '}' {
		return 0, 0, 0, false
	}
	return low, high, i + 1, true
}

func (p *jsRegExpTranslator) quantifier() {
	switch p.peek(0) {
	case '*', '+', '?':
		p.out.WriteRune(p.in[p.pos])
		p.pos++
	case '{':
		low, high, length, ok := p.interval()
		if !ok {
			return
		}
		if high >= 0 && high < low {
			p.fail("numbers out of order in {} quantifier")
			return
		}
		p.out.WriteString(string(p.in[p.pos : p.pos+length]))
		p.pos += length
	default:
		return
	}
	if p.peek(0) == '?' {
		p.out.WriteByte('?')
		p.pos++
	}
}

// group translates a parenthesised term. It reports whether a quantifier may follow.
func (p *jsRegExpTranslator) group() bool {
	p.pos++
	opening, lookbehind, quantifiable := "(", false, true
	pushDot, pushMulti, pushCase := p.dotAll[len(p.dotAll)-1], p.multiline[len(p.multiline)-1], p.ignoreCase[len(p.ignoreCase)-1]
	declared := ""
	switch {
	case p.hasPrefix("?:"):
		p.pos += 2
		opening = "(?:"
	case p.hasPrefix("?="), p.hasPrefix("?!"):
		opening = "(?" + string(p.in[p.pos+1])
		p.pos += 2
	case p.hasPrefix("?<="), p.hasPrefix("?<!"):
		opening = "(?<" + string(p.in[p.pos+2])
		p.pos += 3
		lookbehind, quantifiable = true, false
	case p.hasPrefix("?<"):
		p.pos += 2
		name, ok := p.groupName()
		if !ok {
			return false
		}
		declared = name
		opening = "(?<" + name + ">"
	case p.hasPrefix("?"):
		flags, ok := p.flagGroup()
		if !ok {
			return false
		}
		opening = flags
		on, off, _ := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(flags, "(?"), ":"), "-")
		if strings.Contains(on, "s") {
			pushDot = true
		} else if strings.Contains(off, "s") {
			pushDot = false
		}
		if strings.Contains(on, "m") {
			pushMulti = true
		} else if strings.Contains(off, "m") {
			pushMulti = false
		}
		if strings.Contains(on, "i") {
			pushCase = true
		} else if strings.Contains(off, "i") {
			pushCase = false
		}
	}
	p.out.WriteString(opening)
	p.dotAll, p.multiline, p.ignoreCase = append(p.dotAll, pushDot), append(p.multiline, pushMulti), append(p.ignoreCase, pushCase)
	parent := p.nameFrames[len(p.nameFrames)-1]
	frame := &nameFrame{base: map[string]bool{}, own: map[string]bool{}, union: map[string]bool{}}
	for name := range parent.base {
		frame.base[name] = true
	}
	for name := range parent.own {
		frame.base[name] = true
	}
	p.nameFrames = append(p.nameFrames, frame)
	p.disjunction()
	p.nameFrames = p.nameFrames[:len(p.nameFrames)-1]
	p.dotAll, p.multiline, p.ignoreCase = p.dotAll[:len(p.dotAll)-1], p.multiline[:len(p.multiline)-1], p.ignoreCase[:len(p.ignoreCase)-1]
	if p.err != "" {
		return false
	}
	if p.peek(0) != ')' {
		p.fail("Unterminated group")
		return false
	}
	p.pos++
	p.out.WriteByte(')')
	if declared != "" {
		switch {
		case frame.own[declared] || parent.own[declared]:
			p.fail("Duplicate capture group name")
			return false
		case parent.base[declared]:
			// The earlier group lies outside every group that has moved on to a later alternative: V8 holds that report back until the pattern has parsed.
			later := slices.ContainsFunc(p.nameFrames, func(f *nameFrame) bool { return f.alternative > 0 && f.base[declared] })
			if !later {
				p.fail("Duplicate capture group name")
				return false
			}
			p.duplicateName = true
		}
		parent.own[declared] = true
	}
	for name := range frame.own {
		parent.own[name] = true
	}
	for name := range frame.union {
		parent.own[name] = true
	}
	if lookbehind {
		switch p.peek(0) {
		case '*', '+', '?':
			p.fail("Invalid quantifier")
		case '{':
			if low, high, _, ok := p.interval(); ok {
				if high >= 0 && high < low {
					p.fail("numbers out of order in {} quantifier")
				} else {
					p.fail("Invalid quantifier")
				}
			}
		}
		return false
	}
	_ = quantifiable
	return true
}

// groupName reads the name of a (?<name> group up to its ">", decoding \uXXXX and \u{X} escapes as V8 does.
func (p *jsRegExpTranslator) groupName() (string, bool) {
	var name []rune
	for {
		if p.pos >= len(p.in) {
			p.fail("Invalid capture group name")
			return "", false
		}
		c := p.in[p.pos]
		p.pos++
		switch c {
		case '>':
			if len(name) == 0 {
				p.fail("Invalid capture group name")
				return "", false
			}
			return string(name), true
		case '\\':
			if p.peek(0) != 'u' {
				p.fail("Invalid capture group name")
				return "", false
			}
			p.pos++
			if p.peek(0) == '{' {
				end := p.pos + 1
				for end < len(p.in) && p.in[end] != '}' {
					end++
				}
				v, err := strconv.ParseUint(string(p.in[p.pos+1:min(end, len(p.in))]), 16, 32)
				if end >= len(p.in) || err != nil || v > 0x10FFFF || end == p.pos+1 {
					p.fail("Invalid Unicode escape")
					return "", false
				}
				p.pos = end + 1
				name = append(name, rune(v))
				if !validCaptureGroupName(string(name)) {
					p.fail("Invalid capture group name")
					return "", false
				}
				continue
			}
			v, ok := p.hexDigits(4)
			if !ok {
				p.fail("Invalid Unicode escape")
				return "", false
			}
			name = append(name, v)
		default:
			name = append(name, c)
		}
		if !validCaptureGroupName(string(name)) {
			p.fail("Invalid capture group name")
			return "", false
		}
	}
}

// flagGroup reads the modifiers of (?ims-ims:, which V8 accepts.
func (p *jsRegExpTranslator) flagGroup() (string, bool) {
	start := p.pos
	p.pos++
	seen := map[rune]bool{}
	dash := false
	for ; p.pos < len(p.in); p.pos++ {
		switch c := p.in[p.pos]; c {
		case 'i', 'm', 's':
			if seen[c] {
				p.fail("Repeated flag in flag group")
				return "", false
			}
			seen[c] = true
		case '-':
			if dash {
				p.fail("Multiple dashes in flag group")
				return "", false
			}
			dash = true
		case ':':
			if p.pos == start+1 || (dash && p.pos == start+2 && len(seen) == 0) {
				p.fail("Invalid flag group")
				return "", false
			}
			p.pos++
			return "(?" + string(p.in[start+1:p.pos]), true
		default:
			p.fail("Invalid group")
			return "", false
		}
	}
	p.fail("Invalid group")
	return "", false
}

func validCaptureGroupName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r == '$' || r == '_' || unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) {
			continue
		}
		if i > 0 && (unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Pc, r) || r == 0x200c || r == 0x200d) {
			continue
		}
		return false
	}
	return true
}

// escape translates a backslash escape outside a class. It reports whether the escape was an assertion, which no quantifier may follow.
func (p *jsRegExpTranslator) escape() bool {
	p.pos++
	if p.pos >= len(p.in) {
		p.fail(`\ at end of pattern`)
		return true
	}
	c := p.in[p.pos]
	p.pos++
	switch {
	case c == 'b':
		p.out.WriteString(`(?:(?<=` + jsWordClass + `)(?!` + jsWordClass + `)|(?<!` + jsWordClass + `)(?=` + jsWordClass + `))`)
		return true
	case c == 'B':
		p.out.WriteString(`(?:(?<=` + jsWordClass + `)(?=` + jsWordClass + `)|(?<!` + jsWordClass + `)(?!` + jsWordClass + `))`)
		return true
	case c == 's':
		p.out.WriteString(`[` + jsSpaceSet + `]`)
	case c == 'S':
		p.out.WriteString(`[^` + jsSpaceSet + `]`)
	case c == 'd', c == 'D':
		p.out.WriteString(`\` + string(c))
	case c == 'w', c == 'W':
		// regexp2 folds U+0130 and U+212A into the ASCII letters of \w when it ignores case.
		p.out.WriteString(`(?-i:\` + string(c) + `)`)
	case c == 'k' && len(p.names) > 0:
		start := p.pos
		if p.peek(0) != '<' {
			p.fail("Invalid named reference")
			return true
		}
		end := start + 1
		for end < len(p.in) && p.in[end] != '>' {
			end++
		}
		name := string(p.in[start+1 : min(end, len(p.in))])
		if end >= len(p.in) || !validCaptureGroupName(name) {
			p.fail("Invalid named reference")
			return true
		}
		if !p.names[name] {
			// V8 reports a reference to no group once the whole pattern has parsed.
			p.undefinedReference = true
		}
		p.pos = end + 1
		p.out.WriteString(`\k<` + name + `>`)
	case c >= '1' && c <= '9':
		p.pos--
		start := p.pos
		n := 0
		for d := p.peek(0); d >= '0' && d <= '9'; d = p.peek(0) {
			if n < 1<<20 {
				n = n*10 + int(d-'0')
			}
			p.pos++
		}
		if n <= p.captures {
			fmt.Fprintf(&p.out, `\%d`, n)
			break
		}
		p.pos = start
		p.legacyOctalOrDigit(false)
	case c == '0':
		p.pos--
		p.legacyOctalOrDigit(false)
	default:
		p.pos--
		r := p.escapedCharacter(false)
		p.literal(r, false)
	}
	return false
}

// legacyOctalOrDigit reads \0 and the escapes \1 to \9 that name no group: the digit 8 or 9 itself, or an octal escape of up to three digits with a value up to 0o377.
func (p *jsRegExpTranslator) legacyOctalOrDigit(inClass bool) {
	p.literal(p.legacyOctal(), inClass)
}

func (p *jsRegExpTranslator) legacyOctal() rune {
	first := p.in[p.pos]
	if first == '8' || first == '9' {
		p.pos++
		return first
	}
	value := 0
	for range 3 {
		d := p.peek(0)
		if d < '0' || d > '7' || value*8+int(d-'0') > 0o377 {
			break
		}
		value = value*8 + int(d-'0')
		p.pos++
	}
	return rune(value)
}

// escapedCharacter reads the character escape at the current position (after the backslash) and returns the character it names: \t \n \v \f \r, \cX, \xHH, \uHHHH, and an identity escape. A \u or \x without its digits is the letter itself, as without the u flag.
func (p *jsRegExpTranslator) escapedCharacter(inClass bool) rune {
	c := p.in[p.pos]
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
		if (letter >= 'a' && letter <= 'z') || (letter >= 'A' && letter <= 'Z') || (inClass && ((letter >= '0' && letter <= '9') || letter == '_')) {
			p.pos++
			return letter % 32
		}
		p.pos--
		return '\\'
	case 'x':
		if v, ok := p.hexDigits(2); ok {
			return v
		}
		return 'x'
	case 'u':
		if v, ok := p.hexDigits(4); ok {
			return v
		}
		return 'u'
	}
	return c
}

func (p *jsRegExpTranslator) hexDigits(n int) (rune, bool) {
	if p.pos+n > len(p.in) {
		return 0, false
	}
	v, err := strconv.ParseUint(string(p.in[p.pos:p.pos+n]), 16, 32)
	if err != nil || strings.ContainsAny(string(p.in[p.pos:p.pos+n]), "+-_") {
		return 0, false
	}
	p.pos += n
	return rune(v), true
}

// class translates a character class. An escape that names a set (\d, \w, \s) cannot be an end of a range: without the u flag the "-" is then a member (Annex B).
func (p *jsRegExpTranslator) class() {
	p.pos++
	negate := false
	if p.peek(0) == '^' {
		negate = true
		p.pos++
	}
	type member struct {
		set  string
		char rune
	}
	var members []member
	next := func() (member, bool) {
		c := p.in[p.pos]
		if c != '\\' {
			p.pos++
			return member{char: c}, false
		}
		p.pos++
		if p.pos >= len(p.in) {
			p.fail(`\ at end of pattern`)
			return member{}, false
		}
		switch e := p.in[p.pos]; {
		case e == 'k' && len(p.names) > 0:
			p.fail("Invalid escape")
			return member{}, false
		case e == 'd' || e == 'D' || e == 'w' || e == 'W' || e == 's' || e == 'S':
			p.pos++
			return member{set: `\` + string(e)}, true
		case e == 'b':
			p.pos++
			return member{char: 8}, false
		case e == '-':
			p.pos++
			return member{char: '-'}, false
		case e >= '0' && e <= '9':
			return member{char: p.legacyOctal()}, false
		}
		return member{char: p.escapedCharacter(true)}, false
	}
	for {
		if p.pos >= len(p.in) {
			p.fail("Unterminated character class")
			return
		}
		if p.in[p.pos] == ']' {
			p.pos++
			break
		}
		low, lowSet := next()
		if p.err != "" {
			return
		}
		if p.peek(0) == '-' && p.peek(1) != ']' && p.peek(1) != -1 {
			p.pos++
			high, highSet := next()
			if p.err != "" {
				return
			}
			if lowSet || highSet {
				members = append(members, low, member{char: '-'}, high)
				continue
			}
			if low.char > high.char {
				p.fail("Range out of order in character class")
				return
			}
			members = append(members, member{set: "range"}, member{char: low.char}, member{char: high.char})
			continue
		}
		members = append(members, low)
	}
	ignoreCase := p.ignoreCase[len(p.ignoreCase)-1]
	var chars strings.Builder
	notSpace := false
	for i := 0; i < len(members); i++ {
		m := members[i]
		switch {
		case m.set == "range":
			low, high := members[i+1].char, members[i+2].char
			chars.WriteString(unitEscape(low) + "-" + unitEscape(high))
			if ignoreCase {
				for _, extra := range canonicalClosure(low, high) {
					chars.WriteString(unitEscape(extra))
				}
			}
			i += 2
		case m.set == `\s`:
			chars.WriteString(jsSpaceSet)
		case m.set == `\S`:
			notSpace = true
		case m.set != "":
			chars.WriteString(m.set)
		case ignoreCase:
			for _, member := range canonicalEquivalents(m.char) {
				chars.WriteString(unitEscape(member))
			}
		default:
			chars.WriteString(unitEscape(m.char))
		}
	}
	var text string
	switch {
	case chars.Len() == 0 && !notSpace && negate:
		text = `[\s\S]`
	case chars.Len() == 0 && !notSpace:
		text = `(?!)`
	case notSpace && negate && chars.Len() > 0:
		text = `(?![` + chars.String() + `]|[^` + jsSpaceSet + `])[\s\S]`
	case notSpace && negate:
		text = `(?![^` + jsSpaceSet + `])[\s\S]`
	case notSpace && chars.Len() > 0:
		text = `(?:[` + chars.String() + `]|[^` + jsSpaceSet + `])`
	case notSpace:
		text = `[^` + jsSpaceSet + `]`
	case negate:
		text = `[^` + chars.String() + `]`
	default:
		text = `[` + chars.String() + `]`
	}
	if ignoreCase {
		// The sets are already closed under JavaScript's case mapping, so regexp2 must not fold them again.
		text = "(?-i:" + text + ")"
	}
	p.out.WriteString(text)
}
