// Package minimatch ports minimatch 10.2.6 (the glob matcher Pi uses for model scopes and package resource filters): brace expansion, `!`
// negation, `**`, character classes, extglobs and dot handling. Matching of one path portion is a regular expression, as in minimatch.
package minimatch

// Ports minimatch 10.2.6 index.js (Minimatch.make, parse, matchOne, match) with its default options.

import (
	"slices"
	"strings"
	"time"

	"github.com/dlclark/regexp2"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// Options are the minimatch options that change a match. Every other option has its default.
type Options struct {
	// NoCase makes the match case-insensitive.
	NoCase bool
	// Dot lets wildcards match a leading dot.
	Dot bool
}

const (
	maxPatternLength    = 1024 * 64
	maxGlobstarRecurse  = 200
	regexMatchTimeLimit = 2 * time.Second
)

type regexSource struct{ re *regexp2.Regexp }

func compileSource(src string, nocase, uflag bool) *regexSource {
	options := regexp2.None
	if nocase {
		options = regexp2.IgnoreCase
	}
	re, err := regexp2.Compile("^"+src+`\z`, options)
	if err != nil {
		// A class that cannot match anything: minimatch's "$." source never matches.
		re = regexp2.MustCompile(`(?!)`, regexp2.None)
	}
	re.MatchTimeout = regexMatchTimeLimit
	return &regexSource{re: re}
}

func (r *regexSource) test(s string) bool {
	ok, err := r.re.MatchString(s)
	return err == nil && ok
}

// part is one compiled path portion of a pattern: text, a regular expression, or the globstar.
type part struct {
	globstar bool
	literal  string
	re       *regexSource
	// fast is the specialized test minimatch installs over the regular expression for the most common shapes; it replaces re.test.
	fast func(string) bool
}

func (p part) isLiteral() bool { return !p.globstar && p.re == nil }

type matcher struct {
	options Options
	set     [][]part
	negate  bool
	comment bool
	empty   bool
}

var (
	braceRE      = lazyregexp.New(`\{[^{\n\r\x{2028}\x{2029}]*\}`)
	slashSplitRE = lazyregexp.New(`/+`)
)

// Match reports whether name matches the glob pattern, as minimatch(name, pattern, options).
func Match(name, pattern string, options Options) bool {
	if len(toUTF16(pattern)) > maxPatternLength {
		return false
	}
	if strings.HasPrefix(pattern, "#") {
		return false
	}
	return newMatcher(pattern, options).match(name)
}

func newMatcher(pattern string, options Options) *matcher {
	m := &matcher{options: options}
	if pattern == "" {
		m.empty = true
		return m
	}
	for len(pattern) > 0 && pattern[0] == '!' {
		m.negate = !m.negate
		pattern = pattern[1:]
	}
	var globSet []string
	seen := map[string]bool{}
	for _, expanded := range braceExpand(pattern) {
		if !seen[expanded] {
			seen[expanded] = true
			globSet = append(globSet, expanded)
		}
	}
	for _, glob := range globSet {
		parts := levelOneOptimize(slashSplit(glob))
		compiled := make([]part, len(parts))
		for i, p := range parts {
			compiled[i] = m.parse(p)
		}
		m.set = append(m.set, compiled)
	}
	return m
}

func braceExpand(pattern string) []string {
	if !braceRE.MatchString(pattern) {
		return []string{pattern}
	}
	return braceExpansionExpand(pattern)
}

// slashSplit is p.split(/\/+/).
func slashSplit(p string) []string { return slashSplitRE.Split(p, -1) }

func levelOneOptimize(parts []string) []string {
	var set []string
	for _, p := range parts {
		prev := ""
		hasPrev := len(set) > 0
		if hasPrev {
			prev = set[len(set)-1]
		}
		if p == "**" && hasPrev && prev == "**" {
			continue
		}
		if p == ".." && hasPrev && prev != "" && prev != ".." && prev != "." && prev != "**" {
			set = set[:len(set)-1]
			continue
		}
		set = append(set, p)
	}
	if len(set) == 0 {
		return []string{""}
	}
	return set
}

func (m *matcher) parse(pattern string) part {
	switch pattern {
	case "**":
		return part{globstar: true}
	case "":
		return part{}
	}
	compiled := fromGlob(pattern, astOptions{dot: m.options.Dot, nocase: m.options.NoCase}).toMMPattern()
	if compiled.re != nil {
		return part{re: compiled.re, fast: m.fastTest(pattern)}
	}
	return part{literal: compiled.literal}
}

func (m *matcher) match(f string) bool {
	if m.comment {
		return false
	}
	if m.empty {
		return f == ""
	}
	ff := slashSplit(f)
	for _, pattern := range m.set {
		if m.matchOne(ff, pattern) {
			return !m.negate
		}
	}
	return m.negate
}

func (m *matcher) matchOne(file []string, pattern []part) bool {
	for _, p := range pattern {
		if p.globstar {
			return m.matchGlobstar(file, pattern, 0, 0)
		}
	}
	return m.matchPlain(file, pattern, 0, 0)
}

func (m *matcher) leadingDot(f string) bool {
	return f == "." || f == ".." || (!m.options.Dot && strings.HasPrefix(f, "."))
}

func (m *matcher) matchGlobstar(file []string, pattern []part, fileIndex, patternIndex int) bool {
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
	// Array.prototype.slice yields an empty array when the start is past the end.
	sliceParts := func(from, to int) []part {
		if from >= to {
			return nil
		}
		return pattern[from:to]
	}
	head, body, tail := sliceParts(patternIndex, firstgs), sliceParts(firstgs+1, lastgs), sliceParts(lastgs+1, len(pattern))
	if len(head) > 0 {
		if fileIndex+len(head) > len(file) {
			return false
		}
		if !m.matchPlain(file[fileIndex:fileIndex+len(head)], head, 0, 0) {
			return false
		}
		fileIndex += len(head)
	}
	fileTailMatch := 0
	if len(tail) > 0 {
		if len(tail)+fileIndex > len(file) {
			return false
		}
		tailStart := len(file) - len(tail)
		if m.matchPlain(file, tail, tailStart, 0) {
			fileTailMatch = len(tail)
		} else {
			if file[len(file)-1] != "" || fileIndex+len(tail) == len(file) {
				return false
			}
			tailStart--
			if !m.matchPlain(file, tail, tailStart, 0) {
				return false
			}
			fileTailMatch = len(tail) + 1
		}
	}
	if len(body) == 0 {
		sawSome := fileTailMatch != 0
		for i := fileIndex; i < len(file)-fileTailMatch; i++ {
			sawSome = true
			if m.leadingDot(file[i]) {
				return false
			}
		}
		return sawSome
	}
	type segment struct {
		parts []part
		after int
	}
	segments := []*segment{{}}
	current := segments[0]
	nonGsParts := 0
	sums := []int{0}
	for _, b := range body {
		if b.globstar {
			sums = append(sums, nonGsParts)
			current = &segment{}
			segments = append(segments, current)
		} else {
			current.parts = append(current.parts, b)
			nonGsParts++
		}
	}
	i := len(segments) - 1
	fileLength := len(file) - fileTailMatch
	for _, b := range segments {
		b.after = fileLength - (sums[i] + len(b.parts))
		i--
	}
	// #matchGlobStarBodySections answers true, false or null; a caller continues with the next position only on false.
	const (
		resultFalse = iota
		resultTrue
		resultNull
	)
	asResult := func(b bool) int {
		if b {
			return resultTrue
		}
		return resultFalse
	}
	var sections func(fileIndex, bodyIndex, depth int, sawTail bool) int
	sections = func(fileIndex, bodyIndex, depth int, sawTail bool) int {
		if bodyIndex >= len(segments) {
			for i := fileIndex; i < len(file); i++ {
				sawTail = true
				if m.leadingDot(file[i]) {
					return resultFalse
				}
			}
			return asResult(sawTail)
		}
		bs := segments[bodyIndex]
		for fileIndex <= bs.after {
			end := fileIndex + len(bs.parts)
			if end <= len(file) && m.matchPlain(file[:end], bs.parts, fileIndex, 0) && depth < maxGlobstarRecurse {
				if sub := sections(end, bodyIndex+1, depth+1, sawTail); sub != resultFalse {
					return sub
				}
			}
			if fileIndex < len(file) && m.leadingDot(file[fileIndex]) {
				return resultFalse
			}
			fileIndex++
		}
		return resultNull
	}
	return sections(fileIndex, 0, 0, fileTailMatch != 0) == resultTrue
}

// matchPlain is #matchOne: it matches pattern from patternIndex against file from fileIndex, one portion each.
func (m *matcher) matchPlain(file []string, pattern []part, fileIndex, patternIndex int) bool {
	fi, pi := fileIndex, patternIndex
	fl, pl := len(file), len(pattern)
	for ; fi < fl && pi < pl; fi, pi = fi+1, pi+1 {
		p, f := pattern[pi], file[fi]
		if p.globstar {
			return false
		}
		var hit bool
		switch {
		case p.isLiteral():
			hit = f == p.literal
		case p.fast != nil:
			hit = p.fast(f)
		default:
			hit = p.re.test(f)
		}
		if !hit {
			return false
		}
	}
	switch {
	case fi == fl && pi == pl:
		return true
	case fi == fl:
		return false
	case pi == pl:
		return fi == fl-1 && file[fi] == ""
	}
	return false
}

var (
	starRE       = lazyregexp.New(`^\*+$`)
	starDotExtRE = lazyregexp.New(`^\*+([^+@!?*\[(]*)$`)
	qmarksRE     = lazyregexp.New(`^\?+([^+@!?*\[(]*)?$`)
	starDotStar  = lazyregexp.New(`^\*+\.\*+$`)
	dotStarRE    = lazyregexp.New(`^\.\*+$`)
)

func isDotOrDotDot(f string) bool { return f == "." || f == ".." }

// fastTest is minimatch's parse() shortcut for the most common glob shapes (`*`, `*.ext`, `???`, `*.*`, `.*`). It is observable: minimatch
// answers with these functions, not with the regular expression, and they lower-case with toLowerCase.
func (m *matcher) fastTest(pattern string) func(string) bool {
	dot, nocase := m.options.Dot, m.options.NoCase
	switch {
	case starRE.MatchString(pattern):
		if dot {
			return func(f string) bool { return f != "" && !isDotOrDotDot(f) }
		}
		return func(f string) bool { return f != "" && !strings.HasPrefix(f, ".") }
	case starDotExtRE.MatchString(pattern):
		ext := starDotExtRE.FindStringSubmatch(pattern)[1]
		if nocase {
			ext = jsstring.ToLower(ext)
			if dot {
				return func(f string) bool { return strings.HasSuffix(jsstring.ToLower(f), ext) }
			}
			return func(f string) bool { return !strings.HasPrefix(f, ".") && strings.HasSuffix(jsstring.ToLower(f), ext) }
		}
		if dot {
			return func(f string) bool { return strings.HasSuffix(f, ext) }
		}
		return func(f string) bool { return !strings.HasPrefix(f, ".") && strings.HasSuffix(f, ext) }
	case qmarksRE.MatchString(pattern):
		groups := qmarksRE.FindStringSubmatch(pattern)
		length := len(toUTF16(groups[0]))
		ext := groups[1]
		noext := func(f string) bool { return len(toUTF16(f)) == length && !strings.HasPrefix(f, ".") }
		if dot {
			noext = func(f string) bool { return len(toUTF16(f)) == length && !isDotOrDotDot(f) }
		}
		if ext == "" {
			return noext
		}
		if nocase {
			ext = jsstring.ToLower(ext)
			return func(f string) bool { return noext(f) && strings.HasSuffix(jsstring.ToLower(f), ext) }
		}
		return func(f string) bool { return noext(f) && strings.HasSuffix(f, ext) }
	case starDotStar.MatchString(pattern):
		if dot {
			return func(f string) bool { return !isDotOrDotDot(f) && strings.Contains(f, ".") }
		}
		return func(f string) bool { return !strings.HasPrefix(f, ".") && strings.Contains(f, ".") }
	case dotStarRE.MatchString(pattern):
		return func(f string) bool { return !isDotOrDotDot(f) && strings.HasPrefix(f, ".") }
	}
	return nil
}
