package minimatch

// Ports minimatch 10.2.6 brace-expressions.js, escape.js and unescape.js.

import (
	"strings"

	"github.com/dlclark/regexp2"
)

type posixClass struct {
	name        string
	translation string
	unicode     bool
	negated     bool
}

var posixClasses = []posixClass{
	{"[:alnum:]", `\p{L}\p{Nl}\p{Nd}`, true, false},
	{"[:alpha:]", `\p{L}\p{Nl}`, true, false},
	{"[:ascii:]", `\x00-\x7f`, false, false},
	{"[:blank:]", `\p{Zs}\t`, true, false},
	{"[:cntrl:]", `\p{Cc}`, true, false},
	{"[:digit:]", `\p{Nd}`, true, false},
	{"[:graph:]", `\p{Z}\p{C}`, true, true},
	{"[:lower:]", `\p{Ll}`, true, false},
	{"[:print:]", `\p{C}`, true, false},
	{"[:punct:]", `\p{P}`, true, false},
	{"[:space:]", `\p{Z}\t\r\n\v\f`, true, false},
	{"[:upper:]", `\p{Lu}`, true, false},
	{"[:word:]", `\p{L}\p{Nl}\p{Nd}\p{Pc}`, true, false},
	{"[:xdigit:]", `A-Fa-f0-9`, false, false},
}

// braceEscape escapes `[`, `\`, `]` and `-`, the characters that are special inside a character class.
func braceEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '[' || r == '\\' || r == ']' || r == '-' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// regexpEscape escapes every regular expression metacharacter and JavaScript whitespace.
func regexpEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune("-[]{}()*+?.,\\^$|#", r) || isJSSpace(r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// parseClass takes a glob at a `[` and returns the equivalent regular expression source, whether the unicode flag is needed, the number of
// characters (UTF-16 code units are counted as runes here) consumed, and whether the class is magic.
func parseClass(glob []rune, position int) (src string, uflag bool, consumed int, magic bool) {
	pos := position
	var ranges, negs []string
	i := pos + 1
	sawStart, escaping, negate := false, false, false
	endPos := pos
	rangeStart := rune(-1)
	hasRangeStart := false
	startsWith := func(prefix string, at int) bool {
		p := []rune(prefix)
		if at+len(p) > len(glob) {
			return false
		}
		return string(glob[at:at+len(p)]) == prefix
	}
loop:
	for i < len(glob) {
		c := glob[i]
		if (c == '!' || c == '^') && i == pos+1 {
			negate = true
			i++
			continue
		}
		if c == ']' && sawStart && !escaping {
			endPos = i + 1
			break
		}
		sawStart = true
		if c == '\\' {
			if !escaping {
				escaping = true
				i++
				continue
			}
		}
		if c == '[' && !escaping {
			for _, cls := range posixClasses {
				if startsWith(cls.name, i) {
					if hasRangeStart {
						return "$.", false, len(glob) - pos, true
					}
					i += len([]rune(cls.name))
					if cls.negated {
						negs = append(negs, cls.translation)
					} else {
						ranges = append(ranges, cls.translation)
					}
					uflag = uflag || cls.unicode
					continue loop
				}
			}
		}
		escaping = false
		if hasRangeStart {
			if c > rangeStart {
				ranges = append(ranges, braceEscape(string(rangeStart))+"-"+braceEscape(string(c)))
			} else if c == rangeStart {
				ranges = append(ranges, braceEscape(string(c)))
			}
			hasRangeStart = false
			i++
			continue
		}
		if startsWith("-]", i+1) {
			ranges = append(ranges, braceEscape(string(c)+"-"))
			i += 2
			continue
		}
		if startsWith("-", i+1) {
			rangeStart, hasRangeStart = c, true
			i += 2
			continue
		}
		ranges = append(ranges, braceEscape(string(c)))
		i++
	}
	if endPos < i {
		return "", false, 0, false
	}
	if len(ranges) == 0 && len(negs) == 0 {
		return "$.", false, len(glob) - pos, true
	}
	if len(negs) == 0 && len(ranges) == 1 && !negate && isSingleChar(ranges[0]) {
		r := ranges[0]
		if rs := []rune(r); len(rs) == 2 {
			r = string(rs[1])
		}
		return regexpEscape(r), false, endPos - pos, false
	}
	caret := ""
	if negate {
		caret = "^"
	}
	notCaret := "^"
	if negate {
		notCaret = ""
	}
	sranges := "[" + caret + strings.Join(ranges, "") + "]"
	snegs := "[" + notCaret + strings.Join(negs, "") + "]"
	var comb string
	switch {
	case len(ranges) > 0 && len(negs) > 0:
		comb = "(" + sranges + "|" + snegs + ")"
	case len(ranges) > 0:
		comb = sranges
	default:
		comb = snegs
	}
	return comb, uflag, endPos - pos, true
}

// isSingleChar is /^\\?.$/.test(s).
func isSingleChar(s string) bool {
	rs := []rune(s)
	switch len(rs) {
	case 1:
		return !isLineTerminator(rs[0])
	case 2:
		return rs[0] == '\\' && !isLineTerminator(rs[1])
	}
	return false
}

func isLineTerminator(r rune) bool { return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 }

var (
	unescapeBracketRE   = regexp2.MustCompile(`((?!\\)[^\n\r\u2028\u2029]|^)\[([^/\\])\]`, regexp2.None)
	unescapeBackslashRE = regexp2.MustCompile(`\\([^/])`, regexp2.None)
)

// unescape removes the escapes that escape() adds (magicalBraces on, POSIX paths).
func unescape(s string) string {
	s, _ = unescapeBracketRE.Replace(s, "$1$2", -1, -1)
	s, _ = unescapeBackslashRE.Replace(s, "$1", -1, -1)
	return s
}
