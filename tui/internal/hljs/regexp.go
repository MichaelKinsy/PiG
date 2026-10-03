package hljs

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/dlclark/regexp2"
)

// jsRegExp is a JavaScript RegExp (without the u or v flag) evaluated over UTF-16 code units, the unit JavaScript strings index by. Every highlight.js pattern carries the m flag and, in a case_insensitive language, the i flag; the g flag only selects lastIndex scanning, which exec's start argument expresses.
type jsRegExp struct {
	source     string
	ignoreCase bool
	groups     int
	re         *regexp2.Regexp
	// anchored matches only at the start index; startsWith uses it for exec(...).index === 0.
	anchored *regexp2.Regexp
}

// jsMatch is a RegExp exec result: group spans in code units, start -1 for a group that did not participate (undefined).
type jsMatch struct {
	index int
	spans []int
}

type regexpKey struct {
	source     string
	ignoreCase bool
}

var (
	regexpCacheMu sync.Mutex
	regexpCache   = map[regexpKey]*jsRegExp{}
)

// newJSRegExp is `new RegExp(source, "m" + (ignoreCase ? "i" : ""))`. Compiled patterns are shared: a RegExp's matching state lives in the caller's lastIndex.
func newJSRegExp(source string, ignoreCase bool) (*jsRegExp, error) {
	key := regexpKey{source, ignoreCase}
	regexpCacheMu.Lock()
	cached := regexpCache[key]
	regexpCacheMu.Unlock()
	if cached != nil {
		return cached, nil
	}
	translated, groups, err := translateJSRegExp(source, ignoreCase)
	if err != nil {
		return nil, err
	}
	re, err := regexp2.Compile(translated, regexp2.ECMAScript)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression /%s/: %w", source, err)
	}
	anchored, err := regexp2.Compile(`\G(?:`+translated+`)`, regexp2.ECMAScript)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression /%s/: %w", source, err)
	}
	compiled := &jsRegExp{source: source, ignoreCase: ignoreCase, groups: groups, re: re, anchored: anchored}
	regexpCacheMu.Lock()
	regexpCache[key] = compiled
	regexpCacheMu.Unlock()
	return compiled, nil
}

// exec is RegExp.prototype.exec with lastIndex = start: the leftmost match at or after start. Lookbehind and ^ still see the units before start.
func (r *jsRegExp) exec(input []rune, start int) *jsMatch {
	if start > len(input) {
		return nil
	}
	return r.result(r.re.FindRunesMatchStartingAt(input, start))
}

// matchesAtStart is highlight.js startsWith: re.exec(input) finds a match at index 0. The leftmost match has index 0 exactly when some match starts at 0.
func (r *jsRegExp) matchesAtStart(input []rune) bool {
	m, err := r.anchored.FindRunesMatchStartingAt(input, 0)
	return err == nil && m != nil
}

func (r *jsRegExp) result(m *regexp2.Match, err error) *jsMatch {
	if err != nil || m == nil {
		return nil
	}
	spans := make([]int, 2*(r.groups+1))
	for i := 0; i <= r.groups; i++ {
		g := m.GroupByNumber(i)
		if g == nil || len(g.Captures) == 0 {
			spans[2*i], spans[2*i+1] = -1, -1
			continue
		}
		spans[2*i], spans[2*i+1] = g.Index, g.Index+g.Length
	}
	return &jsMatch{index: m.Index, spans: spans}
}

// translateJSRegExp rewrites a JavaScript pattern (Annex B grammar, m flag, optional i flag) into an equivalent regexp2 ECMAScript-mode pattern over UTF-16 code units.
//
// regexp2's ECMAScript mode already gives \d, \w, \s and backreferences JavaScript's meaning. The rewrite supplies the rest: ASCII word boundaries; line terminators for ., ^ and $; JavaScript's ignoreCase canonicalization; empty and complete classes; Annex B escapes and literal braces; and named groups, which regexp2 would renumber after the unnamed groups.
func translateJSRegExp(source string, ignoreCase bool) (string, int, error) {
	t := &regexpTranslator{src: toUnits(source), ignoreCase: ignoreCase}
	t.scanGroups()
	t.translate()
	if t.err != nil {
		return "", 0, fmt.Errorf("invalid regular expression /%s/: %w", source, t.err)
	}
	return t.out.String(), t.groupCount, nil
}

type regexpTranslator struct {
	src        []rune
	pos        int
	ignoreCase bool
	groupCount int
	names      map[string]int
	out        strings.Builder
	err        error
}

const lineTerminatorClass = `\n\r\u2028\u2029`

// JavaScript's \b and \B test the ASCII word characters; regexp2's ECMAScript boundary tests Unicode letters and digits.
const (
	asciiWordBoundary    = `(?:(?<=[A-Za-z0-9_])(?![A-Za-z0-9_])|(?<![A-Za-z0-9_])(?=[A-Za-z0-9_]))`
	asciiNonWordBoundary = `(?:(?<=[A-Za-z0-9_])(?=[A-Za-z0-9_])|(?<![A-Za-z0-9_])(?![A-Za-z0-9_]))`
)

// scanGroups counts capturing groups and numbers named groups in source order, as JavaScript does before it parses backreferences.
func (t *regexpTranslator) scanGroups() {
	src := t.src
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\\':
			i++
		case '[':
			i = classEnd(src, i)
		case '(':
			if i+1 < len(src) && src[i+1] == '?' {
				if i+2 < len(src) && src[i+2] == '<' && i+3 < len(src) && src[i+3] != '=' && src[i+3] != '!' {
					t.groupCount++
					end := i + 3
					for end < len(src) && src[end] != '>' {
						end++
					}
					if t.names == nil {
						t.names = map[string]int{}
					}
					t.names[string(src[i+3:end])] = t.groupCount
				}
				continue
			}
			t.groupCount++
		}
	}
}

// classEnd returns the index of the ] closing the class that opens at start. In JavaScript a ] directly after [ or [^ closes the class.
func classEnd(src []rune, start int) int {
	i := start + 1
	if i < len(src) && src[i] == '^' {
		i++
	}
	for ; i < len(src); i++ {
		switch src[i] {
		case '\\':
			i++
		case ']':
			return i
		}
	}
	return len(src)
}

func (t *regexpTranslator) fail(format string, args ...any) {
	if t.err == nil {
		t.err = fmt.Errorf(format, args...)
	}
}

func (t *regexpTranslator) peek(offset int) rune {
	if t.pos+offset < len(t.src) {
		return t.src[t.pos+offset]
	}
	return -1
}

func (t *regexpTranslator) translate() {
	for t.pos < len(t.src) && t.err == nil {
		c := t.src[t.pos]
		switch c {
		case '|', ')':
			t.out.WriteRune(c)
			t.pos++
		case '(':
			t.translateGroupOpen()
		case '^':
			t.out.WriteString(`(?<![^` + lineTerminatorClass + `])`)
			t.pos++
		case '$':
			t.out.WriteString(`(?![^` + lineTerminatorClass + `])`)
			t.pos++
		case '.':
			t.out.WriteString(`[^` + lineTerminatorClass + `]`)
			t.pos++
		case '*', '+', '?':
			t.out.WriteRune(c)
			t.pos++
			t.lazySuffix()
		case '{':
			if n := braceQuantifierLength(t.src[t.pos:]); n > 0 {
				t.out.WriteString(string(t.src[t.pos : t.pos+n]))
				t.pos += n
				t.lazySuffix()
				continue
			}
			t.literal('{')
			t.pos++
		case '[':
			t.translateClass()
		case '\\':
			t.translateEscape()
		default:
			t.literal(c)
			t.pos++
		}
	}
}

func (t *regexpTranslator) lazySuffix() {
	if t.peek(0) == '?' {
		t.out.WriteByte('?')
		t.pos++
	}
}

// braceQuantifierLength is the length of a {n}, {n,} or {n,m} quantifier at the start of src, or 0 when the brace is a literal (Annex B ExtendedPatternCharacter).
func braceQuantifierLength(src []rune) int {
	i := 1
	digits := func() int {
		start := i
		for i < len(src) && src[i] >= '0' && src[i] <= '9' {
			i++
		}
		return i - start
	}
	if digits() == 0 {
		return 0
	}
	if i < len(src) && src[i] == ',' {
		i++
		digits()
	}
	if i < len(src) && src[i] == '}' {
		return i + 1
	}
	return 0
}

func (t *regexpTranslator) translateGroupOpen() {
	if t.peek(1) != '?' {
		t.out.WriteByte('(')
		t.pos++
		return
	}
	switch {
	case t.peek(2) == ':' || t.peek(2) == '=' || t.peek(2) == '!':
		t.out.WriteString(string(t.src[t.pos : t.pos+3]))
		t.pos += 3
	case t.peek(2) == '<' && (t.peek(3) == '=' || t.peek(3) == '!'):
		t.out.WriteString(string(t.src[t.pos : t.pos+4]))
		t.pos += 4
	case t.peek(2) == '<':
		end := t.pos + 3
		for end < len(t.src) && t.src[end] != '>' {
			end++
		}
		if end >= len(t.src) {
			t.fail("unterminated group name")
			return
		}
		// Named groups become plain groups so they keep their source-order number.
		t.out.WriteByte('(')
		t.pos = end + 1
	default:
		t.fail("invalid group")
	}
}

// literal emits one pattern character. Under the i flag it matches every code unit with the same canonical form.
func (t *regexpTranslator) literal(unit rune) {
	if t.ignoreCase {
		if class := caseClass(unit); len(class) > 1 {
			t.out.WriteByte('[')
			for _, member := range class {
				writeClassUnit(&t.out, member)
			}
			t.out.WriteByte(']')
			return
		}
	}
	writePatternUnit(&t.out, unit)
}

func writePatternUnit(out *strings.Builder, unit rune) {
	if unit < 0x80 && (unit >= 'a' && unit <= 'z' || unit >= 'A' && unit <= 'Z' || unit >= '0' && unit <= '9' || unit == '_') {
		out.WriteRune(unit)
		return
	}
	fmt.Fprintf(out, `\u%04X`, unit)
}

func writeClassUnit(out *strings.Builder, unit rune) {
	fmt.Fprintf(out, `\u%04X`, unit)
}

func (t *regexpTranslator) translateEscape() {
	t.pos++ // backslash
	if t.pos >= len(t.src) {
		t.fail(`\ at end of pattern`)
		return
	}
	c := t.src[t.pos]
	switch c {
	case 'b':
		t.out.WriteString(asciiWordBoundary)
		t.pos++
		return
	case 'B':
		t.out.WriteString(asciiNonWordBoundary)
		t.pos++
		return
	case 'd', 'D', 'w', 'W', 's', 'S':
		t.out.WriteByte('\\')
		t.out.WriteRune(c)
		t.pos++
		return
	case 'k':
		if t.names != nil {
			if t.peek(1) != '<' {
				t.fail(`invalid named reference`)
				return
			}
			end := t.pos + 2
			for end < len(t.src) && t.src[end] != '>' {
				end++
			}
			group, ok := t.names[string(t.src[t.pos+2:min(end, len(t.src))])]
			if !ok || end >= len(t.src) {
				t.fail(`invalid named capture referenced`)
				return
			}
			t.backreference(group)
			t.pos = end + 1
			return
		}
	case 'c':
		if next := t.peek(1); next >= 'a' && next <= 'z' || next >= 'A' && next <= 'Z' {
			t.literal(next % 32)
			t.pos += 2
			return
		}
		// Annex B: \c without a control letter is a literal backslash; the c is read again as a pattern character.
		t.literal('\\')
		return
	}
	if c >= '1' && c <= '9' {
		end := t.pos
		for end < len(t.src) && t.src[end] >= '0' && t.src[end] <= '9' {
			end++
		}
		if n, err := strconv.Atoi(string(t.src[t.pos:end])); err == nil && n <= t.groupCount {
			t.backreference(n)
			t.pos = end
			return
		}
	}
	unit, length := characterEscape(t.src[t.pos:], false)
	t.literal(unit)
	t.pos += length
}

func (t *regexpTranslator) backreference(group int) {
	if t.ignoreCase {
		fmt.Fprintf(&t.out, `(?i:\%d)`, group)
		return
	}
	fmt.Fprintf(&t.out, `(?:\%d)`, group)
}

// characterEscape decodes the CharacterEscape (Annex B) that follows a backslash and returns its code unit and length. inClass selects ClassEscape's \b (backspace) and the class-only \c forms.
func characterEscape(src []rune, inClass bool) (rune, int) {
	c := src[0]
	switch c {
	case 't':
		return '\t', 1
	case 'n':
		return '\n', 1
	case 'v':
		return '\v', 1
	case 'f':
		return '\f', 1
	case 'r':
		return '\r', 1
	case 'b':
		if inClass {
			return '\b', 1
		}
	case 'c':
		if len(src) > 1 && (src[1] >= 'a' && src[1] <= 'z' || src[1] >= 'A' && src[1] <= 'Z' || inClass && (src[1] >= '0' && src[1] <= '9' || src[1] == '_')) {
			return src[1] % 32, 2
		}
		return '\\', 0
	case 'x':
		if len(src) >= 3 && isHex(src[1]) && isHex(src[2]) {
			return hexValue(src[1:3]), 3
		}
	case 'u':
		if len(src) >= 5 && isHex(src[1]) && isHex(src[2]) && isHex(src[3]) && isHex(src[4]) {
			return hexValue(src[1:5]), 5
		}
	case '0', '1', '2', '3', '4', '5', '6', '7':
		// LegacyOctalEscapeSequence: OctalDigit, then one more OctalDigit, then a third only after a ZeroToThree.
		isOctal := func(i int) bool { return i < len(src) && src[i] >= '0' && src[i] <= '7' }
		value := c - '0'
		if !isOctal(1) {
			return value, 1
		}
		value = value*8 + src[1] - '0'
		if c > '3' || !isOctal(2) {
			return value, 2
		}
		return value*8 + src[2] - '0', 3
	}
	// IdentityEscape: any other character stands for itself (including \8 and \9).
	return c, 1
}

func isHex(c rune) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexValue(digits []rune) rune {
	var value rune
	for _, c := range digits {
		value <<= 4
		switch {
		case c >= '0' && c <= '9':
			value |= c - '0'
		case c >= 'a' && c <= 'f':
			value |= c - 'a' + 10
		default:
			value |= c - 'A' + 10
		}
	}
	return value
}

type classRange struct{ lo, hi rune }

// translateClass rewrites a character class. Ranges and characters are emitted as code units; \d, \w, \s and their complements are closed under ignoreCase canonicalization already, so they pass through.
func (t *regexpTranslator) translateClass() {
	end := classEnd(t.src, t.pos)
	if end >= len(t.src) {
		t.fail("unterminated character class")
		return
	}
	i := t.pos + 1
	negate := false
	if t.src[i] == '^' {
		negate = true
		i++
	}
	var ranges []classRange
	var escapes []rune
	// atom reads one ClassAtom; escape is the class escape letter for \d, \w, \s and their complements.
	atom := func() (unit rune, escape rune) {
		c := t.src[i]
		if c != '\\' {
			i++
			return c, 0
		}
		i++
		switch e := t.src[i]; e {
		case 'd', 'D', 'w', 'W', 's', 'S':
			i++
			return 0, e
		case '-':
			i++
			return '-', 0
		}
		unit, length := characterEscape(t.src[i:end], true)
		i += length
		return unit, 0
	}
	for i < end {
		lo, loEscape := atom()
		if i+1 < end && t.src[i] == '-' && loEscape == 0 {
			save := i
			i++
			hi, hiEscape := atom()
			if hiEscape == 0 {
				if hi < lo {
					t.fail("range out of order in character class")
					return
				}
				ranges = append(ranges, classRange{lo, hi})
				continue
			}
			// Annex B: a range with a class escape at either end is the two atoms and a literal hyphen.
			i = save
		}
		if loEscape != 0 {
			escapes = append(escapes, loEscape)
			continue
		}
		ranges = append(ranges, classRange{lo, lo})
	}
	t.pos = end + 1
	if t.ignoreCase {
		ranges = appendCaseClosure(ranges)
	}
	switch {
	case len(ranges) == 0 && len(escapes) == 0 && !negate:
		t.out.WriteString(`(?!)`)
		return
	case len(ranges) == 0 && len(escapes) == 0 && negate:
		t.out.WriteString(`[\s\S]`)
		return
	}
	t.out.WriteByte('[')
	if negate {
		t.out.WriteByte('^')
	}
	for _, r := range ranges {
		writeClassUnit(&t.out, r.lo)
		if r.hi != r.lo {
			t.out.WriteByte('-')
			writeClassUnit(&t.out, r.hi)
		}
	}
	for _, e := range escapes {
		t.out.WriteByte('\\')
		t.out.WriteRune(e)
	}
	t.out.WriteByte(']')
}

// appendCaseClosure adds, for every class member, each code unit with the same ignoreCase canonical form (ECMA-262 CharacterSetMatcher with rer.[[IgnoreCase]]).
func appendCaseClosure(ranges []classRange) []classRange {
	var extra []classRange
	for _, unit := range caseFoldedUnits() {
		for _, r := range ranges {
			if unit >= r.lo && unit <= r.hi {
				for _, member := range caseClass(unit) {
					extra = append(extra, classRange{member, member})
				}
				break
			}
		}
	}
	return append(ranges, extra...)
}

func toUnits(text string) []rune {
	units := make([]rune, 0, len(text))
	for _, r := range text {
		if r > 0xffff {
			r -= 0x10000
			units = append(units, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			continue
		}
		units = append(units, r)
	}
	return units
}
