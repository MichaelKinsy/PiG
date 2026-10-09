package minimatch

// Ports minimatch 10.2.6 ast.js: the parse of one path portion into literal text, character classes, wildcards and extglobs, and its
// conversion to a regular expression source.

import "strings"

const (
	startNoTraversal = `(?!(?:^|/)\.\.?(?:\z|/))`
	startNoDot       = `(?!\.)`
	qmark            = `[^/]`
	star             = qmark + `*?`
	starNoEmpty      = qmark + `+?`
)

func isExtglobType(c byte) bool {
	switch c {
	case '!', '?', '+', '*', '@':
		return true
	}
	return false
}

var (
	adoptionMap = map[string]string{"!": "@", "?": "?@", "@": "@", "*": "*+?@", "+": "+@"}
	// adoptionWithSpaceMap lists the nested types that can be adopted with an added blank element.
	adoptionWithSpaceMap = map[string]string{"!": "?", "@": "?", "+": "?*"}
	adoptionAnyMap       = map[string]string{"!": "?@", "?": "?@", "@": "?@", "*": "*+?@", "+": "+@?*"}
	usurpMap             = map[string]map[string]string{
		"!": {"!": "@"},
		"?": {"*": "*", "+": "*"},
		"@": {"!": "!", "?": "?", "@": "@", "*": "*", "+": "+"},
		"+": {"?": "*", "*": "*"},
	}
)

// astOptions are the options of a glob parse.
type astOptions struct {
	dot, nocase bool
}

// node is an AST node: a sequence of parts (text or nested nodes), or an extglob (typ != "") whose parts are the alternatives.
type node struct {
	typ         string
	root        *node
	hasMagic    *bool
	uflag       bool
	parts       []any // string or *node
	parent      *node
	parentIndex int
	negs        *[]*node
	filledNegs  bool
	options     astOptions
	toStringVal *string
	emptyExt    bool
}

func newNode(typ string, parent *node, options astOptions) *node {
	n := &node{typ: typ, parent: parent}
	if typ != "" {
		t := true
		n.hasMagic = &t
	}
	if parent != nil {
		n.root = parent.root
	} else {
		n.root = n
	}
	if n.root == n {
		n.options = options
		n.negs = new([]*node)
	} else {
		n.options = n.root.options
		n.negs = n.root.negs
	}
	if typ == "!" && !n.root.filledNegs {
		*n.negs = append(*n.negs, n)
	}
	if parent != nil {
		n.parentIndex = len(parent.parts)
	}
	return n
}

func fromGlob(pattern string, options astOptions) *node {
	ast := newNode("", nil, options)
	parseAST(pattern, ast, 0, options, 0)
	return ast
}

func (n *node) String() string {
	if n.toStringVal != nil {
		return *n.toStringVal
	}
	var s string
	if n.typ == "" {
		var b strings.Builder
		for _, p := range n.parts {
			b.WriteString(partString(p))
		}
		s = b.String()
	} else {
		items := make([]string, len(n.parts))
		for i, p := range n.parts {
			items[i] = partString(p)
		}
		s = n.typ + "(" + strings.Join(items, "|") + ")"
	}
	n.toStringVal = &s
	return s
}

func partString(p any) string {
	if s, ok := p.(string); ok {
		return s
	}
	return p.(*node).String()
}

func (n *node) fillNegs() {
	if n.filledNegs {
		return
	}
	_ = n.String()
	n.filledNegs = true
	for len(*n.negs) > 0 {
		last := len(*n.negs) - 1
		neg := (*n.negs)[last]
		*n.negs = (*n.negs)[:last]
		if neg.typ != "!" {
			continue
		}
		p := neg
		pp := p.parent
		for pp != nil {
			for i := p.parentIndex + 1; pp.typ == "" && i < len(pp.parts); i++ {
				for _, part := range neg.parts {
					part.(*node).copyIn(pp.parts[i])
				}
			}
			p = pp
			pp = p.parent
		}
	}
}

func (n *node) push(parts ...any) {
	for _, p := range parts {
		if s, ok := p.(string); ok && s == "" {
			continue
		}
		n.parts = append(n.parts, p)
	}
}

func (n *node) isStart() bool {
	if n.root == n {
		return true
	}
	if n.parent == nil || !n.parent.isStart() {
		return false
	}
	if n.parentIndex == 0 {
		return true
	}
	for i := 0; i < n.parentIndex; i++ {
		if pp, ok := n.parent.parts[i].(*node); !ok || pp.typ != "!" {
			return false
		}
	}
	return true
}

func (n *node) isEnd() bool {
	if n.root == n {
		return true
	}
	if n.parent != nil && n.parent.typ == "!" {
		return true
	}
	if n.parent == nil || !n.parent.isEnd() {
		return false
	}
	if n.typ == "" {
		return n.parent.isEnd()
	}
	return n.parentIndex == len(n.parent.parts)-1
}

func (n *node) copyIn(part any) {
	if s, ok := part.(string); ok {
		n.push(s)
		return
	}
	n.push(part.(*node).clone(n))
}

func (n *node) clone(parent *node) *node {
	c := newNode(n.typ, parent, astOptions{})
	for _, p := range n.parts {
		c.copyIn(p)
	}
	return c
}

func parseAST(str string, ast *node, pos int, opt astOptions, extDepth int) int {
	const maxDepth = 2
	escaping, inBrace := false, false
	braceStart := -1
	braceNeg := false
	if ast.typ == "" {
		i := pos
		var acc strings.Builder
		flush := func() string { s := acc.String(); acc.Reset(); return s }
		for i < len(str) {
			c := str[i]
			i++
			if escaping || c == '\\' {
				escaping = !escaping
				acc.WriteByte(c)
				continue
			}
			if inBrace {
				if i == braceStart+1 {
					if c == '^' || c == '!' {
						braceNeg = true
					}
				} else if c == ']' && (i != braceStart+2 || !braceNeg) {
					inBrace = false
				}
				acc.WriteByte(c)
				continue
			} else if c == '[' {
				inBrace = true
				braceStart = i
				braceNeg = false
				acc.WriteByte(c)
				continue
			}
			if isExtglobType(c) && i < len(str) && str[i] == '(' && extDepth <= maxDepth {
				ast.push(flush())
				ext := newNode(string(c), ast, astOptions{})
				i = parseAST(str, ext, i, opt, extDepth+1)
				ast.push(ext)
				continue
			}
			acc.WriteByte(c)
		}
		ast.push(acc.String())
		return i
	}
	// An extglob; pos is at the (.
	i := pos + 1
	part := newNode("", ast, astOptions{})
	var parts []any
	var acc strings.Builder
	flush := func() string { s := acc.String(); acc.Reset(); return s }
	for i < len(str) {
		c := str[i]
		i++
		if escaping || c == '\\' {
			escaping = !escaping
			acc.WriteByte(c)
			continue
		}
		if inBrace {
			if i == braceStart+1 {
				if c == '^' || c == '!' {
					braceNeg = true
				}
			} else if c == ']' && (i != braceStart+2 || !braceNeg) {
				inBrace = false
			}
			acc.WriteByte(c)
			continue
		} else if c == '[' {
			inBrace = true
			braceStart = i
			braceNeg = false
			acc.WriteByte(c)
			continue
		}
		if isExtglobType(c) && i < len(str) && str[i] == '(' && (extDepth <= maxDepth || ast.canAdoptType(string(c), adoptionAnyMap)) {
			depthAdd := 1
			if ast.canAdoptType(string(c), adoptionAnyMap) {
				depthAdd = 0
			}
			part.push(flush())
			ext := newNode(string(c), part, astOptions{})
			part.push(ext)
			i = parseAST(str, ext, i, opt, extDepth+depthAdd)
			continue
		}
		if c == '|' {
			part.push(flush())
			parts = append(parts, part)
			part = newNode("", ast, astOptions{})
			continue
		}
		if c == ')' {
			if acc.Len() == 0 && len(ast.parts) == 0 {
				ast.emptyExt = true
			}
			part.push(flush())
			parts = append(parts, part)
			ast.push(parts...)
			return i
		}
		acc.WriteByte(c)
	}
	// An unfinished extglob is not an extglob: the rest is literal text.
	ast.typ = ""
	ast.hasMagic = nil
	ast.parts = []any{str[pos-1:]}
	return i
}

func (n *node) canAdoptWithSpace(child any) bool { return n.canAdopt(child, adoptionWithSpaceMap) }

func (n *node) canAdopt(child any, m map[string]string) bool {
	c, ok := child.(*node)
	if !ok || c.typ != "" || len(c.parts) != 1 || n.typ == "" {
		return false
	}
	gc, ok := c.parts[0].(*node)
	if !ok || gc.typ == "" {
		return false
	}
	return n.canAdoptType(gc.typ, m)
}

func (n *node) canAdoptType(c string, m map[string]string) bool {
	return strings.Contains(m[n.typ], c)
}

func (n *node) adoptWithSpace(child *node, index int) {
	gc := child.parts[0].(*node)
	blank := newNode("", gc, n.options)
	blank.parts = append(blank.parts, "")
	gc.push(blank)
	n.adopt(child, index)
}

func (n *node) adopt(child *node, index int) {
	gc := child.parts[0].(*node)
	parts := append([]any(nil), n.parts[:index]...)
	parts = append(parts, gc.parts...)
	parts = append(parts, n.parts[index+1:]...)
	n.parts = parts
	for _, p := range gc.parts {
		if pn, ok := p.(*node); ok {
			pn.parent = n
		}
	}
	n.toStringVal = nil
}

func (n *node) canUsurp(child any) bool {
	c, ok := child.(*node)
	if !ok || c.typ != "" || len(c.parts) != 1 || n.typ == "" || len(n.parts) != 1 {
		return false
	}
	gc, ok := c.parts[0].(*node)
	if !ok || gc.typ == "" {
		return false
	}
	_, ok = usurpMap[n.typ][gc.typ]
	return ok
}

func (n *node) usurp(child *node) {
	gc := child.parts[0].(*node)
	nt, ok := usurpMap[n.typ][gc.typ]
	if !ok {
		return
	}
	n.parts = gc.parts
	for _, p := range n.parts {
		if pn, ok := p.(*node); ok {
			pn.parent = n
		}
	}
	n.typ = nt
	n.toStringVal = nil
	n.emptyExt = false
}

// mmPattern is a compiled path portion: literal text, or a regular expression.
type mmPattern struct {
	literal string
	re      *regexSource
}

// toMMPattern returns the unescaped text when the glob has no magic, and the regular expression otherwise.
func (n *node) toMMPattern() mmPattern {
	if n != n.root {
		return n.root.toMMPattern()
	}
	glob := n.String()
	re, body, hasMagic, uflag := n.toRegExpSource(nil)
	anyMagic := hasMagic || (n.hasMagic != nil && *n.hasMagic) || (n.options.nocase && jsUpper(glob) != jsLower(glob))
	if !anyMagic {
		return mmPattern{literal: body}
	}
	return mmPattern{re: compileSource(re, n.options.nocase, uflag)}
}

func jsUpper(s string) string { return strings.ToUpper(s) }
func jsLower(s string) string { return strings.ToLower(s) }

func (n *node) toRegExpSource(allowDot *bool) (string, string, bool, bool) {
	dot := n.options.dot
	if allowDot != nil {
		dot = *allowDot
	}
	if n.root == n {
		n.flatten()
		n.fillNegs()
	}
	if n.typ == "" {
		noEmpty := n.isStart() && n.isEnd()
		for _, p := range n.parts {
			if _, isStr := p.(string); !isStr {
				noEmpty = false
			}
		}
		var src strings.Builder
		for _, p := range n.parts {
			var re string
			var magic, u bool
			if s, ok := p.(string); ok {
				var hm bool
				if n.hasMagic != nil {
					hm = *n.hasMagic
				}
				re, _, magic, u = parseGlob(s, hm, noEmpty, n.options.nocase)
			} else {
				re, _, magic, u = p.(*node).toRegExpSource(allowDot)
			}
			t := (n.hasMagic != nil && *n.hasMagic) || magic
			n.hasMagic = &t
			n.uflag = n.uflag || u
			src.WriteString(re)
		}
		source := src.String()
		start := ""
		if n.isStart() {
			if len(n.parts) == 0 {
				// An empty node has no first part to protect.
			} else if first, ok := n.parts[0].(string); ok {
				dotTravAllowed := len(n.parts) == 1 && (first == ".." || first == ".")
				if !dotTravAllowed {
					aps := func(c byte) bool { return c == '[' || c == '.' }
					charAt := func(i int) byte {
						if i < len(source) {
							return source[i]
						}
						return 0
					}
					needNoTrav := (dot && aps(charAt(0))) || (strings.HasPrefix(source, `\.`) && aps(charAt(2))) || (strings.HasPrefix(source, `\.\.`) && aps(charAt(4)))
					needNoDot := !dot && (allowDot == nil || !*allowDot) && aps(charAt(0))
					switch {
					case needNoTrav:
						start = startNoTraversal
					case needNoDot:
						start = startNoDot
					}
				}
			}
		}
		end := ""
		if n.isEnd() && n.root.filledNegs && n.parent != nil && n.parent.typ == "!" {
			end = `(?:\z|\/)`
		}
		magic := n.hasMagic != nil && *n.hasMagic
		t := magic
		n.hasMagic = &t
		return start + source + end, unescape(source), t, n.uflag
	}
	repeated := n.typ == "*" || n.typ == "+"
	start := "(?:"
	if n.typ == "!" {
		start = "(?:(?!(?:"
	}
	body := n.partsToRegExp(dot)
	if n.isStart() && n.isEnd() && body == "" && n.typ != "!" {
		s := n.String()
		n.parts = []any{s}
		n.typ = ""
		n.hasMagic = nil
		return s, unescape(s), false, false
	}
	bodyDotAllowed := ""
	if repeated && (allowDot == nil || !*allowDot) && !dot {
		bodyDotAllowed = n.partsToRegExp(true)
	}
	if bodyDotAllowed == body {
		bodyDotAllowed = ""
	}
	if bodyDotAllowed != "" {
		body = "(?:" + body + ")(?:" + bodyDotAllowed + ")*?"
	}
	var final string
	if n.typ == "!" && n.emptyExt {
		prefix := ""
		if n.isStart() && !dot {
			prefix = startNoDot
		}
		final = prefix + starNoEmpty
	} else {
		var closing string
		switch {
		case n.typ == "!":
			guard := ""
			if n.isStart() && !dot && (allowDot == nil || !*allowDot) {
				guard = startNoDot
			}
			closing = "))" + guard + star + ")"
		case n.typ == "@":
			closing = ")"
		case n.typ == "?":
			closing = ")?"
		case n.typ == "+" && bodyDotAllowed != "":
			closing = ")"
		case n.typ == "*" && bodyDotAllowed != "":
			closing = ")?"
		default:
			closing = ")" + n.typ
		}
		final = start + body + closing
	}
	t := n.hasMagic != nil && *n.hasMagic
	n.hasMagic = &t
	return final, unescape(body), t, n.uflag
}

func (n *node) flatten() {
	if n.typ == "" {
		for _, p := range n.parts {
			if child, ok := p.(*node); ok {
				child.flatten()
			}
		}
	} else {
		iterations := 0
		for done := false; !done && iterations < 10; iterations++ {
			done = true
			for i := 0; i < len(n.parts); i++ {
				child, ok := n.parts[i].(*node)
				if !ok {
					continue
				}
				child.flatten()
				switch {
				case n.canAdopt(child, adoptionMap):
					done = false
					n.adopt(child, i)
				case n.canAdoptWithSpace(child):
					done = false
					n.adoptWithSpace(child, i)
				case n.canUsurp(child):
					done = false
					n.usurp(child)
				}
			}
		}
	}
	n.toStringVal = nil
}

func (n *node) partsToRegExp(dot bool) string {
	var res []string
	for _, p := range n.parts {
		re, _, _, u := p.(*node).toRegExpSource(&dot)
		n.uflag = n.uflag || u
		if (n.isStart() && n.isEnd()) && re == "" {
			continue
		}
		res = append(res, re)
	}
	return strings.Join(res, "|")
}

var reSpecials = "().*{}+?[]^$\\!"

// parseGlob converts literal text with wildcards and classes to a regular expression source.
func parseGlob(glob string, hasMagic, noEmpty, nocase bool) (string, string, bool, bool) {
	escaping := false
	var re strings.Builder
	uflag := false
	inStar := false
	runes := []rune(glob)
	allStars := glob != "" && strings.Trim(glob, "*") == ""
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if escaping {
			escaping = false
			if strings.ContainsRune(reSpecials, c) {
				re.WriteByte('\\')
			}
			re.WriteRune(c)
			continue
		}
		if c == '*' {
			if inStar {
				continue
			}
			inStar = true
			if noEmpty && allStars {
				re.WriteString(starNoEmpty)
			} else {
				re.WriteString(star)
			}
			hasMagic = true
			continue
		}
		inStar = false
		if c == '\\' {
			if i == len(runes)-1 {
				re.WriteString(`\\`)
			} else {
				escaping = true
			}
			continue
		}
		if c == '[' {
			src, needU, consumed, magic := parseClass(runes, i)
			if consumed != 0 {
				re.WriteString(src)
				uflag = uflag || needU
				i += consumed - 1
				hasMagic = hasMagic || magic
				continue
			}
		}
		if c == '?' {
			re.WriteString(qmark)
			hasMagic = true
			continue
		}
		re.WriteString(literalChar(c, nocase))
	}
	return re.String(), unescape(glob), hasMagic, uflag
}
