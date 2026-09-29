package nodesemver

import (
	"errors"
	"strings"
)

var errInvalidRange = errors.New("invalid SemVer range")

// comparator is classes/comparator.js. A nil semver is Comparator.ANY.
type comparator struct {
	operator string
	semver   *SemVer
	value    string
}

// semverRange is classes/range.js: comparator sets joined by ||, each set ANDed.
type semverRange struct {
	set [][]comparator
}

// ValidRange is validRange(r): the normalized range, or false where it returns null.
func ValidRange(r string) (string, bool) {
	parsed, err := newRange(r)
	if err != nil {
		return "", false
	}
	if formatted := parsed.String(); formatted != "" {
		return formatted, true
	}
	return "*", true
}

// Satisfies is satisfies(version, r).
func Satisfies(version, r string) bool {
	parsed, err := newRange(r)
	return err == nil && parsed.Test(version)
}

// MaxSatisfying is maxSatisfying(versions, r): the first highest version the range accepts.
func MaxSatisfying(versions []string, r string) (string, bool) {
	parsed, err := newRange(r)
	if err != nil {
		return "", false
	}
	var best string
	var bestSemVer *SemVer
	for _, version := range versions {
		if !parsed.Test(version) {
			continue
		}
		candidate, err := Parse(version)
		if err != nil {
			continue
		}
		if bestSemVer == nil || bestSemVer.Compare(candidate) == -1 {
			best, bestSemVer = version, candidate
		}
	}
	return best, bestSemVer != nil
}

// newRange is new Range(r), returning an error where the constructor throws.
func newRange(r string) (*semverRange, error) {
	raw := strings.Join(jsSplitWhitespace(jsTrim(r)), " ")
	var set [][]comparator
	for part := range strings.SplitSeq(raw, "||") {
		comparators, err := parseComparatorSet(jsTrim(part))
		if err != nil {
			return nil, err
		}
		if len(comparators) > 0 {
			set = append(set, comparators)
		}
	}
	if len(set) == 0 {
		return nil, errInvalidRange
	}
	if len(set) > 1 {
		first := set[0]
		kept := make([][]comparator, 0, len(set))
		for _, comparators := range set {
			if !isNullSet(comparators[0]) {
				kept = append(kept, comparators)
			}
		}
		set = kept
		if len(set) == 0 {
			set = [][]comparator{first}
		} else if len(set) > 1 {
			for _, comparators := range set {
				if len(comparators) == 1 && comparators[0].value == "" {
					set = [][]comparator{comparators}
					break
				}
			}
		}
	}
	return &semverRange{set: set}, nil
}

// String is the Range.range getter.
func (r *semverRange) String() string {
	var formatted strings.Builder
	for i, comparators := range r.set {
		if i > 0 {
			formatted.WriteString("||")
		}
		for k, c := range comparators {
			if k > 0 {
				formatted.WriteByte(' ')
			}
			formatted.WriteString(c.value)
		}
	}
	return formatted.String()
}

// Test is Range.test: true when every comparator of any set accepts version.
func (r *semverRange) Test(version string) bool {
	if version == "" {
		return false
	}
	parsed, err := Parse(version)
	if err != nil {
		return false
	}
	for _, comparators := range r.set {
		if testSet(comparators, parsed) {
			return true
		}
	}
	return false
}

func isNullSet(c comparator) bool { return c.value == "<0.0.0-0" }

// parseComparatorSet is Range.parseRange for one ||-separated part.
func parseComparatorSet(r string) ([]comparator, error) {
	r = buildStrip().ReplaceAllString(r, "")
	r = replaceFirst(reHyphenRange, r, hyphenReplace)
	r = re(reComparatorTrim, r).ReplaceAllString(r, "${1}${2}${3}")
	r = re(reTildeTrim, r).ReplaceAllString(r, "${1}~")
	r = re(reCaretTrim, r).ReplaceAllString(r, "${1}^")
	parts := strings.Split(r, " ")
	for i, part := range parts {
		parts[i] = parseComparator(part)
	}
	list := jsSplitWhitespace(strings.Join(parts, " "))
	comparators := make([]comparator, len(list))
	for i, item := range list {
		c, err := newComparator(replaceFirst(reGTE0, jsTrim(item), nil))
		if err != nil {
			return nil, err
		}
		comparators[i] = c
	}
	// A Map keyed by comparator value keeps first-insertion order and the last comparator stored for each value.
	order := make([]string, 0, len(comparators))
	byValue := make(map[string]comparator, len(comparators))
	for _, c := range comparators {
		if isNullSet(c) {
			return []comparator{c}, nil
		}
		if _, seen := byValue[c.value]; !seen {
			order = append(order, c.value)
		}
		byValue[c.value] = c
	}
	result := make([]comparator, 0, len(order))
	for _, value := range order {
		if value == "" && len(order) > 1 {
			continue
		}
		result = append(result, byValue[value])
	}
	return result, nil
}

func newComparator(comp string) (comparator, error) {
	comp = strings.Join(jsSplitWhitespace(jsTrim(comp)), " ")
	m := re(reComparator, comp).FindStringSubmatchIndex(comp)
	if m == nil {
		return comparator{}, errInvalidRange
	}
	c := comparator{operator: group(comp, m, 1)}
	if c.operator == "=" {
		c.operator = ""
	}
	if version := group(comp, m, 2); version != "" {
		parsed, err := Parse(version)
		if err != nil {
			return comparator{}, err
		}
		c.semver = parsed
		c.value = c.operator + parsed.version
	}
	return c, nil
}

func (c comparator) test(version *SemVer) bool {
	if c.semver == nil {
		return true
	}
	order := version.Compare(c.semver)
	switch c.operator {
	case ">":
		return order > 0
	case ">=":
		return order >= 0
	case "<":
		return order < 0
	case "<=":
		return order <= 0
	default:
		return order == 0
	}
}

// testSet also rejects a prerelease version unless a comparator in the set names a prerelease of the same major.minor.patch.
func testSet(comparators []comparator, version *SemVer) bool {
	for _, c := range comparators {
		if !c.test(version) {
			return false
		}
	}
	if len(version.prerelease) == 0 {
		return true
	}
	for _, c := range comparators {
		if c.semver == nil || len(c.semver.prerelease) == 0 {
			continue
		}
		if c.semver.major == version.major && c.semver.minor == version.minor && c.semver.patch == version.patch {
			return true
		}
	}
	return false
}

func parseComparator(comp string) string {
	comp = replaceFirst(reBuild, comp, nil)
	comp = replaceCarets(comp)
	comp = replaceTildes(comp)
	comp = replaceXRanges(comp)
	return replaceFirst(reStar, jsTrim(comp), nil)
}

func isX(id string) bool { return id == "" || strings.ToLower(id) == "x" || id == "*" }

// increment is `${+id + 1}`. Results above MAX_SAFE_INTEGER, where JavaScript rounds or switches to exponent notation, fail version parsing either way.
func increment(id string) string {
	digits := []byte(id)
	for i := len(digits) - 1; i >= 0; i-- {
		if digits[i] < '9' {
			digits[i]++
			return string(digits)
		}
		digits[i] = '0'
	}
	return "1" + string(digits)
}

func replaceTildes(comp string) string {
	fields := jsSplitWhitespace(jsTrim(comp))
	for i, field := range fields {
		fields[i] = replaceFirst(reTilde, field, func(g func(int) string) string {
			major, minor, patch, pre := g(1), g(2), g(3), g(4)
			switch {
			case isX(major):
				return ""
			case isX(minor):
				return ">=" + major + ".0.0 <" + increment(major) + ".0.0-0"
			case isX(patch):
				return ">=" + major + "." + minor + ".0 <" + major + "." + increment(minor) + ".0-0"
			case pre != "":
				return ">=" + major + "." + minor + "." + patch + "-" + pre + " <" + major + "." + increment(minor) + ".0-0"
			default:
				return ">=" + major + "." + minor + "." + patch + " <" + major + "." + increment(minor) + ".0-0"
			}
		})
	}
	return strings.Join(fields, " ")
}

func replaceCarets(comp string) string {
	fields := jsSplitWhitespace(jsTrim(comp))
	for i, field := range fields {
		fields[i] = replaceFirst(reCaret, field, func(g func(int) string) string {
			major, minor, patch, pre := g(1), g(2), g(3), g(4)
			if pre != "" {
				pre = "-" + pre
			}
			switch {
			case isX(major):
				return ""
			case isX(minor):
				return ">=" + major + ".0.0 <" + increment(major) + ".0.0-0"
			case isX(patch):
				if major == "0" {
					return ">=" + major + "." + minor + ".0 <" + major + "." + increment(minor) + ".0-0"
				}
				return ">=" + major + "." + minor + ".0 <" + increment(major) + ".0.0-0"
			case major == "0" && minor == "0":
				return ">=" + major + "." + minor + "." + patch + pre + " <" + major + "." + minor + "." + increment(patch) + "-0"
			case major == "0":
				return ">=" + major + "." + minor + "." + patch + pre + " <" + major + "." + increment(minor) + ".0-0"
			default:
				return ">=" + major + "." + minor + "." + patch + pre + " <" + increment(major) + ".0.0-0"
			}
		})
	}
	return strings.Join(fields, " ")
}

func replaceXRanges(comp string) string {
	fields := jsSplitWhitespace(comp)
	for i, field := range fields {
		fields[i] = replaceXRange(jsTrim(field))
	}
	return strings.Join(fields, " ")
}

func replaceXRange(comp string) string {
	return replaceFirst(reXRange, comp, func(g func(int) string) string {
		operator, major, minor, patch := g(1), g(2), g(3), g(4)
		if (isX(major) && !isX(minor)) || (isX(minor) && patch != "" && !isX(patch)) {
			return comp
		}
		xMajor := isX(major)
		xMinor := xMajor || isX(minor)
		anyX := xMinor || isX(patch)
		if operator == "=" && anyX {
			operator = ""
		}
		switch {
		case xMajor:
			if operator == ">" || operator == "<" {
				return "<0.0.0-0"
			}
			return "*"
		case operator != "" && anyX:
			if xMinor {
				minor = "0"
			}
			patch = "0"
			switch operator {
			case ">":
				operator = ">="
				if xMinor {
					major, minor = increment(major), "0"
				} else {
					minor = increment(minor)
				}
			case "<=":
				operator = "<"
				if xMinor {
					major = increment(major)
				} else {
					minor = increment(minor)
				}
			}
			pre := ""
			if operator == "<" {
				pre = "-0"
			}
			return operator + major + "." + minor + "." + patch + pre
		case xMinor:
			return ">=" + major + ".0.0 <" + increment(major) + ".0.0-0"
		case anyX:
			return ">=" + major + "." + minor + ".0 <" + major + "." + increment(minor) + ".0-0"
		default:
			return g(0)
		}
	})
}

// hyphenReplace turns `1.2 - 3.4` into `>=1.2.0 <3.5.0-0`.
func hyphenReplace(g func(int) string) string {
	from, fromMajor, fromMinor, fromPatch := g(1), g(2), g(3), g(4)
	to, toMajor, toMinor, toPatch, toPre := g(7), g(8), g(9), g(10), g(11)
	switch {
	case isX(fromMajor):
		from = ""
	case isX(fromMinor):
		from = ">=" + fromMajor + ".0.0"
	case isX(fromPatch):
		from = ">=" + fromMajor + "." + fromMinor + ".0"
	default:
		from = ">=" + from
	}
	switch {
	case isX(toMajor):
		to = ""
	case isX(toMinor):
		to = "<" + increment(toMajor) + ".0.0-0"
	case isX(toPatch):
		to = "<" + toMajor + "." + increment(toMinor) + ".0-0"
	case toPre != "":
		to = "<=" + toMajor + "." + toMinor + "." + toPatch + "-" + toPre
	default:
		to = "<=" + to
	}
	return jsTrim(from + " " + to)
}

// replaceFirst is String.prototype.replace with a non-global expression: only the first match is replaced, by replacement's result or by nothing when replacement is nil. A group that did not participate reads as an empty string, which every caller treats as JavaScript's undefined.
func replaceFirst(id expression, text string, replacement func(group func(int) string) string) string {
	m := re(id, text).FindStringSubmatchIndex(text)
	if m == nil {
		return text
	}
	value := ""
	if replacement != nil {
		value = replacement(func(i int) string { return group(text, m, i) })
	}
	return text[:m[0]] + value + text[m[1]:]
}

func group(text string, m []int, i int) string {
	if m[2*i] < 0 {
		return ""
	}
	return text[m[2*i]:m[2*i+1]]
}
