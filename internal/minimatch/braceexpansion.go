package minimatch

// Ports brace-expansion 5.0.12 and balanced-match (the packages minimatch 11.0.3 expands braces with). The expansion keeps the results of a
// brace set as lengths until the last combine, so what it holds is bounded by the results it keeps.

import (
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// The limits of brace-expansion 5.0.12: EXPANSION_MAX is the most expansions one brace set gives; EXPANSION_MAX_LENGTH bounds the total UTF-16
// units of one combine's results, of one sequence, or of one comma set's values; EXPANSION_MAX_DEPTH is how deep nesting is followed; and
// EXPANSION_MAX_REWRITES is how often the "{a},b}" rewrite restarts the scan.
const (
	expansionMax = 100_000
	// upstream: node_modules/brace-expansion/dist/esm/index.js:EXPANSION_MAX_LENGTH
	expansionMaxLength = 4_000_000
	// upstream: node_modules/brace-expansion/dist/esm/index.js:EXPANSION_MAX_DEPTH
	expansionMaxDepth = 1_000
	// upstream: node_modules/brace-expansion/dist/esm/index.js:EXPANSION_MAX_REWRITES
	expansionMaxRewrite = 1_000
)

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

// escapeBraces puts a marker in place of each escaped backslash, brace, comma and period of str.
func escapeBraces(str string) string { return braceEscapesOnce().escape.Replace(str) }

// unescapeBraces is the inverse of escapeBraces.
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

var (
	paddedRE          = lazyregexp.New(`^-?0\d`)
	numericSequenceRE = lazyregexp.New(`^-?\d+\.\.-?\d+(?:\.\.-?\d+)?$`)
	alphaSequenceRE   = lazyregexp.New(`^[a-zA-Z]\.\.[a-zA-Z](?:\.\.-?\d+)?$`)
)

// braceExpansionExpand is expand(str) of brace-expansion for a str with a brace set: it never throws, and it keeps at most EXPANSION_MAX results and EXPANSION_MAX_LENGTH units, leaves nesting deeper than EXPANSION_MAX_DEPTH and rewrites past EXPANSION_MAX_REWRITES literal, and stops a sequence at EXPANSION_MAX items.
func braceExpansionExpand(pattern string) []string {
	if strings.HasPrefix(pattern, "{}") {
		pattern = `\{\}` + pattern[2:]
	}
	expanded := expandBraceGroups(escapeBraces(pattern), 0, true)
	for i, s := range expanded.items {
		expanded.items[i] = unescapeBraces(s)
	}
	return expanded.items
}

// braceValues are expansions with the UTF-16 length of each.
type braceValues struct {
	items []string
	units []int
}

func (v *braceValues) add(s string, units int) {
	v.items = append(v.items, s)
	v.units = append(v.units, units)
}

func literalBraceValue(s string) braceValues {
	return braceValues{items: []string{s}, units: []int{jsstring.Length(s)}}
}

// braceProduct is expand_'s accumulator before its last combine. No combine before the last drops a result, so the accumulator is the first count results of the product of each level's pre text and values in row-major order: result t of a level extends result t / len(values) of the level before with values[t % len(values)]. The product keeps each result's length and builds no text until the last combine, as V8 builds none when it concatenates, so a long chain of brace sets costs time in proportion to its results.
type braceProduct struct {
	levels []braceLevel
	count  int
	units  []int
}

type braceLevel struct {
	pre    string
	values []string
	// count is how many of the level's results a kept result still extends.
	count int
}

func newBraceProduct() *braceProduct {
	return &braceProduct{count: 1, units: []int{0}}
}

// extend is a combine that drops nothing: it appends pre and values as a level, keeping the first results until EXPANSION_MAX of them or one that would take the total past EXPANSION_MAX_LENGTH units.
func (p *braceProduct) extend(pre string, values braceValues) {
	preUnits := jsstring.Length(pre)
	units := make([]int, 0, min(p.count*len(values.items), expansionMax))
	total := 0
fill:
	for a := range p.count {
		for v := range values.items {
			if len(units) >= expansionMax {
				break fill
			}
			n := p.units[a] + preUnits + values.units[v]
			if total+n > expansionMaxLength {
				break fill
			}
			units = append(units, n)
			total += n
		}
	}
	p.levels = append(p.levels, braceLevel{pre: pre, values: values.items, count: len(units)})
	p.count, p.units = len(units), units
	p.prune()
}

// prune keeps only the values some kept result reaches, so memory stays bounded by the results, as V8 frees the strings no result references. The first reached results of a level extend the first ceil(reached / len(values)) results of the level before, and use only its first min(reached, len(values)) values. Counts only ever shrink, and each walk carries a count down until it reaches a level already at or below it, so the levels below that one keep only what it reaches.
func (p *braceProduct) prune() {
	reached := p.count
	for i := len(p.levels) - 1; i >= 0; i-- {
		level := &p.levels[i]
		if i < len(p.levels)-1 {
			if reached >= level.count {
				return
			}
			level.count = reached
		}
		if reached == 0 {
			for j := range i + 1 {
				p.levels[j].values, p.levels[j].count = nil, 0
			}
			return
		}
		if reached < len(level.values) {
			// A copy, so the dropped values and their array can be freed.
			level.values = slices.Clone(level.values[:reached])
		}
		reached = (reached + len(level.values) - 1) / len(level.values)
	}
}

// textOf is result t's text.
func (p *braceProduct) textOf(t int, index []int) string {
	size := 0
	for i := len(p.levels) - 1; i >= 0; i-- {
		level := p.levels[i]
		index[i] = t % len(level.values)
		t /= len(level.values)
		size += len(level.pre) + len(level.values[index[i]])
	}
	var b strings.Builder
	b.Grow(size)
	for i, level := range p.levels {
		b.WriteString(level.pre)
		b.WriteString(level.values[index[i]])
	}
	return b.String()
}

// finish is expand_'s last combine: every result + pre + values[v], in that order, until EXPANSION_MAX results or one that would take the total past EXPANSION_MAX_LENGTH units. With dropEmpties an empty result is skipped before it counts.
func (p *braceProduct) finish(pre string, values braceValues, dropEmpties bool) braceValues {
	if len(p.levels) == 0 && p.count == 1 && pre == "" && !dropEmpties {
		// One empty prefix: the results are the values, which their own limits already bound.
		return values
	}
	size := min(p.count*len(values.items), expansionMax)
	out := braceValues{items: make([]string, 0, size), units: make([]int, 0, size)}
	preUnits := jsstring.Length(pre)
	index := make([]int, len(p.levels))
	total := 0
	for a := range p.count {
		prefix, built := "", false
		for v, value := range values.items {
			if len(out.items) >= expansionMax {
				return out
			}
			n := p.units[a] + preUnits + values.units[v]
			if dropEmpties && n == 0 {
				continue
			}
			if total+n > expansionMaxLength {
				return out
			}
			if !built {
				prefix, built = p.textOf(a, index), true
			}
			out.add(prefix+pre+value, n)
			total += n
		}
	}
	return out
}

// expandBraceGroups is brace-expansion 5.0.12's expand_. It consumes the top-level brace sets left to right and combines each set's values onto the prefixes so far. Empty results are dropped only at the top level and only when the first set that expands is a comma set.
func expandBraceGroups(str string, depth int, isTop bool) braceValues {
	if depth > expansionMaxDepth {
		return literalBraceValue(str)
	}
	acc := newBraceProduct()
	empty := literalBraceValue("")
	rewrites := 0
	dropEmpties, firstGroup := false, true
	for {
		pre, body, post, ok := balancedBraces(str)
		if !ok {
			return acc.finish(str, empty, dropEmpties)
		}
		if strings.HasSuffix(pre, "$") {
			firstGroup = false
			if post == "" {
				return acc.finish(pre+"{"+body+"}", empty, dropEmpties)
			}
			acc.extend(pre+"{"+body+"}", empty)
			str = post
			continue
		}
		isAlpha := alphaSequenceRE.MatchString(body)
		isSequence := isAlpha || numericSequenceRE.MatchString(body)
		if !isSequence && !strings.Contains(body, ",") {
			if rewrites < expansionMaxRewrite && hasCommaBrace(post) {
				rewrites++
				str = pre + "{" + body + braceEscapesOnce().close + post
				isTop = true
				continue
			}
			return acc.finish(pre+"{"+body+"}"+post, empty, dropEmpties)
		}
		if firstGroup {
			dropEmpties = isTop && !isSequence
			firstGroup = false
		}
		var values braceValues
		if isSequence {
			values = braceSequence(body, isAlpha)
		} else {
			n := parseCommaParts(body)
			if len(n) == 1 {
				n = expandBraceGroups(n[0], depth+1, false).items
				for i := range n {
					n[i] = "{" + n[i] + "}"
				}
				if len(n) == 1 {
					if post == "" {
						return acc.finish(pre+n[0], empty, dropEmpties)
					}
					acc.extend(pre+n[0], empty)
					str = post
					continue
				}
			}
			// Values that combine would drop as empty are skipped here, so they do not count against EXPANSION_MAX.
			dropsEmpties := dropEmpties && post == "" && pre == "" && !slices.ContainsFunc(acc.units, func(units int) bool { return units != 0 })
			valuesLength := 0
		parts:
			for _, part := range n {
				expanded := expandBraceGroups(part, depth+1, false)
				for k, v := range expanded.items {
					if dropsEmpties && v == "" {
						continue
					}
					if len(values.items) >= expansionMax || valuesLength+expanded.units[k] > expansionMaxLength {
						break parts
					}
					values.add(v, expanded.units[k])
					valuesLength += expanded.units[k]
				}
			}
		}
		if post == "" {
			return acc.finish(pre, values, dropEmpties)
		}
		acc.extend(pre, values)
		str = post
	}
}

// braceSequence is brace-expansion 5.0.12's expandSequence: numbers, padded to the wider bound when a part has a leading zero, or letters by char code with a backslash left out, stepping by the third part. It stops at EXPANSION_MAX items or EXPANSION_MAX_LENGTH units, so a step that no longer advances a JavaScript number ends there.
func braceSequence(body string, isAlpha bool) braceValues {
	n := strings.Split(body, "..")
	// numeric is !isNaN(str) ? parseInt(str, 10) : str.charCodeAt(0); the parts are ASCII digits with an optional "-", or one ASCII letter.
	numeric := func(s string) float64 {
		if s[0] == '-' || s[0] >= '0' && s[0] <= '9' {
			return jsnumber.Parse(s)
		}
		return float64(s[0])
	}
	x, y := numeric(n[0]), numeric(n[1])
	width := max(len(n[0]), len(n[1]))
	incr := 1.0
	if len(n) == 3 {
		incr = max(math.Abs(numeric(n[2])), 1)
	}
	reverse := y < x
	if reverse {
		incr = -incr
	}
	pad := slices.ContainsFunc(n, paddedRE.MatchString)
	size := expansionMax
	if steps := math.Abs(y-x) / math.Abs(incr); steps < float64(size) {
		size = int(steps) + 1
	}
	out := braceValues{items: make([]string, 0, size), units: make([]int, 0, size)}
	total := 0
	for i := x; (reverse && i >= y || !reverse && i <= y) && len(out.items) < expansionMax; i += incr {
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
		if total+len(c) > expansionMaxLength {
			break
		}
		out.add(c, len(c))
		total += len(c)
	}
	return out
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
