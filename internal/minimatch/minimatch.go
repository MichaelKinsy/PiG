// Package minimatch ports minimatch 11.0.3 (the glob matcher Pi's own minimatch() calls use for model scopes and package resource filters) with @isaacs/nonrecursive-glob-match 1.0.3 and brace-expansion 5.0.12: brace expansion, `!` negation, `**`, character classes, extglobs and dot handling. Matching of one path portion is a state-machine simulation, not a regular expression. It does not port the options that no PiG caller sets.
package minimatch

// Ports minimatch 11.0.3 index.js (Minimatch.make, mergeRows, levelOneOptimize, parse, matchRow, matchGlobstar, matchOne, match) with optimizationLevel 1.

import (
	"errors"
	"runtime"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// Options are the minimatch options that change a match. Every other option has its default.
type Options struct {
	// NoCase makes the match case-insensitive.
	NoCase bool
	// NoExt turns extglobs off: `+(a|b)` is literal text.
	NoExt bool
	// Dot lets wildcards match a leading dot.
	Dot bool
	// NoNegate makes a leading `!` literal instead of a negation.
	NoNegate bool
	// Platform is minimatch's platform option: "win32" reads Windows drive letters and UNC roots, anything else, "", means the platform of the running process.
	Platform string
}

// maxPatternLength is MAX_PATTERN_LENGTH, in UTF-16 code units.
// upstream: node_modules/minimatch/dist/esm/assert-valid-pattern.js:MAX_PATTERN_LENGTH
const maxPatternLength = 1024 * 64

// maxGlobstarRecursion is the default of the option of that name.
// upstream: node_modules/minimatch/dist/esm/index.js:maxGlobstarRecursion
const maxGlobstarRecursion = 200

// ErrPatternTooLong is the TypeError of assertValidPattern.
var ErrPatternTooLong = errors.New("pattern is too long")

// portion is one path portion of a compiled pattern: a string, a matcher or the globstar.
type portion struct {
	globstar bool
	s        string
	m        *matcher
}

func (p portion) isString() bool { return !p.globstar && p.m == nil }

// isEmpty is a portion that is the empty string.
func (p portion) isEmpty() bool { return p.isString() && p.s == "" }

// Minimatch is a compiled pattern.
type Minimatch struct {
	options   Options
	pattern   string
	negate    bool
	comment   bool
	empty     bool
	isWindows bool
	nocase    bool
	// windowsNoMagicRoot keeps the root of a Windows path a string, so that case in it is compared as the platform does.
	windowsNoMagicRoot bool
	set                [][]portion
	globParts          [][]string
	matchers           map[string]*matcher
}

var (
	braceRE      = lazyregexp.New(`\{[^{\n\r\x{2028}\x{2029}]*\}`)
	slashSplitRE = lazyregexp.New(`/+`)
	uncRootRE    = lazyregexp.New(`^//[^/]+`)
	globMagicRE  = lazyregexp.New(`[?*]|[+@!]\([^\n\r\x{2028}\x{2029}]*?\)|\[|\]`)
	driveRE      = lazyregexp.New(`^[a-zA-Z]:$`)
	driveStartRE = lazyregexp.New(`^[a-zA-Z]:`)
	longUNCRE    = lazyregexp.New(`^[uU][nN][cC]$`)
	emptyExtRE   = lazyregexp.New(`^[?*+@]\(\)$`)
)

// Match reports whether name matches the glob pattern, as minimatch(name, pattern, options). A pattern over MAX_PATTERN_LENGTH, where minimatch throws, matches nothing.
func Match(name, pattern string, options Options) bool {
	if jsstring.Length(pattern) > maxPatternLength {
		return false
	}
	// shortcut: comments match nothing.
	if strings.HasPrefix(pattern, "#") {
		return false
	}
	m, err := New(pattern, options)
	return err == nil && m.Match(name)
}

// New is new Minimatch(pattern, options). It fails where the constructor throws: for a pattern over MAX_PATTERN_LENGTH.
func New(pattern string, options Options) (*Minimatch, error) {
	if jsstring.Length(pattern) > maxPatternLength {
		return nil, ErrPatternTooLong
	}
	m := &Minimatch{options: options, pattern: pattern, nocase: options.NoCase, matchers: map[string]*matcher{}}
	m.isWindows = options.Platform == "win32" || options.Platform == "" && runtime.GOOS == "windows"
	m.windowsNoMagicRoot = m.isWindows && m.nocase
	m.make()
	return m, nil
}

func (m *Minimatch) make() {
	pattern := m.pattern
	// empty patterns and comments match nothing.
	if strings.HasPrefix(pattern, "#") {
		m.comment = true
		return
	}
	if pattern == "" {
		m.empty = true
		return
	}
	// step 1: figure out negation, etc.
	if !m.options.NoNegate {
		negations := len(pattern) - len(strings.TrimLeft(pattern, "!"))
		m.negate = negations%2 == 1
		m.pattern = pattern[negations:]
	}
	// step 2: expand braces
	var globSet []string
	seen := map[string]bool{}
	for _, expanded := range braceExpand(m.pattern) {
		if !seen[expanded] {
			seen[expanded] = true
			globSet = append(globSet, expanded)
		}
	}
	// step 3: turn each one into a series of path-portion matchers, after level-one preprocessing.
	m.globParts = make([][]string, len(globSet))
	for i, s := range globSet {
		m.globParts[i] = m.levelOneOptimize(m.slashSplit(s))
	}
	m.set = make([][]portion, len(m.globParts))
	for i, s := range m.globParts {
		m.set[i] = m.parseRow(s)
	}
	// do not treat the ? in UNC paths as magic
	if m.isWindows {
		for i, p := range m.set {
			parts := m.globParts[i]
			if len(p) > 3 && p[0].isString() && p[0].s == "" && p[1].isString() && p[1].s == "" && at(parts, 2) == "?" && (isDriveRow(p[3]) || isLongUNC(at(parts, 3))) {
				if isLongUNC(parts[3]) {
					p[3] = portion{s: "UNC"}
				}
				p[2] = portion{s: "?"}
			}
		}
	}
	// with noext, `@(...)` would be literal
	if !m.options.NoExt {
		m.mergeRows()
	}
}

// at is parts[i], or "" past the end, where JavaScript has undefined; no portion is both "" and absent in a place this is asked.
func at(parts []string, i int) string {
	if i < 0 || i >= len(parts) {
		return ""
	}
	return parts[i]
}

func isLongUNC(s string) bool { return longUNCRE.MatchString(s) }

func isDriveRow(p portion) bool { return p.isString() && driveRE.MatchString(p.s) }

func (m *Minimatch) parseRow(s []string) []portion {
	if m.isWindows && m.windowsNoMagicRoot {
		// check if it's a drive or unc path. The root of a long UNC path, //?/UNC/host/share, is 6 portions.
		n := 4
		if at(s, 2) == "?" && isLongUNC(at(s, 3)) {
			n = 6
		}
		isUNC := len(s) > 2 && s[0] == "" && s[1] == "" && (s[2] == "?" || !globMagicRE.MatchString(s[2]))
		if isUNC {
			for _, p := range s[3:min(n, len(s))] {
				if globMagicRE.MatchString(p) {
					isUNC = false
				}
			}
		}
		isDrive := driveStartRE.MatchString(at(s, 0))
		if isUNC {
			row := make([]portion, 0, len(s))
			for i, p := range s {
				if i < n {
					row = append(row, portion{s: p})
				} else {
					row = append(row, m.parse(p))
				}
			}
			return row
		} else if isDrive {
			row := []portion{{s: s[0]}}
			for _, p := range s[1:] {
				row = append(row, m.parse(p))
			}
			return row
		}
	}
	row := make([]portion, len(s))
	for i, p := range s {
		row[i] = m.parse(p)
	}
	return row
}

// braceExpand is the braceExpand function of minimatch, with brace-expansion's default maximum.
func braceExpand(pattern string) []string {
	// Thanks to Yeting Li <https://github.com/yetingli> for improving this regexp to avoid a ReDOS vulnerability.
	if !braceRE.MatchString(pattern) {
		return []string{pattern}
	}
	return braceExpansionExpand(pattern)
}

// slashSplit is #slashSplit: p.split(/\/+/), and for a Windows path that starts with // an extra ” for the one it loses.
func (m *Minimatch) slashSplit(p string) []string {
	if m.isWindows && uncRootRE.MatchString(p) {
		return append([]string{""}, slashSplitRE.Split(p, -1)...)
	}
	return slashSplitRE.Split(p, -1)
}

// rootIndex is #rootIndex: on Windows, the index of the last root portion of a row: 0 for `c:/...`, 3 for `//?/c:/...` and `//host/share/...`, and 5 for `//?/UNC/host/share/...`. Otherwise -1.
func (m *Minimatch) rootIndex(row []string) int {
	if !m.isWindows {
		return -1
	}
	if len(row) > 0 && driveRE.MatchString(row[0]) {
		return 0
	}
	if len(row) > 3 && row[0] == "" && row[1] == "" && row[2] == "?" && driveRE.MatchString(row[3]) {
		return 3
	}
	if h := uncHost(row); h != -1 {
		return h + 1
	}
	return -1
}

// uncHost is the index of the host in a row or split path that starts with a Windows UNC root: 2 for `//host/share`, and 4 for `//?/UNC/host/share`. Otherwise -1. `//?/c:` is not one.
func uncHost(p []string) int {
	if len(p) < 2 || p[0] != "" || p[1] != "" {
		return -1
	}
	h := 2
	if at(p, 2) == "?" {
		if len(p) > 3 && isLongUNC(p[3]) {
			h = 4
		} else {
			return -1
		}
	}
	if h+1 < len(p) && p[h] != "" && p[h+1] != "" {
		return h
	}
	return -1
}

// uncHostPortions is uncHost for a row of the set, where a host or share can be the globstar (which is no host).
func uncHostPortions(p []portion) int {
	if len(p) < 2 || !p[0].isEmpty() || !p[1].isEmpty() {
		return -1
	}
	h := 2
	if len(p) > 2 && p[2].isString() && p[2].s == "?" {
		if len(p) > 3 && p[3].isString() && isLongUNC(p[3].s) {
			h = 4
		} else {
			return -1
		}
	}
	if h+1 < len(p) && !p[h].globstar && !p[h+1].globstar && !p[h].isEmpty() && !p[h+1].isEmpty() {
		return h
	}
	return -1
}

func (m *Minimatch) levelOneOptimize(parts []string) []string {
	// .. can not remove the root of a Windows path
	root := m.rootIndex(parts) + 1
	var set []string
	for _, part := range parts {
		prev := ""
		if len(set) > 0 {
			prev = set[len(set)-1]
		}
		if part == "**" && prev == "**" && len(set) > 0 {
			continue
		}
		if part == ".." && len(set) > root && prev != "" && prev != ".." && prev != "." && prev != "**" {
			set = set[:len(set)-1]
			continue
		}
		set = append(set, part)
	}
	if len(set) == 0 {
		return []string{""}
	}
	return set
}

// classEnd is the index of the `]` that closes the class that starts at start, or -1 if it is not closed. Posix classes such as `[:alpha:]` are not skipped, so this can find a `]` before the real end. That only makes a class look less like a literal.
func classEnd(pattern []rune, start int) int {
	i := start + 1
	if i < len(pattern) && (pattern[i] == '!' || pattern[i] == '^') {
		i++
	}
	sawStart := false
	for ; i < len(pattern); i++ {
		c := pattern[i]
		if c == ']' && sawStart {
			return i
		}
		sawStart = true
		if c == '\\' {
			i++
		}
	}
	return -1
}

// literal is the string a path portion pattern matches if it has no magic, and whether that is so. It can report magic for a pattern that is in fact literal (eg, `[]`), but never the reverse, so a string result is always safe to compare directly.
func literal(pattern string, noext bool) (string, bool) {
	if !noext && pattern == "+()" || pattern == "@()" {
		return pattern, true
	}
	p := []rune(pattern)
	var lit []rune
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '\\':
			// a trailing \ is a literal \
			i++
			if i < len(p) {
				lit = append(lit, p[i])
			} else {
				lit = append(lit, c)
			}
		case c == '*' || c == '?':
			return "", false
		case c == '[':
			closing := classEnd(p, i)
			if closing == -1 {
				// not closed, so the [ is a literal
				lit = append(lit, c)
				continue
			}
			// a class of one char, such as `[*]` or `[\b]`, is a literal
			body := p[i+1 : closing]
			if len(body) > 0 && body[0] == '\\' {
				body = body[1:]
			}
			if len(body) != 1 || i+1 < len(p) && (p[i+1] == '!' || p[i+1] == '^') {
				return "", false
			}
			lit = append(lit, body...)
			i = closing
		case !noext && indexRune("+@!", c) >= 0 && i+1 < len(p) && p[i+1] == '(' && indexRuneFrom(p, ')', i+2) >= 0:
			return "", false
		default:
			lit = append(lit, c)
		}
	}
	return string(lit), true
}

func indexRuneFrom(p []rune, r rune, from int) int {
	for i := max(from, 0); i < len(p); i++ {
		if p[i] == r {
			return i
		}
	}
	return -1
}

// extSafe is true if the portion pattern has the same meaning as an alternative inside `@(...)`. It must not have a `|` or `)` that is not in a closed extglob (those are literal at the top level), an extglob that is not closed (the `)` of the `@(...)` would close it), or a trailing `\` (it would escape the next `|`). A pattern that is only an empty extglob, such as `?()`, is literal on its own, but not in `@(...)`. Posix classes such as `[:alpha:]` can hide a `]` from classEnd, so patterns with them are not merged.
func extSafe(pattern string) bool {
	if emptyExtRE.MatchString(pattern) || strings.Contains(pattern, "[:") {
		return false
	}
	p := []rune(pattern)
	depth := 0
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '\\':
			i++
			if i == len(p) {
				return false
			}
		case c == '[':
			if closing := classEnd(p, i); closing != -1 {
				i = closing
			}
		case indexRune("+@!?*", c) >= 0 && i+1 < len(p) && p[i+1] == '(':
			depth++
			i++
		case c == ')':
			if depth == 0 {
				return false
			}
			depth--
		case c == '|' && depth == 0:
			return false
		}
	}
	return depth == 0
}

// parse is Minimatch#parse: the globstar, a string for a portion without magic, or a matcher.
func (m *Minimatch) parse(pattern string) portion {
	// shortcuts
	if pattern == "**" {
		return portion{globstar: true}
	}
	if pattern == "" {
		return portion{}
	}
	lit, isLit := literal(pattern, m.options.NoExt)
	// in nocase mode, a literal with cased chars still needs a matcher.
	if isLit && (!m.nocase || jsstring.ToUpper(pattern) == jsstring.ToLower(pattern)) {
		return portion{s: lit}
	}
	mt := m.matchers[pattern]
	if mt == nil {
		mt = newMatcher(pattern, m.nocase, m.options.Dot, m.options.NoExt)
		m.matchers[pattern] = mt
	}
	return portion{m: mt}
}

// mergeRows merges rows that only differ in one portion, where each of them is a matcher, into one row that matches any of them with `@(a|b|...)`. Then a path is only walked once for all of them, for example with `**/*.{js,ts}`.
func (m *Minimatch) mergeRows() {
	for merged := true; merged; {
		merged = false
		length := 0
		for _, r := range m.set {
			length = max(length, len(r))
		}
		for k := 0; k < length; k++ {
			// the rows that are the same other than at portion k
			var groups [][]int
			index := map[string]int{}
			for i, row := range m.set {
				if k >= len(row) || row[k].m == nil || !extSafe(m.globParts[i][k]) {
					continue
				}
				parts := append([]string(nil), m.globParts[i]...)
				parts[k] = ""
				key := itoa(len(row)) + "/" + strings.Join(parts, "/")
				if g, ok := index[key]; ok {
					groups[g] = append(groups[g], i)
				} else {
					index[key] = len(groups)
					groups = append(groups, []int{i})
				}
			}
			drop := map[int]bool{}
			for _, g := range groups {
				if len(g) < 2 {
					continue
				}
				alts := make([]string, len(g))
				for j, i := range g {
					alts[j] = m.globParts[i][k]
				}
				glob := "@(" + strings.Join(alts, "|") + ")"
				m.globParts[g[0]][k] = glob
				m.set[g[0]][k] = m.parse(glob)
				for _, i := range g[1:] {
					drop[i] = true
				}
			}
			if len(drop) > 0 {
				merged = true
				var set [][]portion
				var parts [][]string
				for i := range m.set {
					if !drop[i] {
						set = append(set, m.set[i])
						parts = append(parts, m.globParts[i])
					}
				}
				m.set, m.globParts = set, parts
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// portionMatch is true if a portion that is not ** matches a path portion. An empty path portion only matches an empty pattern portion.
func portionMatch(p portion, f string) bool {
	if p.isString() {
		return f == p.s
	}
	return f != "" && p.m.test(f)
}

// Match is Minimatch#match(f) without partial matching or matchBase.
func (m *Minimatch) Match(f string) bool {
	// short-circuit in the case of busted things. comments, etc.
	if m.comment {
		return false
	}
	if m.empty {
		return f == ""
	}
	// treat the test path as a set of pathparts
	if m.isWindows {
		f = strings.ReplaceAll(f, `\`, "/")
	}
	ff := m.slashSplit(f)
	// just ONE of the pattern sets in this.set needs to match in order for it to be valid. If negating, then just one match means that we have failed. Either way, return on the first hit.
	for _, pattern := range m.set {
		if m.matchRow(ff, pattern) {
			return !m.negate
		}
	}
	// didn't get any hits. this is success if it's a negative pattern, failure otherwise.
	return m.negate
}

// matchRow matches the parts of a path against one row of the set.
func (m *Minimatch) matchRow(file []string, pattern []portion) bool {
	fileStart, patternStart := 0, 0
	// UNC paths like //?/X:/... can match X:/... and vice versa, and //?/UNC/host/share/... can match //host/share/... and vice versa. Drive letters in absolute drive or unc paths, and the host and share of UNC paths, are always compared case-insensitively.
	fh := -1
	if m.isWindows {
		fh = uncHost(file)
	}
	ph := -1
	if fh != -1 {
		ph = uncHostPortions(pattern)
	}
	if ph != -1 {
		host, share := pattern[ph], pattern[ph+1]
		if !host.isString() || !share.isString() {
			// a host or share with magic is matched as usual
			fileStart, patternStart = fh, ph
		} else if jsstring.ToLower(file[fh]) == jsstring.ToLower(host.s) && jsstring.ToLower(file[fh+1]) == jsstring.ToLower(share.s) {
			fileStart, patternStart = fh+2, ph+2
		}
	} else if m.isWindows {
		fileDrive := len(file) > 0 && driveRE.MatchString(file[0])
		fileUNC := !fileDrive && len(file) > 3 && file[0] == "" && file[1] == "" && file[2] == "?" && driveRE.MatchString(file[3])
		patternDrive := len(pattern) > 0 && isDriveRow(pattern[0])
		patternUNC := !patternDrive && len(pattern) > 3 && pattern[0].isString() && pattern[0].s == "" && pattern[1].isString() && pattern[1].s == "" && pattern[2].isString() && pattern[2].s == "?" && isDriveRow(pattern[3])
		fdi, pdi := -1, -1
		if fileUNC {
			fdi = 3
		} else if fileDrive {
			fdi = 0
		}
		if patternUNC {
			pdi = 3
		} else if patternDrive {
			pdi = 0
		}
		// the drives match, so start matching after them
		if fdi >= 0 && pdi >= 0 && jsstring.ToLower(file[fdi]) == jsstring.ToLower(pattern[pdi].s) {
			patternStart = pdi + 1
			fileStart = fdi + 1
		}
	}
	for _, p := range pattern {
		if p.globstar {
			return m.matchGlobstar(file, pattern, fileStart, patternStart)
		}
	}
	return m.matchOne(file, pattern, fileStart, patternStart, len(file))
}

func isDotPortion(f string, dot bool) bool {
	return f == "." || f == ".." || (!dot && strings.HasPrefix(f, "."))
}

type bodySegment struct {
	parts []portion
	need  int
}

func (m *Minimatch) matchGlobstar(file []string, pattern []portion, fileIndex, patternIndex int) bool {
	firstgs, lastgs := -1, -1
	for i := patternIndex; i < len(pattern); i++ {
		if pattern[i].globstar {
			firstgs = i
			break
		}
	}
	for i, p := range slices.Backward(pattern) {
		if p.globstar {
			lastgs = i
			break
		}
	}
	if firstgs == -1 {
		return m.matchOne(file, pattern, fileIndex, patternIndex, len(file))
	}
	head := pattern[patternIndex:firstgs]
	var body []portion
	if lastgs > firstgs {
		body = pattern[firstgs+1 : lastgs]
	}
	tail := pattern[lastgs+1:]
	// check the head, from the current file/pattern index.
	if len(head) > 0 {
		if !m.matchOne(file, head, fileIndex, 0, fileIndex+len(head)) {
			return false
		}
		fileIndex += len(head)
	}
	// if the last portion is not empty, it MUST match the end check the tail
	fileTailMatch := 0
	if len(tail) > 0 {
		// if head + tail > file, then we cannot possibly match
		if len(tail)+fileIndex > len(file) {
			return false
		}
		tailStart := len(file) - len(tail)
		if m.matchOne(file, tail, tailStart, 0, len(file)) {
			fileTailMatch = len(tail)
		} else {
			// affordance for stuff like a/**/* matching a/b/ if the last file portion is '', and there's more to the pattern then try without the '' bit.
			if file[len(file)-1] != "" || fileIndex+len(tail) == len(file) {
				return false
			}
			tailStart--
			if !m.matchOne(file, tail, tailStart, 0, len(file)) {
				return false
			}
			fileTailMatch = len(tail) + 1
		}
	}
	// the middle is zero or more portions wrapped in **, possibly containing more ** sections. if it's empty, it means a/**/b, just verify we have no bad dots. if there's no tail, so it ends on /**, then we must have *something* after the head, or it's not a match
	if len(body) == 0 {
		sawSome := fileTailMatch != 0
		for i := fileIndex; i < len(file)-fileTailMatch; i++ {
			sawSome = true
			if isDotPortion(file[i], m.options.Dot) {
				return false
			}
		}
		return sawSome
	}
	// split the body up into sections, and note the number of portions that must come after the start of each one
	segments := []*bodySegment{{}}
	current := segments[0]
	nonGsParts := 0
	sums := []int{0}
	for _, b := range body {
		if b.globstar {
			sums = append(sums, nonGsParts)
			current = &bodySegment{}
			segments = append(segments, current)
		} else {
			current.parts = append(current.parts, b)
			nonGsParts++
		}
	}
	// the portions in this section and the ones after it
	for i, b := range segments {
		b.need = nonGsParts - sums[i]
	}
	r := m.matchBodySections(file, segments, len(file)-fileTailMatch, fileIndex, 0, 0, fileTailMatch != 0, map[int]bool{})
	return r == resultTrue
}

type tri uint8

const (
	resultFalse tri = iota
	resultTrue
	resultNull
)

// matchBodySections is #matchGlobStarBodySections: false is "nope, not matching", null is "not matching, cannot keep trying". tried holds the places a walk already tried, as bodyIndex * (len(file) + 1) + fileIndex: a walk from a place does the same no matter how it got there, and it only comes back if it found no match.
func (m *Minimatch) matchBodySections(file []string, segments []*bodySegment, fileLength, fileIndex, bodyIndex, globStarDepth int, sawTail bool, tried map[int]bool) tri {
	if bodyIndex >= len(segments) {
		// just make sure that there's no bad dots before the tail
		for i := fileIndex; i < fileLength; i++ {
			sawTail = true
			if isDotPortion(file[i], m.options.Dot) {
				return resultFalse
			}
		}
		if sawTail {
			return resultTrue
		}
		return resultFalse
	}
	// have a non-globstar body section to test, up to the last index where it can start
	bs := segments[bodyIndex]
	after := fileLength - bs.need
	for fileIndex <= after {
		key := bodyIndex*(len(file)+1) + fileIndex
		if tried[key] {
			return resultFalse
		}
		tried[key] = true
		matched := m.matchOne(file, bs.parts, fileIndex, 0, fileIndex+len(bs.parts))
		// if limit exceeded, no match. intentional false negative, acceptable break in correctness for security.
		if matched && globStarDepth < maxGlobstarRecursion {
			// match! see if the rest match. if so, we're done!
			if sub := m.matchBodySections(file, segments, fileLength, fileIndex+len(bs.parts), bodyIndex+1, globStarDepth+1, sawTail, tried); sub != resultFalse {
				return sub
			}
		}
		// the ** does not walk past the last place the body can start
		if fileIndex == after {
			break
		}
		if isDotPortion(file[fileIndex], m.options.Dot) {
			return resultFalse
		}
		fileIndex++
	}
	// walked off. no point continuing
	return resultNull
}

// matchOne matches the portions of pattern from patternIndex with those of file from fileIndex, up to fileEnd.
func (m *Minimatch) matchOne(file []string, pattern []portion, fileIndex, patternIndex, fileEnd int) bool {
	fi, pi := fileIndex, patternIndex
	fl, pl := min(fileEnd, len(file)), len(pattern)
	for ; fi < fl && pi < pl; fi, pi = fi+1, pi+1 {
		p := pattern[pi]
		// should be impossible.
		if p.globstar {
			return false
		}
		// non-magic patterns just have to match exactly; patterns with magic have been turned into matchers.
		if !portionMatch(p, file[fi]) {
			return false
		}
	}
	// Note: ending in / means that we'll get a final "" at the end of the pattern. This can only match a corresponding "" at the end of the file. If the file ends in /, then it can only match a pattern that ends in /, unless the pattern just doesn't have any more for it. But, a/b/ should *not* match "a/b/*", even though * can match "". However, a/b/ should still satisfy a/*
	switch {
	case fi == fl && pi == pl:
		// ran out of pattern and filename at the same time. an exact hit!
		return true
	case fi == fl:
		// ran out of file, but still had pattern left. this is ok only if we're doing the match as part of a glob fs traversal (partial)
		return false
	}
	// ran out of pattern, still have file left. this is only acceptable if we're on the very last empty segment of a file with a trailing slash. a/* should match a/b/
	return fi == fl-1 && file[fi] == ""
}
