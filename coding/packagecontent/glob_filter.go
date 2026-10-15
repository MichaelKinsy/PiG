package packagecontent

import (
	"runtime"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// globIsWindows is minimatch's isWindows: its platform defaults to process.platform.
var globIsWindows = runtime.GOOS == "windows"

// globMaxGlobstarRecursion is minimatch's default maxGlobstarRecursion.
// upstream: node_modules/minimatch/dist/esm/index.js:maxGlobstarRecursion
const globMaxGlobstarRecursion = 200

// minimatchFilter is a Minimatch with minimatch's default options, as Pi's matchesAnyPattern builds one for each minimatch(path, pattern) call (package-manager.ts:661-685): case is not ignored, "#" starts a comment, leading "!"s negate, and the parts go through the first optimization level only.
type minimatchFilter struct {
	set     [][]packageGlobSegment
	negate  bool
	comment bool
	empty   bool
}

// newMinimatchFilter is new Minimatch(pattern). Its braces expand as Pi's brace-expansion 5.0.12 expands them (npmBraceExpand). It fails where the constructor throws: for a pattern over MAX_PATTERN_LENGTH, or a segment whose RegExp V8 rejects. A comment is not compiled.
func newMinimatchFilter(pattern string) (*minimatchFilter, error) {
	if jsstring.Length(pattern) > globMaxPatternLength {
		return nil, errGlobPatternTooLong
	}
	m := &minimatchFilter{}
	if strings.HasPrefix(pattern, "#") {
		m.comment = true
		return m, nil
	}
	if pattern == "" {
		m.empty = true
		return m, nil
	}
	negations := len(pattern) - len(strings.TrimLeft(pattern, "!"))
	m.negate = negations%2 == 1
	pattern = pattern[negations:]
	var globParts [][]string
	seen := make(map[string]bool)
	for _, p := range npmBraceExpand(pattern) {
		if !seen[p] {
			seen[p] = true
			globParts = append(globParts, levelOneOptimize(slashSplit(p)))
		}
	}
	for _, parts := range globParts {
		segments := make([]packageGlobSegment, 0, len(parts))
		for _, part := range parts {
			segment, err := newPackageGlobSegment(part, false)
			if err != nil {
				return nil, err
			}
			segments = append(segments, segment)
		}
		// On Windows the "?" of a "//?/<drive>:" UNC root is not magic.
		if globIsWindows && len(parts) > 3 && parts[0] == "" && parts[1] == "" && parts[2] == "?" && segments[3].isLiteral && isDriveName(segments[3].literal) {
			segments[2] = packageGlobSegment{isLiteral: true, literal: "?"}
		}
		m.set = append(m.set, segments)
	}
	return m, nil
}

// filterPatterns are the patterns of one matchesAnyPattern call, each in Pi's toPosixPath(normalizePath(pattern)) form. Pi builds a Minimatch for each minimatch(path, pattern) call, so a pattern is normalized and compiled when a path first reaches it: one after a pattern that already matched is never compiled, and one that cannot be normalized (an invalid file URL) or compiled fails only when a path reaches it.
type filterPatterns struct {
	patterns []string
	invalid  []error
	compiled []*minimatchFilter
}

func newFilterPatterns(patterns []string) *filterPatterns {
	normalized := make([]string, len(patterns))
	invalid := make([]error, len(patterns))
	for i, pattern := range patterns {
		// upstream: package-manager.ts:matchesAnyPattern (toPosixPath(normalizePath(pattern)), Pi #10765)
		normalized[i], invalid[i] = normalizePatternPath(pattern)
	}
	return &filterPatterns{patterns: normalized, invalid: invalid, compiled: make([]*minimatchFilter, len(patterns))}
}

func (f *filterPatterns) at(i int) (*minimatchFilter, error) {
	if f.invalid[i] != nil {
		return nil, f.invalid[i]
	}
	if f.compiled[i] == nil {
		m, err := newMinimatchFilter(f.patterns[i])
		if err != nil {
			return nil, err
		}
		f.compiled[i] = m
	}
	return f.compiled[i], nil
}

// levelOneOptimize is minimatch's levelOneOptimize: adjacent globstars become one, and ".." removes the part before it unless that part is "..", "." or a globstar. No parts become one empty part.
func levelOneOptimize(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		var prev string
		if len(out) > 0 {
			prev = out[len(out)-1]
		}
		if part == "**" && prev == "**" && len(out) > 0 {
			continue
		}
		if part == ".." && len(out) > 0 && prev != "" && prev != ".." && prev != "." && prev != "**" {
			out = out[:len(out)-1]
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// isDriveName is /^[a-z]:$/i.
func isDriveName(s string) bool {
	return len(s) == 2 && s[1] == ':' && (s[0] >= 'a' && s[0] <= 'z' || s[0] >= 'A' && s[0] <= 'Z')
}

// match is Minimatch.match(f) without partial matching or matchBase.
func (m *minimatchFilter) match(f string) bool {
	switch {
	case m.comment:
		return false
	case m.empty:
		return f == ""
	}
	if globIsWindows {
		f = strings.ReplaceAll(f, `\`, "/")
	}
	file := slashSplit(f)
	for _, pattern := range m.set {
		if matchOne(file, pattern) {
			return !m.negate
		}
	}
	return m.negate
}

// matchOne is Minimatch.matchOne. On Windows a drive, or the drive of a "//?/" UNC path, in the file and the pattern starts the match when the two drives are the same ignoring case.
func matchOne(file []string, pattern []packageGlobSegment) bool {
	fileStart, patternStart := 0, 0
	if globIsWindows {
		at := func(i int) string {
			s, _ := partAt(file, i)
			return s
		}
		literal := func(i int) (string, bool) {
			if i >= len(pattern) || !pattern[i].isLiteral {
				return "", false
			}
			return pattern[i].literal, true
		}
		fileDrive := len(file) > 0 && isDriveName(file[0])
		fileUNC := !fileDrive && len(file) > 3 && file[0] == "" && file[1] == "" && file[2] == "?" && isDriveName(file[3])
		p0, ok0 := literal(0)
		patternDrive := ok0 && isDriveName(p0)
		p1, ok1 := literal(1)
		p2, ok2 := literal(2)
		p3, ok3 := literal(3)
		patternUNC := !patternDrive && ok0 && p0 == "" && ok1 && p1 == "" && ok2 && p2 == "?" && ok3 && isDriveName(p3)
		fdi, pdi := -1, -1
		switch {
		case fileUNC:
			fdi = 3
		case fileDrive:
			fdi = 0
		}
		switch {
		case patternUNC:
			pdi = 3
		case patternDrive:
			pdi = 0
		}
		if fdi >= 0 && pdi >= 0 {
			if fd, pd := at(fdi), pattern[pdi].literal; strings.EqualFold(fd, pd) {
				// minimatch writes the file's drive into the pattern; a copy keeps the compiled set unchanged.
				pattern = append([]packageGlobSegment(nil), pattern...)
				pattern[pdi] = packageGlobSegment{isLiteral: true, literal: fd}
				fileStart, patternStart = fdi, pdi
			}
		}
	}
	for _, segment := range pattern {
		if segment.globstar {
			return matchGlobstar(file, pattern, fileStart, patternStart)
		}
	}
	return matchSegments(file, pattern, fileStart, patternStart)
}

// matchSegments is Minimatch.#matchOne without partial matching: each segment matches its file part, and a pattern that runs out matches a file whose only remaining part is an empty one.
func matchSegments(file []string, pattern []packageGlobSegment, fi, pi int) bool {
	for ; fi < len(file) && pi < len(pattern); fi, pi = fi+1, pi+1 {
		if pattern[pi].globstar || !pattern[pi].matches(file[fi]) {
			return false
		}
	}
	switch {
	case fi == len(file) && pi == len(pattern):
		return true
	case fi == len(file):
		return false
	}
	return fi == len(file)-1 && file[fi] == ""
}

// isDotPart reports a part that a globstar does not cross without the dot option.
func isDotPart(f string) bool { return f == "." || f == ".." || strings.HasPrefix(f, ".") }

// matchGlobstar is Minimatch.#matchGlobstar without partial matching: the head before the first globstar matches at the start, the tail after the last one at the end, and the sections between globstars in order in the middle, none of it crossing a dot part.
func matchGlobstar(file []string, pattern []packageGlobSegment, fileIndex, patternIndex int) bool {
	firstGS, lastGS := -1, -1
	for i := patternIndex; i < len(pattern); i++ {
		if pattern[i].globstar {
			if firstGS < 0 {
				firstGS = i
			}
		}
	}
	for i, p := range slices.Backward(pattern) {
		if p.globstar {
			lastGS = i
			break
		}
	}
	head := pattern[patternIndex:firstGS]
	body := pattern[firstGS+1 : max(lastGS, firstGS+1)]
	tail := pattern[lastGS+1:]
	if len(head) > 0 {
		fileHead := file[min(fileIndex, len(file)):min(fileIndex+len(head), len(file))]
		if !matchSegments(fileHead, head, 0, 0) {
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
		if matchSegments(file, tail, tailStart, 0) {
			fileTailMatch = len(tail)
		} else {
			// minimatch lets "a/**/*" match "a/b/" by trying the tail once more before a trailing empty part.
			if file[len(file)-1] != "" || fileIndex+len(tail) == len(file) {
				return false
			}
			tailStart--
			if !matchSegments(file, tail, tailStart, 0) {
				return false
			}
			fileTailMatch = len(tail) + 1
		}
	}
	if len(body) == 0 {
		sawSome := fileTailMatch > 0
		for i := fileIndex; i < len(file)-fileTailMatch; i++ {
			sawSome = true
			if isDotPart(file[i]) {
				return false
			}
		}
		return sawSome
	}
	type section struct {
		segments []packageGlobSegment
		after    int
	}
	sections := []section{{}}
	nonGlobstar := 0
	sums := []int{0}
	for _, segment := range body {
		if segment.globstar {
			sums = append(sums, nonGlobstar)
			sections = append(sections, section{})
			continue
		}
		sections[len(sections)-1].segments = append(sections[len(sections)-1].segments, segment)
		nonGlobstar++
	}
	fileLength := len(file) - fileTailMatch
	for i := range sections {
		sections[i].after = fileLength - (sums[len(sections)-1-i] + len(sections[i].segments))
	}
	var walk func(fileIndex, sectionIndex, depth int, sawTail bool) int
	// walk is #matchGlobStarBodySections: 1 for a match, 0 for false and -1 for null (no match, and no use trying further positions).
	walk = func(fileIndex, sectionIndex, depth int, sawTail bool) int {
		if sectionIndex == len(sections) {
			for i := fileIndex; i < len(file); i++ {
				sawTail = true
				if isDotPart(file[i]) {
					return 0
				}
			}
			if sawTail {
				return 1
			}
			return 0
		}
		s := sections[sectionIndex]
		// levelOneOptimize joins adjacent globstars, so a section is never empty and after stays below len(file).
		for ; fileIndex <= s.after && fileIndex < len(file); fileIndex++ {
			if matchSegments(file[:min(fileIndex+len(s.segments), len(file))], s.segments, fileIndex, 0) && depth < globMaxGlobstarRecursion {
				if sub := walk(fileIndex+len(s.segments), sectionIndex+1, depth+1, sawTail); sub != 0 {
					return sub
				}
			}
			if isDotPart(file[fileIndex]) {
				return 0
			}
		}
		return -1
	}
	return walk(fileIndex, 0, 0, fileTailMatch > 0) == 1
}
