package packagecontent

import (
	"errors"
	"math"
	"math/rand/v2"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// braceExpansionMax is brace-expansion's EXPANSION_MAX, the most expansions one brace set gives.
const braceExpansionMax = 100_000

// braceEscapes are the markers brace-expansion puts in place of an escaped brace, comma, period or backslash while it expands, and the replacers that put them in and take them out. As in brace-expansion, each marker holds a random number, so a pattern cannot spell one. They are built on first use, outside the package initialization every pig start pays.
type braceEscapes struct {
	slash, open, close, comma, period string
	escape, unescape                  *strings.Replacer
}

var (
	braceEscapesInit sync.Once
	braceEscapesSet  *braceEscapes
)

func braceEscapesOnce() *braceEscapes {
	braceEscapesInit.Do(func() {
		e := &braceEscapes{slash: braceMarker("SLASH"), open: braceMarker("OPEN"), close: braceMarker("CLOSE"), comma: braceMarker("COMMA"), period: braceMarker("PERIOD")}
		e.escape = strings.NewReplacer(`\\`, e.slash, `\{`, e.open, `\}`, e.close, `\,`, e.comma, `\.`, e.period)
		e.unescape = strings.NewReplacer(e.slash, `\`, e.open, "{", e.close, "}", e.comma, ",", e.period, ".")
		braceEscapesSet = e
	})
	return braceEscapesSet
}

func braceMarker(name string) string {
	return "\x00" + name + jsnumber.String(rand.Float64()) + "\x00"
}

var (
	numericSequence = lazyregexp.New(`^-?\d+\.\.-?\d+(?:\.\.-?\d+)?$`)
	alphaSequence   = lazyregexp.New(`^[a-zA-Z]\.\.[a-zA-Z](?:\.\.-?\d+)?$`)
	paddedNumber    = lazyregexp.New(`^-?0\d`)
)

// braceExpand is minimatch's braceExpand: a pattern with a "{" that a "}" closes before another "{" or a line terminator expands as brace-expansion's expand does, and any other pattern is itself.
func braceExpand(pattern string) ([]string, error) {
	if !hasBraceSet(pattern) {
		return []string{pattern}, nil
	}
	return expandBraces(pattern)
}

// hasBraceSet is /\{(?:(?!\{).)*\}/.test(pattern).
func hasBraceSet(pattern string) bool {
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '{' {
			continue
		}
		for _, r := range pattern[i+1:] {
			if r == '}' {
				return true
			}
			if r == '{' || isLineTerminator(r) {
				break
			}
		}
	}
	return false
}

func isLineTerminator(r rune) bool {
	return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029
}

// expandBraces is brace-expansion's expand.
func expandBraces(str string) ([]string, error) {
	if str == "" {
		return nil, nil
	}
	if strings.HasPrefix(str, "{}") {
		str = `\{\}` + str[2:]
	}
	expanded, err := expandBraceSet(escapeBraces(str), true)
	if err != nil {
		return nil, err
	}
	for i, s := range expanded {
		expanded[i] = unescapeBraces(s)
	}
	return expanded, nil
}

func escapeBraces(str string) string { return braceEscapesOnce().escape.Replace(str) }

func unescapeBraces(str string) string { return braceEscapesOnce().unescape.Replace(str) }

// balancedBraces is balanced-match's balanced("{", "}", str): the text before, inside and after the first balanced pair.
func balancedBraces(str string) (pre, body, post string, ok bool) {
	start, end, ok := braceRange(str)
	if !ok {
		return "", "", "", false
	}
	return str[:start], str[start+1 : end], str[end+1:], true
}

// braceRange is balanced-match's range for "{" and "}".
func braceRange(str string) (int, int, bool) {
	indexFrom := func(c byte, from int) int {
		if from > len(str) {
			return -1
		}
		if i := strings.IndexByte(str[from:], c); i >= 0 {
			return from + i
		}
		return -1
	}
	ai := strings.IndexByte(str, '{')
	bi := indexFrom('}', ai+1)
	if ai < 0 || bi <= 0 {
		return 0, 0, false
	}
	var begs []int
	left, right, haveRight := len(str), 0, false
	for i := ai; i >= 0; {
		switch {
		case i == ai:
			begs = append(begs, i)
			ai = indexFrom('{', i+1)
		case len(begs) == 1:
			return begs[0], bi, true
		default:
			if len(begs) > 0 {
				beg := begs[len(begs)-1]
				begs = begs[:len(begs)-1]
				if beg < left {
					left, right, haveRight = beg, bi, true
				}
			}
			bi = indexFrom('}', i+1)
		}
		if ai < bi && ai >= 0 {
			i = ai
		} else {
			i = bi
		}
	}
	if len(begs) > 0 && haveRight {
		return left, right, true
	}
	return 0, 0, false
}

// parseCommaParts is brace-expansion's parseCommaParts: str split at its top-level commas, a brace set staying whole. It walks the sets in turn, as 5.0.12 does, and builds the part a later set continues in one buffer, so a long run of sets costs time in proportion to its length; Node's bundled copy recurses to the same parts.
func parseCommaParts(str string) []string {
	var parts []string
	var carry strings.Builder
	for {
		pre, body, post, ok := balancedBraces(str)
		if !ok {
			tail := strings.Split(str, ",")
			carry.WriteString(tail[0])
			tail[0] = carry.String()
			return append(parts, tail...)
		}
		p := strings.Split(pre, ",")
		if len(p) > 1 {
			carry.WriteString(p[0])
			parts = append(parts, carry.String())
			parts = append(parts, p[1:len(p)-1]...)
			carry.Reset()
		}
		carry.WriteString(p[len(p)-1])
		carry.WriteString("{")
		carry.WriteString(body)
		carry.WriteString("}")
		if post == "" {
			return append(parts, carry.String())
		}
		str = post
	}
}

// expandBraceSet is brace-expansion's expand_.
func expandBraceSet(str string, isTop bool) ([]string, error) {
	pre, body, rawPost, ok := balancedBraces(str)
	if !ok {
		return []string{str}, nil
	}
	post := []string{""}
	if rawPost != "" {
		var err error
		if post, err = expandBraceSet(rawPost, false); err != nil {
			return nil, err
		}
	}
	var expansions []string
	if strings.HasSuffix(pre, "$") {
		for k := 0; k < len(post) && k < braceExpansionMax; k++ {
			expansions = append(expansions, pre+"{"+body+"}"+post[k])
		}
		return expansions, nil
	}
	isNumeric, isAlpha := numericSequence.MatchString(body), alphaSequence.MatchString(body)
	isSequence := isNumeric || isAlpha
	if !isSequence && !strings.Contains(body, ",") {
		if commaBeforeClose(rawPost) {
			return expandBraceSet(pre+"{"+body+braceEscapesOnce().close+rawPost, true)
		}
		return []string{str}, nil
	}
	var n []string
	if isSequence {
		n = strings.Split(body, "..")
	} else {
		n = parseCommaParts(body)
		if len(n) == 1 {
			var err error
			if n, err = expandBraceSet(n[0], false); err != nil {
				return nil, err
			}
			for i := range n {
				n[i] = "{" + n[i] + "}"
			}
			if len(n) == 1 {
				out := make([]string, len(post))
				for i, p := range post {
					out[i] = pre + n[0] + p
				}
				return out, nil
			}
		}
	}
	var items []string
	if isSequence {
		var err error
		if items, err = braceSequence(n, isAlpha); err != nil {
			return nil, err
		}
	} else {
		for _, part := range n {
			expanded, err := expandBraceSet(part, false)
			if err != nil {
				return nil, err
			}
			items = append(items, expanded...)
		}
	}
	for _, item := range items {
		for k := 0; k < len(post) && len(expansions) < braceExpansionMax; k++ {
			if expansion := pre + item + post[k]; !isTop || isSequence || expansion != "" {
				expansions = append(expansions, expansion)
			}
		}
	}
	return expansions, nil
}

// commaBeforeClose is /,(?!,).*\}/.test(post): a comma not followed by a comma, and later on the same line a "}".
func commaBeforeClose(post string) bool {
	for i := 0; i < len(post); i++ {
		if post[i] != ',' || i+1 < len(post) && post[i+1] == ',' {
			continue
		}
		for _, r := range post[i+1:] {
			if r == '}' {
				return true
			}
			if isLineTerminator(r) {
				break
			}
		}
	}
	return false
}

// jsMaxArrayLength is the most elements V8 lets Array.prototype.push hold; one more throws a RangeError.
const jsMaxArrayLength = 1 << 27

// errInvalidArrayLength is the RangeError of a push past jsMaxArrayLength.
var errInvalidArrayLength = errors.New("Invalid array length")

// braceSequence is the sequence branch of expand_: numbers, padded to the wider bound when a bound has a leading zero, or letters by char code, with a backslash left out, stepping by the third part. It computes in JavaScript numbers, so a bound or step beyond 2^53 rounds as in Node. Each item gives at least one expansion, so only the first EXPANSION_MAX items can reach the expansions and only those are formatted; the loop still counts on, because brace-expansion pushes every item and a sequence with more than jsMaxArrayLength items, such as one whose step stops advancing, throws in Node.
func braceSequence(n []string, isAlpha bool) ([]string, error) {
	numeric := func(s string) float64 {
		if v := jsnumber.Parse(s); !math.IsNaN(v) {
			return v
		}
		return float64(s[0])
	}
	x, y := numeric(n[0]), numeric(n[1])
	width := max(len(n[0]), len(n[1]))
	incr := 1.0
	if len(n) == 3 {
		incr = math.Max(math.Abs(numeric(n[2])), 1)
	}
	reverse := y < x
	if reverse {
		incr = -incr
	}
	pad := paddedNumber.MatchString(n[0]) || paddedNumber.MatchString(n[1]) || len(n) == 3 && paddedNumber.MatchString(n[2])
	var out []string
	for i, count := x, 0; reverse && i >= y || !reverse && i <= y; i, count = i+incr, count+1 {
		if count == jsMaxArrayLength {
			return nil, errInvalidArrayLength
		}
		if len(out) == braceExpansionMax {
			continue
		}
		var c string
		if isAlpha {
			if c = string(rune(i)); c == `\` {
				c = ""
			}
		} else {
			c = jsnumber.String(i)
			if pad {
				if need := width - len(c); need > 0 {
					zeros := strings.Repeat("0", need)
					if i < 0 {
						c = "-" + zeros + c[1:]
					} else {
						c = zeros + c
					}
				}
			}
		}
		out = append(out, c)
	}
	return out, nil
}
