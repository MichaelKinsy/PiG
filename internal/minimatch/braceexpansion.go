package minimatch

// Ports brace-expansion 5.0.12 and balanced-match (the packages minimatch 10.2.6 expands braces with).

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

const (
	expansionMax        = 100_000
	expansionMaxLength  = 4_000_000
	expansionMaxDepth   = 1_000
	expansionMaxRewrite = 1_000

	escSlash  = "\x00SLASH0\x00"
	escOpen   = "\x00OPEN0\x00"
	escClose  = "\x00CLOSE0\x00"
	escComma  = "\x00COMMA0\x00"
	escPeriod = "\x00PERIOD0\x00"
)

type balancedMatch struct {
	start, end int
	pre, body  string
	post       string
}

// balanced is balanced-match's balanced(a, b, str) for single-character strings a and b.
func balanced(a, b, str string) *balancedMatch {
	r, ok := balancedRange(a, b, str)
	if !ok {
		return nil
	}
	return &balancedMatch{start: r[0], end: r[1], pre: str[:r[0]], body: str[r[0]+len(a) : r[1]], post: str[r[1]+len(b):]}
}

func balancedRange(a, b, str string) ([2]int, bool) {
	ai := strings.Index(str, a)
	bi := -1
	if ai >= 0 {
		if rel := strings.Index(str[ai+1:], b); rel >= 0 {
			bi = ai + 1 + rel
		}
	}
	i := ai
	if ai >= 0 && bi > 0 {
		if a == b {
			return [2]int{ai, bi}, true
		}
		var begs []int
		left := len(str)
		right := -1
		var result *[2]int
		for i >= 0 && result == nil {
			switch {
			case i == ai:
				begs = append(begs, i)
				if rel := strings.Index(str[i+1:], a); rel >= 0 {
					ai = i + 1 + rel
				} else {
					ai = -1
				}
			case len(begs) == 1:
				r := begs[len(begs)-1]
				begs = begs[:len(begs)-1]
				result = &[2]int{r, bi}
			default:
				beg := begs[len(begs)-1]
				begs = begs[:len(begs)-1]
				if beg < left {
					left = beg
					right = bi
				}
				if rel := strings.Index(str[i+1:], b); rel >= 0 {
					bi = i + 1 + rel
				} else {
					bi = -1
				}
			}
			if ai < bi && ai >= 0 {
				i = ai
			} else {
				i = bi
			}
		}
		if len(begs) > 0 && right >= 0 {
			result = &[2]int{left, right}
		}
		if result != nil {
			return *result, true
		}
	}
	return [2]int{}, false
}

func escapeBraces(str string) string {
	str = strings.ReplaceAll(str, `\\`, escSlash)
	str = strings.ReplaceAll(str, `\{`, escOpen)
	str = strings.ReplaceAll(str, `\}`, escClose)
	str = strings.ReplaceAll(str, `\,`, escComma)
	return strings.ReplaceAll(str, `\.`, escPeriod)
}

func unescapeBraces(str string) string {
	str = strings.ReplaceAll(str, escSlash, `\`)
	str = strings.ReplaceAll(str, escOpen, "{")
	str = strings.ReplaceAll(str, escClose, "}")
	str = strings.ReplaceAll(str, escComma, ",")
	return strings.ReplaceAll(str, escPeriod, ".")
}

// parseCommaParts is str.split(",") that keeps nested brace sections whole.
func parseCommaParts(str string) []string {
	var parts []string
	carry := ""
	for {
		m := balanced("{", "}", str)
		if m == nil {
			tail := strings.Split(str, ",")
			tail[0] = carry + tail[0]
			return append(parts, tail...)
		}
		p := strings.Split(m.pre, ",")
		p[0] = carry + p[0]
		p[len(p)-1] += "{" + m.body + "}"
		if len(m.post) == 0 {
			return append(parts, p...)
		}
		carry = p[len(p)-1]
		p = p[:len(p)-1]
		parts = append(parts, p...)
		str = m.post
	}
}

// braceExpansionExpand is expand(str) of brace-expansion.
func braceExpansionExpand(str string) []string {
	if str == "" {
		return nil
	}
	if strings.HasPrefix(str, "{}") {
		str = `\{\}` + str[2:]
	}
	expanded := expandBraces(escapeBraces(str), expansionMax, expansionMaxLength, expansionMaxDepth, 0, expansionMaxRewrite, true)
	for i := range expanded {
		expanded[i] = unescapeBraces(expanded[i])
	}
	return expanded
}

var (
	paddedRE          = lazyregexp.New(`^-?0\d`)
	numericSequenceRE = lazyregexp.New(`^-?\d+\.\.-?\d+(?:\.\.-?\d+)?$`)
	alphaSequenceRE   = lazyregexp.New(`^[a-zA-Z]\.\.[a-zA-Z](?:\.\.-?\d+)?$`)
	dollarEndRE       = lazyregexp.New(`\$$`)
)

// numeric is `!isNaN(str) ? parseInt(str, 10) : str.charCodeAt(0)`.
func numeric(str string) int {
	if n, err := strconv.Atoi(strings.TrimSpace(str)); err == nil && strings.TrimSpace(str) != "" {
		return n
	}
	if u := toUTF16(str); len(u) > 0 {
		return int(u[0])
	}
	return 0
}

func toUTF16(s string) []uint16 {
	var out []uint16
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

func combine(acc []string, pre string, values []string, maxN, maxLength int, dropEmpties bool) []string {
	var out []string
	length := 0
	for _, a := range acc {
		for _, v := range values {
			if len(out) >= maxN {
				return out
			}
			expansion := a + pre + v
			if dropEmpties && expansion == "" {
				continue
			}
			if length+len(toUTF16(expansion)) > maxLength {
				return out
			}
			out = append(out, expansion)
			length += len(toUTF16(expansion))
		}
	}
	return out
}

func expandSequence(body string, isAlpha bool, maxN, maxLength int) []string {
	n := strings.Split(body, "..")
	var result []string
	if len(n) < 2 {
		return result
	}
	x, y := numeric(n[0]), numeric(n[1])
	width := max(len(n[0]), len(n[1]))
	incr := 1
	if len(n) == 3 {
		v := numeric(n[2])
		if v < 0 {
			v = -v
		}
		incr = max(v, 1)
	}
	reverse := y < x
	if reverse {
		incr = -incr
	}
	pad := false
	for _, el := range n {
		if paddedRE.MatchString(el) {
			pad = true
		}
	}
	length := 0
	for i := x; (!reverse && i <= y || reverse && i >= y) && len(result) < maxN; i += incr {
		var c string
		if isAlpha {
			c = string(rune(i))
			if c == `\` {
				c = ""
			}
		} else {
			c = strconv.Itoa(i)
			if pad {
				if need := width - len(c); need > 0 {
					z := strings.Repeat("0", need)
					if i < 0 {
						c = "-" + z + c[1:]
					} else {
						c = z + c
					}
				}
			}
		}
		if length+len(toUTF16(c)) > maxLength {
			break
		}
		result = append(result, c)
		length += len(toUTF16(c))
	}
	return result
}

func expandBraces(str string, maxN, maxLength, maxDepth, depth, maxRewrites int, isTop bool) []string {
	if depth > maxDepth {
		return []string{str}
	}
	acc := []string{""}
	rewrites := 0
	dropEmpties := false
	firstGroup := true
	for {
		m := balanced("{", "}", str)
		if m == nil {
			return combine(acc, str, []string{""}, maxN, maxLength, dropEmpties)
		}
		pre := m.pre
		if dollarEndRE.MatchString(pre) {
			acc = combine(acc, pre+"{"+m.body+"}", []string{""}, maxN, maxLength, dropEmpties && len(m.post) == 0)
			firstGroup = false
			if len(m.post) == 0 {
				break
			}
			str = m.post
			continue
		}
		isNumericSequence := numericSequenceRE.MatchString(m.body)
		isAlphaSequence := alphaSequenceRE.MatchString(m.body)
		isSequence := isNumericSequence || isAlphaSequence
		isOptions := strings.Contains(m.body, ",")
		if !isSequence && !isOptions {
			if rewrites < maxRewrites && hasCommaBrace(m.post) {
				rewrites++
				str = m.pre + "{" + m.body + escClose + m.post
				isTop = true
				continue
			}
			return combine(acc, pre+"{"+m.body+"}"+m.post, []string{""}, maxN, maxLength, dropEmpties)
		}
		if firstGroup {
			dropEmpties = isTop && !isSequence
			firstGroup = false
		}
		var values []string
		if isSequence {
			values = expandSequence(m.body, isAlphaSequence, maxN, maxLength)
		} else {
			n := parseCommaParts(m.body)
			if len(n) == 1 {
				inner := expandBraces(n[0], maxN, maxLength, maxDepth, depth+1, maxRewrites, false)
				n = make([]string, len(inner))
				for i, v := range inner {
					n[i] = "{" + v + "}"
				}
				if len(n) == 1 {
					acc = combine(acc, pre+n[0], []string{""}, maxN, maxLength, dropEmpties && len(m.post) == 0)
					if len(m.post) == 0 {
						break
					}
					str = m.post
					continue
				}
			}
			dropsEmpties := dropEmpties && len(m.post) == 0 && pre == ""
			for _, a := range acc {
				if a != "" {
					dropsEmpties = false
				}
			}
			valuesLength := 0
		outer:
			for _, part := range n {
				for _, v := range expandBraces(part, maxN, maxLength, maxDepth, depth+1, maxRewrites, false) {
					if dropsEmpties && v == "" {
						continue
					}
					if len(values) >= maxN || valuesLength+len(toUTF16(v)) > maxLength {
						break outer
					}
					values = append(values, v)
					valuesLength += len(toUTF16(v))
				}
			}
		}
		acc = combine(acc, pre, values, maxN, maxLength, dropEmpties && len(m.post) == 0)
		if len(m.post) == 0 {
			break
		}
		str = m.post
	}
	return acc
}

// hasCommaBrace is /,(?!,).*\}/.test(post): a comma not followed by a comma, then anything and a closing brace.
func hasCommaBrace(post string) bool {
	for i := 0; i < len(post); i++ {
		if post[i] != ',' || (i+1 < len(post) && post[i+1] == ',') {
			continue
		}
		rest := post[i+1:]
		// `.` does not match line terminators.
		line := rest
		if cut := strings.IndexAny(rest, "\n\r\u2028\u2029"); cut >= 0 {
			line = rest[:cut]
		}
		if strings.Contains(line, "}") {
			return true
		}
	}
	return false
}
