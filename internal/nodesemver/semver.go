// Package nodesemver translates the npm semver package (semver 7.8.5) calls Pi's package manager makes. ValidRange, Satisfies and MaxSatisfying are validRange, satisfies and maxSatisfying; Parse is valid, and SemVer.Compare gives gt and rcompare. It implements node-semver's default options only (loose and includePrerelease false) and keeps its regular expressions, including the length-bounded forms its Range and SemVer parsers match against.
package nodesemver

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

const (
	maxLength          = 256
	maxSafeInteger     = 9007199254740991
	maxSafeBuildLength = maxLength - 6
)

var errInvalidVersion = errors.New("invalid version")

// Source fragments of node-semver internal/re.js createToken, in its order.
const (
	letterDashNumber          = `[a-zA-Z0-9-]`
	numericIdentifier         = `0|[1-9]\d*`
	numericIdentifierLoose    = `\d+`
	nonNumericIdentifier      = `\d*[a-zA-Z-]` + letterDashNumber + `*`
	mainVersion               = `(` + numericIdentifier + `)\.(` + numericIdentifier + `)\.(` + numericIdentifier + `)`
	mainVersionLoose          = `(` + numericIdentifierLoose + `)\.(` + numericIdentifierLoose + `)\.(` + numericIdentifierLoose + `)`
	prereleaseIdentifier      = `(?:` + nonNumericIdentifier + `|` + numericIdentifier + `)`
	prereleaseIdentifierLoose = `(?:` + nonNumericIdentifier + `|` + numericIdentifierLoose + `)`
	prerelease                = `(?:-(` + prereleaseIdentifier + `(?:\.` + prereleaseIdentifier + `)*))`
	prereleaseLoose           = `(?:-?(` + prereleaseIdentifierLoose + `(?:\.` + prereleaseIdentifierLoose + `)*))`
	buildIdentifier           = letterDashNumber + `+`
	build                     = `(?:\+(` + buildIdentifier + `(?:\.` + buildIdentifier + `)*))`
	fullPlain                 = `v?` + mainVersion + prerelease + `?` + build + `?`
	loosePlain                = `[v=\s]*` + mainVersionLoose + prereleaseLoose + `?` + build + `?`
	gtlt                      = `((?:<|>)?=?)`
	xRangeIdentifier          = numericIdentifier + `|x|X|\*`
	xRangePlain               = `[v=\s]*(` + xRangeIdentifier + `)(?:\.(` + xRangeIdentifier + `)(?:\.(` + xRangeIdentifier + `)(?:` + prerelease + `)?` + build + `?)?)?`
	loneTilde                 = `(?:~>?)`
	loneCaret                 = `(?:\^)`
)

// expression names one node-semver internal/re.js token that Range or SemVer matches with.
type expression int

const (
	reFull expression = iota
	reBuild
	reXRange
	reTildeTrim
	reTilde
	reCaretTrim
	reCaret
	reComparator
	reComparatorTrim
	reHyphenRange
	reStar
	reGTE0
	expressionCount
)

type expressions [expressionCount]*regexp.Regexp

// makeSafeRegex is internal/re.js makeSafeRegex: it bounds the repetition of whitespace, digits and identifier characters as node-semver's safeRe does.
func makeSafeRegex(value string) string {
	for _, bound := range []struct {
		token string
		max   int
	}{{`\s`, 1}, {`\d`, maxLength}, {letterDashNumber, maxSafeBuildLength}} {
		limit := strconv.Itoa(bound.max)
		value = strings.ReplaceAll(value, bound.token+"*", bound.token+"{0,"+limit+"}")
		value = strings.ReplaceAll(value, bound.token+"+", bound.token+"{1,"+limit+"}")
	}
	return value
}

func compileExpressions(transform func(string) string) *expressions {
	sources := [expressionCount]string{
		reFull:           `^` + fullPlain + `$`,
		reBuild:          build,
		reXRange:         `^` + gtlt + `\s*` + xRangePlain + `$`,
		reTildeTrim:      `(\s*)` + loneTilde + `\s+`,
		reTilde:          `^` + loneTilde + xRangePlain + `$`,
		reCaretTrim:      `(\s*)` + loneCaret + `\s+`,
		reCaret:          `^` + loneCaret + xRangePlain + `$`,
		reComparator:     `^` + gtlt + `\s*(` + fullPlain + `)$|^$`,
		reComparatorTrim: `(\s*)` + gtlt + `\s*(` + loosePlain + `|` + xRangePlain + `)`,
		reHyphenRange:    `^\s*(` + xRangePlain + `)\s+-\s+(` + xRangePlain + `)\s*$`,
		reStar:           `(<|>)?=?\s*\*`,
		reGTE0:           `^\s*>=\s*0\.0\.0\s*$`,
	}
	var compiled expressions
	for i, source := range sources {
		compiled[i] = regexp.MustCompile(transform(source))
	}
	return &compiled
}

// buildStrip is Range.parseRange's global build-metadata stripper, which uses the unbounded BUILD source.
var buildStrip = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(build) })

// Both sets compile on first use because most processes never parse an npm version. The bounded set expands to about 60,000 instructions and takes tens of milliseconds to compile, so only text that needs it compiles it.
var (
	unboundedExpressions = sync.OnceValue(func() *expressions { return compileExpressions(func(source string) string { return source }) })
	boundedExpressions   = sync.OnceValue(func() *expressions { return compileExpressions(makeSafeRegex) })
)

// re returns expression id for text; it matches exactly as node-semver's safeRe token does. A bounded repetition of one character class can differ from its unbounded form only on text with a longer run of that class, so text without two adjacent whitespace characters, 257 adjacent digits or 251 adjacent identifier characters uses the unbounded set. Range expressions see only whitespace-normalized text and the version expression has no whitespace class, so Go's \s and JavaScript's \s never disagree.
func re(id expression, text string) *regexp.Regexp {
	whitespace, digits, identifier := 0, 0, 0
	for i := range len(text) {
		c := text[i]
		whitespace, digits, identifier = whitespace+1, digits+1, identifier+1
		if c != ' ' && c != '\t' && c != '\n' && c != '\f' && c != '\r' {
			whitespace = 0
		}
		if c < '0' || c > '9' {
			digits = 0
		}
		if c != '-' && (c < '0' || c > '9') && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			identifier = 0
		}
		if whitespace > 1 || digits > maxLength || identifier > maxSafeBuildLength {
			return boundedExpressions()[id]
		}
	}
	return unboundedExpressions()[id]
}

// SemVer is a parsed version (classes/semver.js). Prerelease identifiers keep their text: node-semver's numeric identifiers are canonical digit strings, so text equality is its === and numeric comparison happens in compareIdentifiers.
type SemVer struct {
	major, minor, patch uint64
	prerelease          []string
	version             string
}

// Parse is parse(version): the SemVer node-semver constructs, or an error where it returns null.
func Parse(version string) (*SemVer, error) {
	if jsstring.Length(version) > maxLength {
		return nil, errInvalidVersion
	}
	trimmed := jsTrim(version)
	m := re(reFull, trimmed).FindStringSubmatch(trimmed)
	if m == nil {
		return nil, errInvalidVersion
	}
	v := &SemVer{}
	var err error
	if v.major, err = versionComponent(m[1]); err != nil {
		return nil, err
	}
	if v.minor, err = versionComponent(m[2]); err != nil {
		return nil, err
	}
	if v.patch, err = versionComponent(m[3]); err != nil {
		return nil, err
	}
	v.version = strconv.FormatUint(v.major, 10) + "." + strconv.FormatUint(v.minor, 10) + "." + strconv.FormatUint(v.patch, 10)
	if m[4] != "" {
		v.prerelease = strings.Split(m[4], ".")
		v.version += "-" + m[4]
	}
	return v, nil
}

// versionComponent applies the constructor's MAX_SAFE_INTEGER bound. Every integer above it converts to a double above it, so the exact test agrees.
func versionComponent(digits string) (uint64, error) {
	if len(digits) > len(strconv.Itoa(maxSafeInteger)) {
		return 0, errInvalidVersion
	}
	value, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || value > maxSafeInteger {
		return 0, errInvalidVersion
	}
	return value, nil
}

// Compare is SemVer.compare: -1, 0 or 1, ignoring build metadata.
func (v *SemVer) Compare(other *SemVer) int {
	if other.version == v.version {
		return 0
	}
	if c := v.compareMain(other); c != 0 {
		return c
	}
	return v.comparePre(other)
}

func (v *SemVer) compareMain(other *SemVer) int {
	for _, pair := range [3][2]uint64{{v.major, other.major}, {v.minor, other.minor}, {v.patch, other.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
}

func (v *SemVer) comparePre(other *SemVer) int {
	switch {
	case len(v.prerelease) > 0 && len(other.prerelease) == 0:
		return -1
	case len(v.prerelease) == 0 && len(other.prerelease) > 0:
		return 1
	case len(v.prerelease) == 0:
		return 0
	}
	for i := 0; ; i++ {
		switch {
		case i >= len(v.prerelease) && i >= len(other.prerelease):
			return 0
		case i >= len(other.prerelease):
			return 1
		case i >= len(v.prerelease):
			return -1
		case v.prerelease[i] == other.prerelease[i]:
			continue
		default:
			return compareIdentifiers(v.prerelease[i], other.prerelease[i])
		}
	}
}

// compareIdentifiers is internal/identifiers.js: digit-only identifiers compare as JavaScript numbers (doubles) and sort before alphanumeric identifiers.
func compareIdentifiers(a, b string) int {
	anum, bnum := isDigits(a), isDigits(b)
	if anum && bnum {
		x, _ := strconv.ParseFloat(a, 64)
		y, _ := strconv.ParseFloat(b, 64)
		switch {
		case x == y:
			return 0
		case x < y:
			return -1
		default:
			return 1
		}
	}
	switch {
	case a == b:
		return 0
	case anum:
		return -1
	case bnum:
		return 1
	case a < b:
		return -1
	default:
		return 1
	}
}

func isDigits(text string) bool {
	if text == "" {
		return false
	}
	for i := range len(text) {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

// isJSWhitespace is JavaScript's WhiteSpace or LineTerminator, the \s class and the set String.prototype.trim removes. It differs from unicode.IsSpace in U+FEFF (JavaScript whitespace) and U+0085 (not JavaScript whitespace).
func isJSWhitespace(r rune) bool {
	switch r {
	case 0xFEFF:
		return true
	case 0x85:
		return false
	}
	return unicode.IsSpace(r)
}

func jsTrim(text string) string { return strings.TrimFunc(text, isJSWhitespace) }

// jsSplitWhitespace is text.split(/\s+/), which keeps empty leading and trailing fields.
func jsSplitWhitespace(text string) []string {
	fields := []string{}
	start := 0
	for i := 0; i < len(text); {
		r, size := jsstring.DecodeRuneInString(text[i:])
		if !isJSWhitespace(r) {
			i += size
			continue
		}
		fields = append(fields, text[start:i])
		for i < len(text) {
			r, size = jsstring.DecodeRuneInString(text[i:])
			if !isJSWhitespace(r) {
				break
			}
			i += size
		}
		start = i
	}
	return append(fields, text[start:])
}
