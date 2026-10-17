package packagecontent

import (
	"cmp"
	"errors"
	"runtime"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// globIgnoresCase is the nocase option of Node's globSync: createMatcher (lib/internal/fs/glob.js) passes minimatch nocase on Windows and macOS, with nocaseMagicOnly, so only a segment with magic ignores case.
var globIgnoresCase = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// globMaxPatternLength is minimatch's MAX_PATTERN_LENGTH, in UTF-16 code units.
// upstream: node_modules/minimatch/dist/esm/assert-valid-pattern.js:MAX_PATTERN_LENGTH
const globMaxPatternLength = 1024 * 64

// errGlobPatternTooLong is the TypeError of minimatch's assertValidPattern.
var errGlobPatternTooLong = errors.New("pattern is too long")

// globPatternParts is minimatch's globParts for a pattern of Node's globSync (Minimatch constructor and make, with windowsPathsNoEscape and optimizationLevel 2): backslashes become slashes, braces expand into distinct patterns, runs of slashes split segments, and preprocessing gives the equivalent patterns that the walk uses. An empty pattern has none. It fails where minimatch throws: for a pattern over MAX_PATTERN_LENGTH, and where brace expansion does.
func globPatternParts(pattern string) ([][]string, error) {
	if jsstring.Length(pattern) > globMaxPatternLength {
		return nil, errGlobPatternTooLong
	}
	pattern = strings.ReplaceAll(pattern, `\`, "/")
	if pattern == "" {
		return nil, nil
	}
	expanded, err := braceExpand(pattern)
	if err != nil {
		return nil, err
	}
	var parts [][]string
	seen := make(map[string]bool)
	for _, pattern := range expanded {
		if !seen[pattern] {
			seen[pattern] = true
			parts = append(parts, slashSplit(pattern))
		}
	}
	return secondPhasePreProcess(firstPhasePreProcess(parts)), nil
}

// globMagic is minimatch's globMagic.
var globMagic = lazyregexp.New(`[?*]|[+@!]\([^\n\r\x{2028}\x{2029}]*?\)|\[|\]`)

var globDriveRoot = lazyregexp.New(`^[a-zA-Z]:`)

// globPatternSegments is minimatch's set for the parts of a pattern: each segment parsed, except, on Windows, where nocase sets windowsNoMagicRoot, the root of a UNC path (its first four segments) or the drive of a drive path, which stay strings.
func globPatternSegments(parts []string, nocase bool) ([]packageGlobSegment, error) {
	raw := 0
	if runtime.GOOS == "windows" && nocase {
		at := func(i int) string {
			part, _ := partAt(parts, i)
			return part
		}
		switch {
		case len(parts) >= 2 && parts[0] == "" && parts[1] == "" && (at(2) == "?" || !globMagic.MatchString(at(2))) && !globMagic.MatchString(at(3)):
			raw = min(4, len(parts))
		case len(parts) > 0 && globDriveRoot.MatchString(parts[0]):
			raw = 1
		}
	}
	segments := make([]packageGlobSegment, 0, len(parts))
	for i, part := range parts {
		if i < raw {
			segments = append(segments, packageGlobSegment{isLiteral: true, literal: part})
			continue
		}
		segment, err := newPackageGlobSegment(part, nocase)
		if err != nil {
			return nil, err
		}
		segments = append(segments, segment)
	}
	return segments, nil
}

var windowsUNCPattern = lazyregexp.New(`^//[^/]+`)

// slashSplit splits at runs of slashes, as p.split(/\/+/) does, and keeps a leading empty segment for a UNC path on Windows.
func slashSplit(pattern string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(pattern); {
		if pattern[i] != '/' {
			i++
			continue
		}
		parts = append(parts, pattern[start:i])
		for i < len(pattern) && pattern[i] == '/' {
			i++
		}
		start = i
	}
	parts = append(parts, pattern[start:])
	if runtime.GOOS == "windows" && windowsUNCPattern.MatchString(pattern) {
		parts = append([]string{""}, parts...)
	}
	return parts
}

// partAt is parts[i] and whether it exists, as JavaScript reads an array past its end as undefined.
func partAt(parts []string, i int) (string, bool) {
	if i < 0 || i >= len(parts) {
		return "", false
	}
	return parts[i], true
}

func indexFrom(parts []string, value string, from int) int {
	for i := max(from, 0); i < len(parts); i++ {
		if parts[i] == value {
			return i
		}
	}
	return -1
}

// firstPhasePreProcess is minimatch's single-pattern preprocessing: "<pre>/**/../<p>/<p>/<rest>" becomes two patterns, "." and empty segments after the first and before the last are dropped, and "<p>/.." is removed.
func firstPhasePreProcess(globParts [][]string) [][]string {
	for didSomething := true; didSomething; {
		didSomething = false
		for k := 0; k < len(globParts); k++ {
			parts := globParts[k]
			for gs := indexFrom(parts, "**", 0); gs != -1; gs = indexFrom(parts, "**", gs+1) {
				gss := gs
				for gss+1 < len(parts) && parts[gss+1] == "**" {
					gss++
				}
				if gss > gs {
					parts = slices.Delete(parts, gs+1, gss+1)
				}
				next, _ := partAt(parts, gs+1)
				p, _ := partAt(parts, gs+2)
				p2, _ := partAt(parts, gs+3)
				if next != ".." || p == "" || p == "." || p == ".." || p2 == "" || p2 == "." || p2 == ".." {
					continue
				}
				didSomething = true
				parts = slices.Delete(parts, gs, gs+1)
				other := slices.Clone(parts)
				other[gs] = "**"
				globParts = append(globParts, other)
				gs--
			}
			for i := 1; i < len(parts)-1; i++ {
				p := parts[i]
				// The second empty segment of a path that starts with "//" stays.
				if i == 1 && p == "" && parts[0] == "" {
					continue
				}
				if p == "." || p == "" {
					didSomething = true
					parts = slices.Delete(parts, i, i+1)
					i--
				}
			}
			if parts[0] == "." && len(parts) == 2 && (parts[1] == "." || parts[1] == "") {
				didSomething = true
				parts = parts[:1]
			}
			for dd := indexFrom(parts, "..", 1); dd != -1; dd = indexFrom(parts, "..", dd+1) {
				if p, _ := partAt(parts, dd-1); p != "" && p != "." && p != ".." && p != "**" {
					didSomething = true
					following, _ := partAt(parts, dd+1)
					var dot []string
					if dd == 1 && following == "**" {
						dot = []string{"."}
					}
					parts = slices.Replace(parts, dd-1, dd+1, dot...)
					if len(parts) == 0 {
						parts = append(parts, "")
					}
					dd -= 2
				}
			}
			globParts[k] = parts
		}
	}
	return globParts
}

// secondPhasePreProcess is minimatch's multi-pattern dedupe: a pattern that another one covers is dropped.
func secondPhasePreProcess(globParts [][]string) [][]string {
	for i := 0; i < len(globParts)-1; i++ {
		for j := i + 1; j < len(globParts); j++ {
			if matched, ok := partsMatch(globParts[i], globParts[j]); ok {
				globParts[i] = nil
				globParts[j] = matched
				break
			}
		}
	}
	return slices.DeleteFunc(globParts, func(parts []string) bool { return len(parts) == 0 })
}

// partsMatch is minimatch's partsMatch with emptyGSMatch: the pattern that covers both a and b, if one does.
func partsMatch(a, b []string) ([]string, bool) {
	ai, bi := 0, 0
	result := []string{}
	which := ""
	for ai < len(a) && bi < len(b) {
		aNext, aHasNext := partAt(a, ai+1)
		bNext, bHasNext := partAt(b, bi+1)
		switch {
		case a[ai] == b[bi]:
			if which == "b" {
				result = append(result, b[bi])
			} else {
				result = append(result, a[ai])
			}
			ai++
			bi++
		case a[ai] == "**" && aHasNext && b[bi] == aNext:
			result = append(result, a[ai])
			ai++
		case b[bi] == "**" && bHasNext && a[ai] == bNext:
			result = append(result, b[bi])
			bi++
		case a[ai] == "*" && b[bi] != "" && !strings.HasPrefix(b[bi], ".") && b[bi] != "**":
			if which == "b" {
				return nil, false
			}
			which = "a"
			result = append(result, a[ai])
			ai++
			bi++
		case b[bi] == "*" && a[ai] != "" && !strings.HasPrefix(a[ai], ".") && a[ai] != "**":
			if which == "a" {
				return nil, false
			}
			which = "b"
			result = append(result, b[bi])
			ai++
			bi++
		default:
			return nil, false
		}
	}
	return result, len(a) == len(b)
}

// packageGlobSegment is a segment of a manifest glob as minimatch's parse gives it to Node's globSync: a globstar, a literal string, which Pattern.test compares exactly, or a RegExp, whose test a fast test replaces for some shapes.
type packageGlobSegment struct {
	globstar  bool
	isLiteral bool
	literal   string
	test      func(name string) bool
}

// newPackageGlobSegment is minimatch's parse of one segment. It fails where new RegExp throws for the segment's source.
func newPackageGlobSegment(part string, nocase bool) (packageGlobSegment, error) {
	if part == "**" {
		return packageGlobSegment{globstar: true}, nil
	}
	if part == "" {
		return packageGlobSegment{isLiteral: true}, nil
	}
	literal, re, err := compileGlobPart(part, nocase)
	switch {
	case err != nil:
		return packageGlobSegment{}, err
	case re == nil:
		return packageGlobSegment{isLiteral: true, literal: literal}, nil
	}
	if fast := fastGlobTest(part, nocase); fast != nil {
		return packageGlobSegment{test: fast}, nil
	}
	return packageGlobSegment{test: re.test}, nil
}

// compileGlobPart is AST.fromGlob(part).toMMPattern() under Node's globSync options, where nocaseMagicOnly leaves the case of a segment without magic alone: the unescaped text of a segment without magic, or else a RegExp of "^", the source and "$", with the i flag when ignoring case and the u flag when a POSIX class needs it.
func compileGlobPart(part string, nocase bool) (string, *jsRegExp, error) {
	source := globASTFromGlob(jsstring.ToUTF16(part)).toRegExpSource(nil)
	if !source.hasMagic {
		return jsstring.FromUTF16(source.body), nil, nil
	}
	flags := ""
	if nocase {
		flags += "i"
	}
	if source.uflag {
		flags += "u"
	}
	re, err := compileJSRegExp(concatUnits(jsText("^"), source.re, jsText("$")), flags)
	return "", re, err
}

// matches is Pattern.test for one segment.
func (s packageGlobSegment) matches(name string) bool {
	switch {
	case s.globstar:
		return true
	case s.isLiteral:
		return s.literal == name
	}
	return s.test(name)
}

var (
	starGlob       = lazyregexp.New(`^\*+$`)
	starDotExtGlob = lazyregexp.New(`^\*+([^+@!?*\[(]*)$`)
	qmarksGlob     = lazyregexp.New(`^\?+([^+@!?*\[(]*)?$`)
	starDotStar    = lazyregexp.New(`^\*+\.\*+$`)
	dotStarGlob    = lazyregexp.New(`^\.\*+$`)
)

// fastGlobTest is the test that minimatch's parse puts in place of the RegExp's for a segment of only "*", "*" and an extension, "?" and an extension, "*.*", or ".*", without the dot option. Ignoring case, an extension is compared after toLowerCase, not by the RegExp's rules.
func fastGlobTest(part string, nocase bool) func(string) bool {
	lower := func(s string) string { return s }
	if nocase {
		lower = jsToLower
	}
	if starGlob.MatchString(part) {
		return func(f string) bool { return f != "" && !strings.HasPrefix(f, ".") }
	}
	if m := starDotExtGlob.FindStringSubmatch(part); m != nil {
		ext := lower(m[1])
		return func(f string) bool { return !strings.HasPrefix(f, ".") && strings.HasSuffix(lower(f), ext) }
	}
	if m := qmarksGlob.FindStringSubmatch(part); m != nil {
		length, ext := utf16Length(m[0]), lower(m[1])
		return func(f string) bool {
			return utf16Length(f) == length && !strings.HasPrefix(f, ".") && (ext == "" || strings.HasSuffix(lower(f), ext))
		}
	}
	if starDotStar.MatchString(part) {
		return func(f string) bool { return !strings.HasPrefix(f, ".") && strings.Contains(f, ".") }
	}
	if dotStarGlob.MatchString(part) {
		return func(f string) bool { return f != "." && f != ".." && strings.HasPrefix(f, ".") }
	}
	return nil
}

func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// canonicalizeRune is ECMAScript's Canonicalize for a RegExp with the i flag and without the u flag, for one UTF-16 code unit: the single code unit that toUpperCase gives, unless that maps a non-ASCII character to ASCII. A surrogate is its own Canonicalize, and so is a character whose full uppercase mapping (SpecialCasing.txt) has several characters. unicode.ToUpper gives the simple mapping, which differs from that only for the Greek letters with ypogegrammeni.
func canonicalizeRune(r rune) rune {
	if r > 0xFFFF || utf16.IsSurrogate(r) || hasMultiCharacterUppercase(r) {
		return r
	}
	if upper := unicode.ToUpper(r); r < 0x80 || upper >= 0x80 {
		return upper
	}
	return r
}

// hasMultiCharacterUppercase reports the lowercase letters whose simple uppercase mapping is one character and whose full mapping (SpecialCasing.txt) is two: the Greek letters with ypogegrammeni.
func hasMultiCharacterUppercase(r rune) bool {
	switch {
	case r >= 0x1F80 && r <= 0x1F87, r >= 0x1F90 && r <= 0x1F97, r >= 0x1FA0 && r <= 0x1FA7:
		return true
	}
	return r == 0x1FB3 || r == 0x1FC3 || r == 0x1FF3
}

// jsToLower is String.prototype.toLowerCase: the full lowercase mapping, which differs from the simple one for U+0130 and for a capital sigma that ends a word (Final_Sigma, Unicode section 3.13).
func jsToLower(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r == 0x130 || r == 0x3A3 || unicode.ToLower(r) != r }) {
		return s
	}
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		switch r {
		case 0x130:
			b.WriteString("i\u0307")
		case 0x3A3:
			if finalSigma(runes, i) {
				b.WriteRune(0x3C2)
			} else {
				b.WriteRune(0x3C3)
			}
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// finalSigma is the Final_Sigma condition for runes[i]: a cased letter before it and none after it, skipping case-ignorable characters.
func finalSigma(runes []rune, i int) bool {
	before := false
	for j := i - 1; j >= 0; j-- {
		if isCaseIgnorable(runes[j]) {
			continue
		}
		before = isCased(runes[j])
		break
	}
	if !before {
		return false
	}
	for j := i + 1; j < len(runes); j++ {
		if isCaseIgnorable(runes[j]) {
			continue
		}
		return !isCased(runes[j])
	}
	return true
}

func isCased(r rune) bool {
	return unicode.In(r, unicode.Lu, unicode.Ll, unicode.Lt, unicode.Other_Lowercase, unicode.Other_Uppercase)
}

// isCaseIgnorable is Case_Ignorable: Mn, Me, Cf, Lm, Sk, and Word_Break MidLetter, MidNumLet and Single_Quote.
func isCaseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', 0xB7, 0x387, 0x55F, 0x5F4, 0x2018, 0x2019, 0x2024, 0x2027, 0xFE13, 0xFE52, 0xFE55, 0xFF07, 0xFF0E, 0xFF1A:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

// compareUTF16 orders strings by UTF-16 code units, as JavaScript's < does.
func compareUTF16(a, b string) int {
	for a != "" && b != "" {
		ra, na := utf8.DecodeRuneInString(a)
		rb, nb := utf8.DecodeRuneInString(b)
		if ra != rb {
			ua, ub := utf16Units(ra), utf16Units(rb)
			if c := cmp.Compare(ua[0], ub[0]); c != 0 {
				return c
			}
			return cmp.Compare(ua[len(ua)-1], ub[len(ub)-1])
		}
		a, b = a[na:], b[nb:]
	}
	return cmp.Compare(len(a), len(b))
}

func utf16Units(r rune) []uint16 {
	if r1, r2 := utf16.EncodeRune(r); r1 != utf8.RuneError {
		return []uint16{uint16(r1), uint16(r2)}
	}
	return []uint16{uint16(r)}
}
