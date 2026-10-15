package packagecontent

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"
)

// globAST is minimatch's AST (ast.ts) for one segment of a glob: the text and extglobs that #parseAST reads, which toRegExpSource turns into the source of a RegExp. Its strings are JavaScript strings, UTF-16 code units, because the parser reads charAt and a class can split a surrogate pair. Node's globSync and Pi's minimatch calls set neither dot, noext nor maxExtglobRecursion, so the AST has minimatch's defaults for them.
type globAST struct {
	// typ is 0 for a plain AST and one of "!?+*@" for an extglob.
	typ         uint16
	root        *globAST
	hasMagic    triBool
	uflag       bool
	parts       *globASTParts
	parent      *globAST
	parentIndex int
	negs        *[]*globAST
	filledNegs  bool
	// text caches toString; textSet tells an empty cache from none.
	text     []uint16
	textSet  bool
	emptyExt bool
}

// globASTParts is a #parts array. usurp makes two ASTs share one, as minimatch shares the JavaScript array.
type globASTParts struct{ items []globASTPart }

// globASTPart is a string, or an AST when ast is set.
type globASTPart struct {
	str []uint16
	ast *globAST
}

type triBool uint8

const (
	triUnset triBool = iota
	triFalse
	triTrue
)

func (t triBool) or(b bool) triBool {
	if t == triTrue || b {
		return triTrue
	}
	return triFalse
}

// globMaxExtglobRecursion is minimatch's default maxExtglobRecursion.
// upstream: node_modules/minimatch/dist/esm/ast.js:maxExtglobRecursion
const globMaxExtglobRecursion = 2

const globExtglobTypes = "!?+*@"

func isExtglobType(c uint16) bool {
	return c != 0 && c < 0x80 && slices.Contains([]byte(globExtglobTypes), byte(c))
}

// adoptionTypes, adoptionWithSpaceTypes and adoptionAnyTypes are minimatch's adoptionMap, adoptionWithSpaceMap and adoptionAnyMap: the extglob types an extglob of type t adopts, or "" for none. Switches keep them out of package initialization, which every pig start pays.
func adoptionTypes(t uint16) string {
	switch t {
	case '!', '@':
		return "@"
	case '?':
		return "?@"
	case '*':
		return "*+?@"
	case '+':
		return "+@"
	}
	return ""
}

func adoptionWithSpaceTypes(t uint16) string {
	switch t {
	case '!', '@':
		return "?"
	case '+':
		return "?*"
	}
	return ""
}

func adoptionAnyTypes(t uint16) string {
	switch t {
	case '!', '?', '@':
		return "?@"
	case '*':
		return "*+?@"
	case '+':
		return "+@?*"
	}
	return ""
}

// usurpType is minimatch's usurpMap: the type an extglob of type t takes when it usurps a child extglob of type c.
func usurpType(t, c uint16) (uint16, bool) {
	switch t {
	case '!':
		if c == '!' {
			return '@', true
		}
	case '?':
		if c == '*' || c == '+' {
			return '*', true
		}
	case '@':
		switch c {
		case '!', '?', '@', '*', '+':
			return c, true
		}
	case '+':
		if c == '?' || c == '*' {
			return '*', true
		}
	}
	return 0, false
}

// The RegExp source texts toRegExpSource adds, as UTF-16 code units. They are ASCII, so each unit is its byte; constant slices need no work at package initialization.
var (
	startNoTraversal = []uint16{'(', '?', '!', '(', '?', ':', '^', '|', '/', ')', '\\', '.', '\\', '.', '?', '(', '?', ':', '$', '|', '/', ')', ')'} // (?!(?:^|/)\.\.?(?:$|/))
	startNoDot       = []uint16{'(', '?', '!', '\\', '.', ')'}                                                                                       // (?!\.)
	globQmark        = []uint16{'[', '^', '/', ']'}                                                                                                  // [^/]
	globStar         = []uint16{'[', '^', '/', ']', '*', '?'}                                                                                        // [^/]*?
	globStarNoEmpty  = []uint16{'[', '^', '/', ']', '+', '?'}                                                                                        // [^/]+?
)

// jsText is the UTF-16 code units of s.
func jsText(s string) []uint16 { return utf16.Encode([]rune(s)) }

func concatUnits(parts ...[]uint16) []uint16 { return slices.Concat(parts...) }

func newGlobAST(typ uint16, parent *globAST) *globAST {
	a := &globAST{typ: typ, parent: parent, parts: &globASTParts{}}
	if typ != 0 {
		a.hasMagic = triTrue
	}
	a.root = a
	if parent != nil {
		a.root = parent.root
	}
	if a.root == a {
		a.negs = &[]*globAST{}
	} else {
		a.negs = a.root.negs
	}
	if typ == '!' && !a.root.filledNegs {
		*a.negs = append(*a.negs, a)
	}
	if parent != nil {
		a.parentIndex = len(parent.parts.items)
	}
	return a
}

// globASTFromGlob is AST.fromGlob.
func globASTFromGlob(pattern []uint16) *globAST {
	ast := newGlobAST(0, nil)
	parseGlobAST(pattern, ast, 0, 0)
	return ast
}

func (a *globAST) toString() []uint16 {
	if a.textSet {
		return a.text
	}
	var text []uint16
	if a.typ == 0 {
		for _, p := range a.parts.items {
			text = append(text, p.toString()...)
		}
	} else {
		text = append(text, a.typ, '(')
		for i, p := range a.parts.items {
			if i > 0 {
				text = append(text, '|')
			}
			text = append(text, p.toString()...)
		}
		text = append(text, ')')
	}
	a.text, a.textSet = text, true
	return text
}

func (p globASTPart) toString() []uint16 {
	if p.ast != nil {
		return p.ast.toString()
	}
	return p.str
}

func (a *globAST) clearText() { a.text, a.textSet = nil, false }

// fillNegs gives every "!" extglob a copy of the parts that follow it, so that its lookahead covers the rest of the segment.
func (a *globAST) fillNegs() {
	if a.filledNegs {
		return
	}
	a.toString()
	a.filledNegs = true
	for len(*a.negs) > 0 {
		n := (*a.negs)[len(*a.negs)-1]
		*a.negs = (*a.negs)[:len(*a.negs)-1]
		if n.typ != '!' {
			continue
		}
		p, pp := n, n.parent
		for pp != nil {
			for i := p.parentIndex + 1; pp.typ == 0 && i < len(pp.parts.items); i++ {
				for _, part := range n.parts.items {
					part.ast.copyIn(pp.parts.items[i])
				}
			}
			p, pp = pp, pp.parent
		}
	}
}

// push adds parts, without empty strings.
func (a *globAST) push(parts ...globASTPart) {
	for _, p := range parts {
		if p.ast == nil && len(p.str) == 0 {
			continue
		}
		a.parts.items = append(a.parts.items, p)
	}
}

func (a *globAST) isStart() bool {
	if a.root == a {
		return true
	}
	if a.parent == nil || !a.parent.isStart() {
		return false
	}
	if a.parentIndex == 0 {
		return true
	}
	for i := range a.parentIndex {
		if i >= len(a.parent.parts.items) {
			return false
		}
		if pp := a.parent.parts.items[i].ast; pp == nil || pp.typ != '!' {
			return false
		}
	}
	return true
}

func (a *globAST) isEnd() bool {
	if a.root == a {
		return true
	}
	if a.parent != nil && a.parent.typ == '!' {
		return true
	}
	if a.parent == nil || !a.parent.isEnd() {
		return false
	}
	if a.typ == 0 {
		return true
	}
	return a.parentIndex == len(a.parent.parts.items)-1
}

func (a *globAST) copyIn(part globASTPart) {
	if part.ast == nil {
		a.push(part)
		return
	}
	a.push(globASTPart{ast: part.ast.clone(a)})
}

func (a *globAST) clone(parent *globAST) *globAST {
	c := newGlobAST(a.typ, parent)
	for _, p := range a.parts.items {
		c.copyIn(p)
	}
	return c
}

// parseGlobAST is #parseAST: it reads str from pos into ast and returns where it stopped. An extglob type followed by "(" starts an extglob, up to maxExtglobRecursion levels deep or where the enclosing extglob can adopt it; an extglob that does not close is text.
func parseGlobAST(str []uint16, ast *globAST, pos, extDepth int) int {
	escaping, inBrace, braceNeg := false, false, false
	braceStart := -1
	charAt := func(i int) uint16 {
		if i < len(str) {
			return str[i]
		}
		return 0
	}
	// brace reports whether c is inside a class, where extglobs do not start.
	brace := func(c uint16, i int, acc *[]uint16) bool {
		if escaping || c == '\\' {
			escaping = !escaping
			*acc = append(*acc, c)
			return true
		}
		if inBrace {
			if i == braceStart+1 {
				if c == '^' || c == '!' {
					braceNeg = true
				}
			} else if c == ']' && (i != braceStart+2 || !braceNeg) {
				inBrace = false
			}
			*acc = append(*acc, c)
			return true
		}
		if c == '[' {
			inBrace, braceStart, braceNeg = true, i, false
			*acc = append(*acc, c)
			return true
		}
		return false
	}
	if ast.typ == 0 {
		i := pos
		var acc []uint16
		for i < len(str) {
			c := str[i]
			i++
			if brace(c, i, &acc) {
				continue
			}
			if isExtglobType(c) && charAt(i) == '(' && extDepth <= globMaxExtglobRecursion {
				ast.push(globASTPart{str: acc})
				acc = nil
				ext := newGlobAST(c, ast)
				i = parseGlobAST(str, ext, i, extDepth+1)
				ast.push(globASTPart{ast: ext})
				continue
			}
			acc = append(acc, c)
		}
		ast.push(globASTPart{str: acc})
		return i
	}
	i := pos + 1
	part := newGlobAST(0, ast)
	var parts []globASTPart
	var acc []uint16
	for i < len(str) {
		c := str[i]
		i++
		if brace(c, i, &acc) {
			continue
		}
		if isExtglobType(c) && charAt(i) == '(' && (extDepth <= globMaxExtglobRecursion || ast.canAdoptType(c, adoptionAnyTypes)) {
			depthAdd := 1
			if ast.canAdoptType(c, adoptionAnyTypes) {
				depthAdd = 0
			}
			part.push(globASTPart{str: acc})
			acc = nil
			ext := newGlobAST(c, part)
			part.push(globASTPart{ast: ext})
			i = parseGlobAST(str, ext, i, extDepth+depthAdd)
			continue
		}
		if c == '|' {
			part.push(globASTPart{str: acc})
			acc = nil
			parts = append(parts, globASTPart{ast: part})
			part = newGlobAST(0, ast)
			continue
		}
		if c == ')' {
			if len(acc) == 0 && len(ast.parts.items) == 0 {
				ast.emptyExt = true
			}
			part.push(globASTPart{str: acc})
			ast.push(append(parts, globASTPart{ast: part})...)
			return i
		}
		acc = append(acc, c)
	}
	ast.typ = 0
	ast.hasMagic = triUnset
	ast.parts = &globASTParts{items: []globASTPart{{str: slices.Clone(str[pos-1:])}}}
	return i
}

func (a *globAST) canAdopt(child *globAST, adoption func(uint16) string) bool {
	if child == nil || child.typ != 0 || len(child.parts.items) != 1 || a.typ == 0 {
		return false
	}
	gc := child.parts.items[0].ast
	if gc == nil || gc.typ == 0 {
		return false
	}
	return a.canAdoptType(gc.typ, adoption)
}

func (a *globAST) canAdoptType(c uint16, adoption func(uint16) string) bool {
	types := adoption(a.typ)
	return types != "" && c < 0x80 && strings.IndexByte(types, byte(c)) >= 0
}

func (a *globAST) adoptWithSpace(child *globAST, index int) {
	gc := child.parts.items[0].ast
	blank := newGlobAST(0, gc)
	blank.parts.items = append(blank.parts.items, globASTPart{str: []uint16{}})
	gc.push(globASTPart{ast: blank})
	a.adopt(child, index)
}

func (a *globAST) adopt(child *globAST, index int) {
	gc := child.parts.items[0].ast
	a.parts.items = slices.Concat(a.parts.items[:index], gc.parts.items, a.parts.items[index+1:])
	for _, p := range gc.parts.items {
		if p.ast != nil {
			p.ast.parent = a
		}
	}
	a.clearText()
}

func (a *globAST) canUsurp(child *globAST) bool {
	if child == nil || child.typ != 0 || len(child.parts.items) != 1 || a.typ == 0 || len(a.parts.items) != 1 {
		return false
	}
	gc := child.parts.items[0].ast
	if gc == nil || gc.typ == 0 {
		return false
	}
	_, ok := usurpType(a.typ, gc.typ)
	return ok
}

func (a *globAST) usurp(child *globAST) {
	gc := child.parts.items[0].ast
	nt, ok := usurpType(a.typ, gc.typ)
	if !ok {
		return
	}
	a.parts = gc.parts
	for _, p := range a.parts.items {
		if p.ast != nil {
			p.ast.parent = a
		}
	}
	a.typ = nt
	a.clearText()
	a.emptyExt = false
}

// flatten merges an extglob's single-extglob branches into it where minimatch's adoption and usurp maps allow, for at most 10 rounds.
func (a *globAST) flatten() {
	if !isExtglobType(a.typ) {
		for _, p := range a.parts.items {
			if p.ast != nil {
				p.ast.flatten()
			}
		}
	} else {
		for iterations := 1; ; iterations++ {
			done := true
			for i := 0; i < len(a.parts.items); i++ {
				c := a.parts.items[i].ast
				if c == nil {
					continue
				}
				c.flatten()
				switch {
				case a.canAdopt(c, adoptionTypes):
					done = false
					a.adopt(c, i)
				case a.canAdopt(c, adoptionWithSpaceTypes):
					done = false
					a.adoptWithSpace(c, i)
				case a.canUsurp(c):
					done = false
					a.usurp(c)
				}
			}
			if done || iterations >= 10 {
				break
			}
		}
	}
	a.clearText()
}

// globSource is what toRegExpSource returns: the RegExp source, the unescaped text, whether there is magic, and whether the source needs the u flag.
type globSource struct {
	re       []uint16
	body     []uint16
	hasMagic bool
	uflag    bool
}

var addPatternStart = []uint16{'[', '.'}

// toRegExpSource is AST.toRegExpSource. allowDot is nil when the caller passes none.
func (a *globAST) toRegExpSource(allowDot *bool) globSource {
	dot := allowDot != nil && *allowDot
	allowDotSet := dot
	if a.root == a {
		a.flatten()
		a.fillNegs()
	}
	if !isExtglobType(a.typ) {
		noEmpty := a.isStart() && a.isEnd() && !slices.ContainsFunc(a.parts.items, func(p globASTPart) bool { return p.ast != nil })
		var src []uint16
		for _, p := range a.parts.items {
			var s globSource
			if p.ast == nil {
				s = parseGlobText(p.str, a.hasMagic == triTrue, noEmpty)
			} else {
				s = p.ast.toRegExpSource(allowDot)
			}
			a.hasMagic = a.hasMagic.or(s.hasMagic)
			a.uflag = a.uflag || s.uflag
			src = append(src, s.re...)
		}
		var start []uint16
		if a.isStart() && len(a.parts.items) > 0 && a.parts.items[0].ast == nil {
			first := a.parts.items[0].str
			dotTravAllowed := len(a.parts.items) == 1 && (slices.Equal(first, jsText(".")) || slices.Equal(first, jsText("..")))
			if !dotTravAllowed {
				startsWith := func(prefix string, then int) bool {
					p := jsText(prefix)
					return len(src) > then && hasUnits(src, p) && slices.Contains(addPatternStart, src[then])
				}
				needNoTrav := dot && len(src) > 0 && slices.Contains(addPatternStart, src[0]) || startsWith(`\.`, 2) || startsWith(`\.\.`, 4)
				needNoDot := !dot && !allowDotSet && len(src) > 0 && slices.Contains(addPatternStart, src[0])
				switch {
				case needNoTrav:
					start = startNoTraversal
				case needNoDot:
					start = startNoDot
				}
			}
		}
		var end []uint16
		if a.isEnd() && a.root.filledNegs && a.parent != nil && a.parent.typ == '!' {
			end = jsText(`(?:$|\/)`)
		}
		a.hasMagic = a.hasMagic.or(false)
		return globSource{re: concatUnits(start, src, end), body: unescapeGlob(src), hasMagic: a.hasMagic == triTrue, uflag: a.uflag}
	}
	repeated := a.typ == '*' || a.typ == '+'
	open := jsText("(?:")
	if a.typ == '!' {
		open = jsText("(?:(?!(?:")
	}
	body := a.partsToRegExp(dot)
	if a.isStart() && a.isEnd() && len(body) == 0 && a.typ != '!' {
		s := a.toString()
		a.parts = &globASTParts{items: []globASTPart{{str: s}}}
		a.typ = 0
		a.hasMagic = triUnset
		return globSource{re: s, body: unescapeGlob(a.toString())}
	}
	var bodyDotAllowed []uint16
	if repeated && !allowDotSet && !dot {
		bodyDotAllowed = a.partsToRegExp(true)
	}
	if slices.Equal(bodyDotAllowed, body) {
		bodyDotAllowed = nil
	}
	if len(bodyDotAllowed) > 0 {
		body = concatUnits(jsText("(?:"), body, jsText(")(?:"), bodyDotAllowed, jsText(")*?"))
	}
	var final []uint16
	if a.typ == '!' && a.emptyExt {
		if a.isStart() && !dot {
			final = append(final, startNoDot...)
		}
		final = append(final, globStarNoEmpty...)
	} else {
		var closing []uint16
		switch {
		case a.typ == '!':
			closing = jsText("))")
			if a.isStart() && !dot && !allowDotSet {
				closing = append(closing, startNoDot...)
			}
			closing = concatUnits(closing, globStar, jsText(")"))
		case a.typ == '@':
			closing = jsText(")")
		case a.typ == '?':
			closing = jsText(")?")
		case a.typ == '+' && len(bodyDotAllowed) > 0:
			closing = jsText(")")
		case a.typ == '*' && len(bodyDotAllowed) > 0:
			closing = jsText(")?")
		default:
			closing = []uint16{')', a.typ}
		}
		final = concatUnits(open, body, closing)
	}
	a.hasMagic = a.hasMagic.or(false)
	return globSource{re: final, body: unescapeGlob(body), hasMagic: a.hasMagic == triTrue, uflag: a.uflag}
}

func (a *globAST) partsToRegExp(dot bool) []uint16 {
	var out []uint16
	first := true
	for _, p := range a.parts.items {
		s := p.ast.toRegExpSource(&dot)
		a.uflag = a.uflag || s.uflag
		if a.isStart() && a.isEnd() && len(s.re) == 0 {
			continue
		}
		if !first {
			out = append(out, '|')
		}
		out = append(out, s.re...)
		first = false
	}
	return out
}

func hasUnits(units, prefix []uint16) bool {
	return len(units) >= len(prefix) && slices.Equal(units[:len(prefix)], prefix)
}

// globReSpecials are the characters that #parseGlob escapes after a backslash.
var globReSpecials = []uint16{'(', ')', '.', '*', '{', '}', '+', '?', '[', ']', '^', '$', '\\', '!'}

// parseGlobText is AST.#parseGlob for a string part.
func parseGlobText(glob []uint16, hasMagic, noEmpty bool) globSource {
	escaping, uflag, inStar := false, false, false
	var re []uint16
	allStars := len(glob) > 0 && !slices.ContainsFunc(glob, func(c uint16) bool { return c != '*' })
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		if escaping {
			escaping = false
			if slices.Contains(globReSpecials, c) {
				re = append(re, '\\')
			}
			re = append(re, c)
			continue
		}
		if c == '*' {
			if inStar {
				continue
			}
			inStar = true
			if noEmpty && allStars {
				re = append(re, globStarNoEmpty...)
			} else {
				re = append(re, globStar...)
			}
			hasMagic = true
			continue
		}
		inStar = false
		if c == '\\' {
			if i == len(glob)-1 {
				re = append(re, '\\', '\\')
			} else {
				escaping = true
			}
			continue
		}
		if c == '[' {
			if src, needUflag, consumed, magic := parseGlobClass(glob, i); consumed > 0 {
				re = append(re, src...)
				uflag = uflag || needUflag
				i += consumed - 1
				hasMagic = hasMagic || magic
				continue
			}
		}
		if c == '?' {
			re = append(re, globQmark...)
			hasMagic = true
			continue
		}
		re = append(re, regExpEscape([]uint16{c})...)
	}
	return globSource{re: re, body: unescapeGlob(glob), hasMagic: hasMagic, uflag: uflag}
}

// regExpEscape is minimatch's regExpEscape: a backslash before each of -[]{}()*+?.,\^$|# and whitespace.
func regExpEscape(s []uint16) []uint16 {
	out := make([]uint16, 0, len(s))
	for _, c := range s {
		if isRegExpSpecial(c) || isJSWhitespace(rune(c)) {
			out = append(out, '\\')
		}
		out = append(out, c)
	}
	return out
}

func isRegExpSpecial(c uint16) bool {
	return c < 0x80 && slices.Contains([]byte(`-[]{}()*+?.,\^$|#`), byte(c))
}

// isJSWhitespace is \s of a JavaScript RegExp: WhiteSpace and LineTerminator.
func isJSWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A || unicode.Is(unicode.Zs, r)
}

// braceEscape is brace-expressions' braceEscape: a backslash before each of []\-.
func braceEscape(s []uint16) []uint16 {
	out := make([]uint16, 0, len(s))
	for _, c := range s {
		if c == '[' || c == ']' || c == '\\' || c == '-' {
			out = append(out, '\\')
		}
		out = append(out, c)
	}
	return out
}

// posixGlobClasses are minimatch's POSIX classes, in its order: the RegExp class text each stands for, whether it needs the u flag, and whether it is a negated set.
var posixGlobClasses = []struct {
	name, source string
	unicode      bool
	negated      bool
}{
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

// parseGlobClass is minimatch's parseClass for the class at glob[pos]: the RegExp source, whether it needs the u flag, the units it uses (0 when it does not close), and whether it is magic. One member that is not negated is that character, escaped; a class that cannot match is "$.".
func parseGlobClass(glob []uint16, pos int) (src []uint16, uflag bool, consumed int, magic bool) {
	var ranges, negs [][]uint16
	i := pos + 1
	sawStart, escaping, negate := false, false, false
	endPos := pos
	rangeStart := -1
	startsWith := func(s string, at int) bool { return at <= len(glob) && hasUnits(glob[at:], jsText(s)) }
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
		if c == '\\' && !escaping {
			escaping = true
			i++
			continue
		}
		if c == '[' && !escaping {
			for _, posix := range posixGlobClasses {
				if startsWith(posix.name, i) {
					if rangeStart >= 0 {
						return jsText("$."), false, len(glob) - pos, true
					}
					i += len(posix.name)
					if posix.negated {
						negs = append(negs, jsText(posix.source))
					} else {
						ranges = append(ranges, jsText(posix.source))
					}
					uflag = uflag || posix.unicode
					continue loop
				}
			}
		}
		escaping = false
		switch {
		case rangeStart >= 0:
			if int(c) > rangeStart {
				ranges = append(ranges, concatUnits(braceEscape([]uint16{uint16(rangeStart)}), []uint16{'-'}, braceEscape([]uint16{c})))
			} else if int(c) == rangeStart {
				ranges = append(ranges, braceEscape([]uint16{c}))
			}
			rangeStart = -1
			i++
		case startsWith("-]", i+1):
			ranges = append(ranges, braceEscape([]uint16{c, '-'}))
			i += 2
		case startsWith("-", i+1):
			rangeStart = int(c)
			i += 2
		default:
			ranges = append(ranges, braceEscape([]uint16{c}))
			i++
		}
	}
	if endPos < i {
		return nil, false, 0, false
	}
	if len(ranges) == 0 && len(negs) == 0 {
		return jsText("$."), false, len(glob) - pos, true
	}
	if len(negs) == 0 && len(ranges) == 1 && isOneCharacter(ranges[0]) && !negate {
		r := ranges[0]
		if len(r) == 2 {
			r = r[1:]
		}
		return regExpEscape(r), false, endPos - pos, false
	}
	sranges := concatUnits([]uint16{'['}, caretIf(negate), slices.Concat(ranges...), []uint16{']'})
	snegs := concatUnits([]uint16{'['}, caretIf(!negate), slices.Concat(negs...), []uint16{']'})
	switch {
	case len(ranges) > 0 && len(negs) > 0:
		src = concatUnits([]uint16{'('}, sranges, []uint16{'|'}, snegs, []uint16{')'})
	case len(ranges) > 0:
		src = sranges
	default:
		src = snegs
	}
	return src, uflag, endPos - pos, true
}

func caretIf(b bool) []uint16 {
	if b {
		return []uint16{'^'}
	}
	return nil
}

// isOneCharacter is /^\\?.$/.test(s): one unit, or a backslash and one unit, that is not a line terminator.
func isOneCharacter(s []uint16) bool {
	if len(s) == 2 && s[0] == '\\' {
		s = s[1:]
	}
	return len(s) == 1 && !isLineTerminator(rune(s[0]))
}

// unescapeGlob is minimatch's unescape with its defaults: a one-character class that does not follow a backslash is its character, and then a backslash before a character other than "/" is dropped.
func unescapeGlob(s []uint16) []uint16 {
	var once []uint16
	for i := 0; i < len(s); {
		// ((?!\\).|^)\[([^/\\])\] at i: a character that is not a backslash or a line terminator, or the start, then "[", one unit that is not "/" or "\", and "]".
		classAt := func(j int) bool {
			return j+2 < len(s) && s[j] == '[' && s[j+1] != '/' && s[j+1] != '\\' && s[j+2] == ']'
		}
		if s[i] != '\\' && !isLineTerminator(rune(s[i])) && classAt(i+1) {
			once = append(once, s[i], s[i+2])
			i += 4
			continue
		}
		if i == 0 && classAt(0) {
			once = append(once, s[1])
			i += 3
			continue
		}
		once = append(once, s[i])
		i++
	}
	out := make([]uint16, 0, len(once))
	for i := 0; i < len(once); i++ {
		if once[i] == '\\' && i+1 < len(once) && once[i+1] != '/' {
			i++
		}
		out = append(out, once[i])
	}
	return out
}
