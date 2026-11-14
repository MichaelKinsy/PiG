// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import "strings"

// `graph` / `flowchart`: node chains with inline shapes and labelled links,
// plus `subgraph` grouping. Ported from lovely-mermaid diagrams/flowchart.ts.
//
// The grammar is lenient, inherited from upstream and mermaid.js itself: a
// statement contributes whatever prefix parsed and the rest is dropped,
// recorded in graph.warnings.

var flowchartHeaders = []string{"graph", "flowchart"}

func flowchartDiagram() *diagram {
	return &diagram{
		kind:    "flowchart",
		headers: flowchartHeaders,
		render: func(src string) (*canvas, drawnMeta, bool) {
			g := parseGraph(src)
			if g == nil {
				return nil, drawnMeta{}, false
			}
			var c *canvas
			if len(g.groups) == 0 {
				c = layoutFlowchart(g)
			} else {
				c = layoutGrouped(g)
			}
			if c == nil {
				return nil, drawnMeta{}, false
			}
			return c, drawnMeta{warnings: g.warnings, classDefs: g.classDefs}, true
		},
	}
}

func parseGraph(src string) *graph {
	statements := statementsOf(src)
	kind := headerKind(statements)
	if kind == "" || !hasHeader(flowchartHeaders, kind) {
		return nil
	}

	dirTok := "TB"
	if hdr := words(statements[0]); len(hdr) > 1 {
		dirTok = hdr[1]
	}
	g := newGraph(parseDir(dirTok))
	var stack []int
	// `class A,B name` assignments, applied after the walk so a statement may
	// precede the nodes it names. Unknown ids are ignored.
	var classAssignments []classAssign
	// `click A "url"` link targets, applied after the walk like classes.
	var hrefs []hrefAssign

loop:
	for _, st := range statements[1:] {
		switch asciiLower(firstWord(st)) {
		case "subgraph":
			if len(g.groups) >= maxGroups || len(stack) >= maxGroupDepth {
				if g.truncated == "" {
					g.truncated = "subgraph cap (" + itoa(maxGroups) + " groups, depth " + itoa(maxGroupDepth) + ") reached"
				}
				break
			}
			id, label := parseSubgraphDecl(trimJS(st[len("subgraph"):]))
			g.groups = append(g.groups, group{id: id, label: label, parent: lastOrNil(stack)})
			stack = append(stack, len(g.groups)-1)
			g.curGroup = lastOrNil(stack)
			continue
		case "end":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			g.curGroup = lastOrNil(stack)
			continue
		case "classdef":
			if names, props, ok := parseClassDef(st[len(firstWord(st)):]); ok {
				for _, name := range names {
					// `__proto__` is a prototype assignment in the JS record this mirrors, not a class.
					if name != "__proto__" {
						g.classDefs[name] = props
					}
				}
			}
			continue
		case "class":
			if a, ok := parseClassAssign(st[len(firstWord(st)):]); ok {
				classAssignments = append(classAssignments, a)
			}
			continue
		case "click":
			// `click A "url" [tooltip]` / `click A href "url" ...`; the callback
			// forms carry nothing a terminal can invoke.
			if h, ok := parseHref(st[len(firstWord(st)):]); ok {
				hrefs = append(hrefs, h)
			}
			continue
		case "style", "linkstyle", "direction":
			continue
		}
		if g.truncated == "" {
			parseStatement(st, g)
		}
		if g.truncated != "" {
			break loop
		}
	}

	g.applyClasses(classAssignments)
	g.applyHrefs(hrefs)

	if g.truncated != "" {
		g.warnings = append(g.warnings, "diagram truncated: "+g.truncated)
	}
	if len(g.nodes) == 0 {
		return nil
	}
	return g
}

// parseSubgraphDecl reads `subgraph id[Title]`, `subgraph "Title"`, or a bare title.
func parseSubgraphDecl(rest string) (string, string) {
	if strings.HasPrefix(rest, "\"") {
		if close := strings.Index(rest[1:], "\""); close != -1 {
			label := rest[1 : 1+close]
			return label, decodeHtmlEntities(label)
		}
	}
	if before, after, ok := strings.Cut(rest, "["); ok {
		id := trimJS(before)
		label := cleanLabel(trimJS(strings.TrimRight(after, "]")))
		if id != "" && label != "" {
			return id, label
		}
	}
	return rest, rest
}

// parseStatement reads a chain of `node link node ...`, each link fanning over &.
// It parses as far as it can and keeps the prefix, matching upstream and
// mermaid.js; whatever it could not read is recorded in g.warnings.
func parseStatement(st string, g *graph) {
	chars := []rune(st)
	i := 0

	// A parse failure after the cap hit is the cap's fault, not the statement's:
	// the truncation warning already covers it (graph.drop has the same rule).
	headGroup, headNext, ok := parseNodeGroup(chars, i, g)
	if !ok {
		if g.truncated == "" {
			g.warnings = append(g.warnings, "dropped, does not start with a node: \""+st+"\"")
		}
		return
	}
	prev := headGroup
	i = headNext

	for {
		i = skipSpaces(chars, i)
		if i >= len(chars) {
			break
		}
		link := parseLink(chars, i)
		if link == nil {
			if g.truncated == "" {
				g.warnings = append(g.warnings, "dropped, expected a link: \""+string(chars[i:])+"\"")
			}
			break
		}
		i = skipSpaces(chars, link.next)
		targetGroup, targetNext, ok := parseNodeGroup(chars, i, g)
		if !ok {
			if g.truncated == "" {
				g.warnings = append(g.warnings, "dropped, link has no target: \""+st+"\"")
			}
			break
		}
		i = targetNext
		for _, f := range prev {
			for _, t := range targetGroup {
				// `A <-- B` reads right-to-left: swap the endpoints so the arrow
				// that was written on the left becomes a normal forward head.
				reversed := link.left == headArrow && link.right != headArrow
				from, to := f, t
				headTo, headFrom := link.right, link.left
				if reversed {
					from, to = t, f
					headTo, headFrom = headArrow, link.right
				}
				if !g.pushEdge(edge{from: from, to: to, label: link.label, headTo: headTo, headFrom: headFrom, line: link.line}) {
					return
				}
			}
		}
		prev = targetGroup
	}
}

// parseNodeGroup reads one or more nodes joined by &, fanning into a cross product.
func parseNodeGroup(chars []rune, start int, g *graph) (group []int, next int, ok bool) {
	firstIdx, firstNext, ok := parseNode(chars, start, g)
	if !ok {
		return nil, 0, false
	}
	group = []int{firstIdx}
	i := firstNext
	for {
		j := skipSpaces(chars, i)
		if charAt(chars, j) != '&' {
			break
		}
		nextIdx, nextNext, ok := parseNode(chars, j+1, g)
		if !ok {
			return nil, 0, false
		}
		group = append(group, nextIdx)
		i = nextNext
	}
	return group, i, true
}

func skipSpaces(chars []rune, i int) int {
	for i < len(chars) && (chars[i] == ' ' || chars[i] == '\t') {
		i++
	}
	return i
}

func parseNode(chars []rune, start int, g *graph) (index, next int, ok bool) {
	i := skipSpaces(chars, start)
	idStart := i
	// `-` joins the id only when an id char follows, so kebab-case ids parse
	// while `-->` / `-.` / `--` still terminate (mermaid lexes ids greedily).
	for i < len(chars) && (isIdChar(chars[i]) || (chars[i] == '-' && i+1 < len(chars) && isIdChar(chars[i+1]))) {
		i++
	}
	if i == idStart {
		return 0, 0, false
	}
	id := string(chars[idStart:i])

	var sh shaped
	if charAt(chars, i) == '@' && charAt(chars, i+1) == '{' {
		sh = readAtShape(chars, i+2)
	} else {
		sh = readShapeAt(chars, i)
	}
	if sh.unclosed != nil {
		g.warnings = append(g.warnings, "node \""+id+"\": label is missing its closing `"+*sh.unclosed+"`")
	}
	idx, ok := g.nodeIndex(id, sh.label, sh.shape)
	if !ok {
		return 0, 0, false
	}

	// `id:::name` (after any shape) attaches an author class to the node;
	// upstream drops the rest of the line here.
	next = sh.after
	if charAt(chars, next) == ':' && charAt(chars, next+1) == ':' && charAt(chars, next+2) == ':' {
		k := next + 3
		for k < len(chars) && (isIdChar(chars[k]) || chars[k] == '-') {
			k++
		}
		// A name never ends in `-`: back off so `A:::x-->B` keeps its link.
		for k > next+3 && chars[k-1] == '-' {
			k--
		}
		if k > next+3 {
			g.addClass(idx, string(chars[next+3:k]))
			next = k
		}
	}
	return idx, next, true
}

type shaped struct {
	shape shape
	label *string
	after int
	// unclosed is the closing token that was expected but never found.
	unclosed *string
}

// readShapeAt dispatches on the bracket following an id to pick shape and closer.
func readShapeAt(chars []rune, i int) shaped {
	c := charAt(chars, i)
	n := charAt(chars, i+1)
	switch c {
	case '[':
		if n == '[' {
			return readShape(chars, i+2, "]]", shapeRect)
		}
		if n == '(' {
			return readShape(chars, i+2, ")]", shapeRound)
		}
		return readShape(chars, i+1, "]", shapeRect)
	case '(':
		if n == '(' {
			return readShape(chars, i+2, "))", shapeRound)
		}
		if n == '[' {
			return readShape(chars, i+2, "])", shapeRound)
		}
		return readShape(chars, i+1, ")", shapeRound)
	case '{':
		if n == '{' {
			return readShape(chars, i+2, "}}", shapeDiamond)
		}
		return readShape(chars, i+1, "}", shapeDiamond)
	case '>':
		return readShape(chars, i+1, "]", shapeRect)
	}
	return shaped{shape: shapeRect, label: nil, after: i}
}

// readShape reads label text up to closer. Quoting is decided by the first
// non-space character: inside a quoted label the closer is ignored until the
// quote closes, so `A["a] b"]` is one node. An unquoted label ends at the first
// closer, so `A[5" pipe]` keeps its quote.
func readShape(chars []rune, start int, closer string, sh shape) shaped {
	j := start
	for charAt(chars, j) == ' ' || charAt(chars, j) == '\t' {
		j++
	}
	quoted := charAt(chars, j) == '"'

	i := start
	var text strings.Builder
	inQuotes := false
	for i < len(chars) {
		c := chars[i]
		if quoted && c == '"' {
			inQuotes = !inQuotes
			text.WriteRune(c)
			i++
			continue
		}
		if !inQuotes && runesEqualAt(chars, i, closer) {
			l := cleanLabel(text.String())
			return shaped{shape: sh, label: &l, after: i + len(closer)}
		}
		text.WriteRune(c)
		i++
	}
	// Ran off the end still looking for the closer: everything after the opening
	// bracket became label text, so any link operator in it was swallowed.
	l := cleanLabel(text.String())
	uc := closer
	return shaped{shape: sh, label: &l, after: len(chars), unclosed: &uc}
}

// atShapes are the flowchart v2 shape names that read as something other than
// a plain box. The terminal has three silhouettes; every name not listed here
// means "some kind of box" and maps to rect.
var atShapes = map[string]shape{
	"rounded": shapeRound, "stadium": shapeRound, "pill": shapeRound, "terminal": shapeRound,
	"cyl": shapeRound, "cylinder": shapeRound, "database": shapeRound, "db": shapeRound,
	"circle": shapeRound, "circ": shapeRound, "sm-circ": shapeRound, "small-circle": shapeRound,
	"dbl-circ": shapeRound, "double-circle": shapeRound, "fr-circ": shapeRound, "framed-circle": shapeRound,
	"start": shapeRound, "stop": shapeRound, "event": shapeRound, "delay": shapeRound,
	"cloud": shapeRound, "bang": shapeRound,
	"diam": shapeDiamond, "diamond": shapeDiamond, "decision": shapeDiamond, "question": shapeDiamond,
	"hex": shapeDiamond, "hexagon": shapeDiamond, "prepare": shapeDiamond,
}

// readAtShape reads the v2 node syntax `id@{shape: cyl, label: "..."}`, cursor
// past the `@{`. The body is `key: value` pairs split on top-level commas;
// quoted values may contain commas and `}`. Unknown keys are ignored, unknown
// shapes draw as a plain box. A body that never closes reports itself like any
// unterminated label bracket.
func readAtShape(chars []rune, start int) shaped {
	i := start
	depth := 0
	inQuotes := false
loop:
	for ; i < len(chars); i++ {
		c := chars[i]
		switch {
		case inQuotes:
			if c == '"' {
				inQuotes = false
			}
		case c == '"':
			inQuotes = true
		case c == '{':
			depth++
		case c == '}':
			if depth == 0 {
				break loop
			}
			depth--
		}
	}
	body := string(chars[start:i])
	closed := charAt(chars, i) == '}'

	sh := shapeRect
	var label *string
	for _, pair := range splitTop(body, func(c rune) bool { return c == ',' }) {
		key, value, ok := splitOnce(pair, ":")
		if !ok {
			continue
		}
		switch asciiLower(trimJS(key)) {
		case "shape":
			if s, ok := atShapes[asciiLower(trimJS(value))]; ok {
				sh = s
			} else {
				sh = shapeRect
			}
		case "label":
			label = nonEmpty(cleanLabel(value))
		}
	}
	if closed {
		return shaped{shape: sh, label: label, after: i + 1}
	}
	uc := "}"
	return shaped{shape: sh, label: label, after: len(chars), unclosed: &uc}
}

func isLinkChar(c rune) bool {
	return c == '-' || c == '.' || c == '=' || c == '<' || c == '>'
}

type link struct {
	left  head
	right head
	line  lineKind
	label *string
	next  int
}

// parseLink reads a link operator and its label. Labels come in two forms:
// `-->|text|` and the inline `-- text -->`, the latter only when the first
// operator carried no head.
func parseLink(chars []rune, start int) *link {
	i := skipSpaces(chars, start)
	left := headNone
	// A leading `o`/`x` decorates the tail, but only directly before an operator.
	if (charAt(chars, i) == 'o' || charAt(chars, i) == 'x') &&
		(charAt(chars, i+1) == '-' || charAt(chars, i+1) == '.' || charAt(chars, i+1) == '=') {
		if chars[i] == 'o' {
			left = headCircle
		} else {
			left = headCross
		}
		i++
	}

	opStart := i
	for i < len(chars) && isLinkChar(chars[i]) {
		i++
	}
	if i == opStart {
		return nil
	}
	op1 := string(chars[opStart:i])
	if left == headNone && strings.HasPrefix(op1, "<") {
		left = headArrow
	}

	line := lineKindOf(op1)
	right := headNone
	if strings.Contains(op1, ">") {
		right = headArrow
	}
	if right == headNone {
		if h, next, ok := trailingHead(chars, i); ok {
			right = h
			i = next
		}
	}

	if charAt(chars, i) == '|' {
		i++
		lStart := i
		// A quoted stretch keeps its `|`s as text: `-->|"a|b"|`.
		inQuotes := false
		for i < len(chars) && (inQuotes || chars[i] != '|') {
			if chars[i] == '"' {
				inQuotes = !inQuotes
			}
			i++
		}
		label := cleanLabel(string(chars[lStart:i]))
		if charAt(chars, i) == '|' {
			i++
		}
		return &link{left: left, right: right, line: line, label: nonEmpty(label), next: i}
	}

	if right == headNone {
		textStart := skipSpaces(chars, i)
		j := textStart
		// The label runs to the closing operator, which always starts with two
		// link chars (`--`, `-.`, `==`): a lone `=`/`.`/`-` is label text, and a
		// quoted stretch is label text throughout (`A --"a=b"--> B`).
	scan:
		for j < len(chars) {
			switch {
			case chars[j] == '"':
				j++
				for j < len(chars) && chars[j] != '"' {
					j++
				}
				if j < len(chars) {
					j++
				}
			case isLinkChar(chars[j]) && isLinkChar(charAt(chars, j+1)):
				break scan
			default:
				j++
			}
		}
		if j < len(chars) && j > textStart && chars[j] != '<' {
			text := string(chars[textStart:j])
			op2Start := j
			for j < len(chars) && isLinkChar(chars[j]) {
				j++
			}
			op2 := string(chars[op2Start:j])
			if strings.Contains(op2, ">") {
				right = headArrow
			} else if h, next, ok := trailingHead(chars, j); ok {
				right = h
				j = next
			}
			if line == lineSolid {
				line = lineKindOf(op2)
			}
			return &link{left: left, right: right, line: line, label: nonEmpty(cleanLabel(text)), next: j}
		}
	}

	return &link{left: left, right: right, line: line, label: nil, next: i}
}

func lineKindOf(op string) lineKind {
	if strings.Contains(op, "=") {
		return lineThick
	}
	if strings.Contains(op, ".") {
		return lineDotted
	}
	return lineSolid
}

// trailingHead reads a trailing o/x head, only at a statement boundary.
func trailingHead(chars []rune, i int) (head, int, bool) {
	var h head
	switch charAt(chars, i) {
	case 'o':
		h = headCircle
	case 'x':
		h = headCross
	default:
		return "", 0, false
	}
	after := charAt(chars, i+1)
	boundary := after == 0 || after == ' ' || after == '\t' || after == '|' || after == '&' || after == ';'
	if boundary {
		return h, i + 1, true
	}
	return "", 0, false
}
