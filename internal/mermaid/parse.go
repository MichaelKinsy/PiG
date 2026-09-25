// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"strconv"
	"strings"
)

// Source-text-to-model parsing, ported from grok-mermaid parse.ts. Every parseX
// returns ok=false when the source is not that kind of diagram, or when it
// exceeds a cap; render tries each in turn and falls back to a framed copy.

// ---------------------------------------------------------------- whitespace

// isJSSpace matches JavaScript's \s (used by trim, split(/\s+/), /\s/.test).
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return r >= '\u2000' && r <= '\u200a'
}

func trimJS(s string) string      { return strings.TrimFunc(s, isJSSpace) }
func trimStartJS(s string) string { return strings.TrimLeftFunc(s, isJSSpace) }
func trimEndJS(s string) string   { return strings.TrimRightFunc(s, isJSSpace) }

func containsJSSpace(s string) bool { return strings.IndexFunc(s, isJSSpace) >= 0 }

func words(s string) []string { return strings.FieldsFunc(s, isJSSpace) }

func firstWord(s string) string {
	w := words(s)
	if len(w) == 0 {
		return ""
	}
	return w[0]
}

// ---------------------------------------------------------------- statements

func flushStatement(cur string, out *[]string) string {
	trimmed := trimJS(cur)
	if trimmed != "" {
		*out = append(*out, trimmed)
	}
	return ""
}

// splitStatements splits one line into statements on ';', stopping at a %%
// comment. Quoted spans are opaque.
func splitStatements(line string, out *[]string) {
	chars := []rune(line)
	cur := ""
	inQuotes := false
	for i := range chars {
		c := chars[i]
		switch {
		case inQuotes:
			if c == '"' {
				inQuotes = false
			}
			cur += string(c)
		case c == '"':
			inQuotes = true
			cur += string(c)
		case c == '%' && charAt(chars, i+1) == '%':
			flushStatement(cur, out)
			return
		case c == ';':
			cur = flushStatement(cur, out)
		default:
			cur += string(c)
		}
	}
	flushStatement(cur, out)
}

func statementsOf(src string) []string {
	var out []string
	for _, line := range srcLines(src) {
		splitStatements(line, &out)
	}
	return out
}

// splitOnce splits on the first occurrence of sep (Rust's split_once).
func splitOnce(s, sep string) (pre, post string, ok bool) {
	before, after, ok0 := strings.Cut(s, sep)
	if !ok0 {
		return "", "", false
	}
	return before, after, true
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// headerKind returns the diagram kind from the header statement, lowercased.
func headerKind(statements []string) string {
	if len(statements) == 0 {
		return ""
	}
	return asciiLower(firstWord(statements[0]))
}

// diagramKind returns the kind src declares, or "" if its header names no type
// this renderer draws. Reads the header only.
func diagramKind(src string) string {
	kind := headerKind(statementsOf(src))
	switch {
	case kind == "":
		return ""
	case kind == "graph" || kind == "flowchart":
		return "flowchart"
	case strings.HasPrefix(kind, "statediagram"):
		return "state"
	case strings.HasPrefix(kind, "classdiagram"):
		return "class"
	case kind == "erdiagram":
		return "er"
	case kind == "sequencediagram":
		return "sequence"
	}
	return ""
}

// charAt returns chars[i] or 0 (mirrors JS out-of-range undefined in compares).
func charAt(chars []rune, i int) rune {
	if i < 0 || i >= len(chars) {
		return 0
	}
	return chars[i]
}

// runesEqualAt reports whether chars starting at i equals the ASCII token.
func runesEqualAt(chars []rune, i int, token string) bool {
	if i < 0 || i+len(token) > len(chars) {
		return false
	}
	for k := 0; k < len(token); k++ {
		if chars[i+k] != rune(token[k]) {
			return false
		}
	}
	return true
}

func lastOrNil(stack []int) *int {
	if len(stack) == 0 {
		return nil
	}
	v := stack[len(stack)-1]
	return &v
}

// ----------------------------------------------------------------- flowchart

func parseGraph(src string) *graph {
	statements := statementsOf(src)
	kind := headerKind(statements)
	if kind != "graph" && kind != "flowchart" {
		return nil
	}

	hdr := words(statements[0])
	dirTok := "TB"
	if len(hdr) > 1 {
		dirTok = hdr[1]
	}
	g := newGraph(parseDir(dirTok))
	var stack []int

	for _, st := range statements[1:] {
		switch asciiLower(firstWord(st)) {
		case "subgraph":
			if len(g.groups) >= maxGroups || len(stack) >= maxGroupDepth {
				return nil
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
		case "classdef", "class", "style", "linkstyle", "click", "direction":
			continue
		}
		parseStatement(st, g)
		if g.overCap {
			return nil
		}
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
func parseStatement(st string, g *graph) {
	chars := []rune(st)
	i := 0

	headGroup, headNext, ok := parseNodeGroup(chars, i, g)
	if !ok {
		g.warnings = append(g.warnings, "dropped, does not start with a node: \""+st+"\"")
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
			g.warnings = append(g.warnings, "dropped, expected a link: \""+string(chars[i:])+"\"")
			break
		}
		i = skipSpaces(chars, link.next)
		targetGroup, targetNext, ok := parseNodeGroup(chars, i, g)
		if !ok {
			g.warnings = append(g.warnings, "dropped, link has no target: \""+st+"\"")
			break
		}
		i = targetNext
		aborted := false
		for _, f := range prev {
			for _, t := range targetGroup {
				reversed := link.left == headArrow && link.right != headArrow
				from, to := f, t
				headTo, headFrom := link.right, link.left
				if reversed {
					from, to = t, f
					headTo, headFrom = headArrow, link.right
				}
				if !g.pushEdge(edge{from: from, to: to, label: link.label, headTo: headTo, headFrom: headFrom, line: link.line}) {
					aborted = true
				}
			}
			if aborted {
				return
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
	for i < len(chars) && isIdChar(chars[i]) {
		i++
	}
	if i == idStart {
		return 0, 0, false
	}
	id := string(chars[idStart:i])

	sh := readShapeAt(chars, i)
	if sh.unclosed != nil {
		g.warnings = append(g.warnings, "node \""+id+"\": label is missing its closing `"+*sh.unclosed+"`")
	}
	idx, ok := g.nodeIndex(id, sh.label, sh.shape)
	if !ok {
		return 0, 0, false
	}
	return idx, sh.after, true
}

type shaped struct {
	shape    shape
	label    *string
	after    int
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

// readShape reads label text up to closer, honouring quoting decided by the
// first non-space character.
func readShape(chars []rune, start int, closer string, sh shape) shaped {
	j := start
	for charAt(chars, j) == ' ' || charAt(chars, j) == '\t' {
		j++
	}
	quoted := charAt(chars, j) == '"'

	i := start
	text := ""
	inQuotes := false
	for i < len(chars) {
		c := chars[i]
		if quoted && c == '"' {
			inQuotes = !inQuotes
			text += string(c)
			i++
			continue
		}
		if !inQuotes && runesEqualAt(chars, i, closer) {
			l := cleanLabel(text)
			return shaped{shape: sh, label: &l, after: i + len(closer)}
		}
		text += string(c)
		i++
	}
	l := cleanLabel(text)
	uc := closer
	return shaped{shape: sh, label: &l, after: len(chars), unclosed: &uc}
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

// parseLink reads a link operator and its label.
func parseLink(chars []rune, start int) *link {
	i := skipSpaces(chars, start)
	left := headNone
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
		for i < len(chars) && chars[i] != '|' {
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
		for j < len(chars) && !isLinkChar(chars[j]) {
			j++
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

// --------------------------------------------------------------------- state

func parseState(src string) *graph {
	statements := statementsOf(src)
	kind := headerKind(statements)
	if kind == "" || !strings.HasPrefix(kind, "statediagram") {
		return nil
	}

	g := newGraph(dirDown)
	inNote := false

	for _, st := range statements[1:] {
		if inNote {
			if asciiLower(st) == "end note" {
				inNote = false
			}
			continue
		}
		first := asciiLower(firstWord(st))
		switch {
		case first == "direction":
			dt := ""
			if w := words(st); len(w) > 1 {
				dt = w[1]
			}
			g.dir = parseDir(dt)
		case first == "note":
			if !strings.Contains(st, ":") {
				inNote = true
			}
		case first == "state":
			if !parseStateDecl(st, g) {
				return nil
			}
		case first == "classdef" || first == "class" || first == "hide" || first == "scale" || first == "}" || first == "--":
			// styling / composite-state punctuation carry no layout meaning
		case strings.Contains(st, "-->"):
			if !parseTransition(st, g) {
				return nil
			}
		default:
			if !parseStateDesc(st, g) {
				return nil
			}
		}
		if g.overCap {
			return nil
		}
	}

	if len(g.nodes) == 0 {
		return nil
	}
	return g
}

// parseStateDecl handles `state "Label" as id`, `state id <<choice>>`, or `state id {`.
func parseStateDecl(st string, g *graph) bool {
	rest := trimJS(strings.TrimSuffix(trimJS(st[len("state"):]), "{"))
	if rest == "" {
		return true
	}

	if strings.HasPrefix(rest, "\"") {
		close := strings.Index(rest[1:], "\"")
		if close == -1 {
			return false
		}
		close++ // index within rest
		label := rest[1:close]
		after := trimJS(rest[close+1:])
		id := label
		if strings.HasPrefix(after, "as") {
			id = trimJS(after[2:])
		}
		_, ok := g.nodeLabel(id, decodeHtmlEntities(label))
		return ok
	}

	sh := shapeRound
	id := rest
	stereotyped := false
	if before, after, ok := strings.Cut(rest, "<<"); ok {
		stereo := trimJS(strings.TrimSuffix(after, ">>"))
		if stereo == "choice" {
			sh = shapeDiamond
		}
		id = trimJS(before)
		stereotyped = true
	}
	if id == "" || containsJSSpace(id) {
		return false
	}
	var label *string
	if stereotyped {
		label = &id
	}
	_, ok := g.nodeIndex(id, label, sh)
	return ok
}

// parseTransition handles `A --> B: label`, including chains.
func parseTransition(st string, g *graph) bool {
	rest := st
	prev := -1
	havePrev := false

	for {
		lhs, rhs, ok := splitOnce(rest, "-->")
		if !ok {
			break
		}

		fromID := trimJS(strings.TrimRight(trimEndJS(lhs), "-"))
		var from int
		if havePrev {
			if fromID != "" {
				return false
			}
			from = prev
		} else {
			if fromID == "" {
				return false
			}
			f, ok := stateEndpoint(g, fromID, true)
			if !ok {
				return false
			}
			from = f
		}

		nextArrow := strings.Index(rhs, "-->")
		toPartRaw := rhs
		tail := ""
		if nextArrow != -1 {
			toPartRaw = rhs[:nextArrow]
			tail = rhs[nextArrow:]
		}

		toPart := toPartRaw
		var label *string
		if pre, post, ok := splitOnce(toPartRaw, ":"); ok {
			toPart = pre
			label = nonEmpty(decodeHtmlEntities(trimJS(post)))
		}

		toID := trimJS(strings.TrimRight(trimEndJS(strings.TrimLeft(trimStartJS(toPart), ">")), "-"))
		if toID == "" {
			return false
		}
		to, ok := stateEndpoint(g, toID, false)
		if !ok {
			return false
		}

		if !g.pushEdge(edge{from: from, to: to, label: label, headTo: headArrow, headFrom: headNone, line: lineSolid}) {
			return true
		}
		prev = to
		havePrev = true
		rest = tail
	}
	return true
}

// stateEndpoint resolves `[*]` to start/end depending on the arrow side.
func stateEndpoint(g *graph, id string, isSource bool) (int, bool) {
	if id == "[*]" {
		name := "[*]end"
		if isSource {
			name = "[*]start"
		}
		dot := "●"
		return g.nodeIndex(name, &dot, shapeRound)
	}
	return g.nodeIndex(id, nil, shapeRound)
}

// parseStateDesc handles `id: description` or a bare state name.
func parseStateDesc(st string, g *graph) bool {
	if pre, post, ok := splitOnce(st, ":"); ok {
		id := trimJS(pre)
		desc := trimJS(post)
		if id == "" || containsJSSpace(id) || desc == "" {
			return false
		}
		_, ok := g.nodeLabel(id, decodeHtmlEntities(desc))
		return ok
	}
	if containsJSSpace(st) {
		return false
	}
	_, ok := g.nodeIndex(st, nil, shapeRound)
	return ok
}

// --------------------------------------------------------------------- class

// classOp is one relation operator, longest-first so `--|>` wins over `--`.
type classOp struct {
	op       string
	headFrom head
	headTo   head
	line     lineKind
}

var classOps = []classOp{
	{"<|--", headTriangle, headNone, lineSolid},
	{"--|>", headNone, headTriangle, lineSolid},
	{"<|..", headTriangle, headNone, lineDotted},
	{"..|>", headNone, headTriangle, lineDotted},
	{"*--", headDiamondFill, headNone, lineSolid},
	{"--*", headNone, headDiamondFill, lineSolid},
	{"o--", headDiamondOpen, headNone, lineSolid},
	{"--o", headNone, headDiamondOpen, lineSolid},
	{"<--", headArrow, headNone, lineSolid},
	{"-->", headNone, headArrow, lineSolid},
	{"<..", headArrow, headNone, lineDotted},
	{"..>", headNone, headArrow, lineDotted},
	{"--", headNone, headNone, lineSolid},
	{"..", headNone, headNone, lineDotted},
}

const maxClassOp = 4

func parseClass(src string) (*graph, []classInfo, bool) {
	statements := statementsOf(src)
	kind := headerKind(statements)
	if kind == "" || !strings.HasPrefix(kind, "classdiagram") {
		return nil, nil, false
	}

	g := newGraph(dirDown)
	var infos []classInfo
	sync := func() {
		for len(infos) < len(g.nodes) {
			infos = append(infos, emptyClassInfo())
		}
	}
	declare := func(name string) (int, bool) {
		idx, ok := g.nodeIndex(name, nil, shapeRect)
		sync()
		return idx, ok
	}
	curClass := -1

	for _, st := range statements[1:] {
		if curClass != -1 {
			if st == "}" {
				curClass = -1
			} else {
				pushMember(&infos[curClass], st)
			}
			continue
		}

		first := asciiLower(firstWord(st))
		if first == "direction" {
			dt := ""
			if w := words(st); len(w) > 1 {
				dt = w[1]
			}
			g.dir = parseDir(dt)
			continue
		}
		switch first {
		case "note", "callback", "click", "link", "style", "cssclass", "classdef", "namespace", "}":
			continue
		}
		if first == "class" {
			rest := trimJS(st[len("class"):])
			open := strings.HasSuffix(rest, "{")
			name := rest
			if open {
				name = trimJS(rest[:len(rest)-1])
			}
			if name == "" || containsJSSpace(name) {
				return nil, nil, false
			}
			idx, ok := declare(name)
			if !ok {
				return nil, nil, false
			}
			if open {
				curClass = idx
			}
			continue
		}

		if strings.HasPrefix(st, "<<") {
			pre, post, ok := splitOnce(st[2:], ">>")
			if !ok {
				return nil, nil, false
			}
			name := trimJS(post)
			if name == "" || containsJSSpace(name) {
				return nil, nil, false
			}
			idx, ok := declare(name)
			if !ok {
				return nil, nil, false
			}
			ann := trimJS(pre)
			infos[idx].annotation = &ann
			continue
		}

		if rel := parseClassRelation(st); rel != nil {
			f, ok := declare(rel.from)
			if !ok {
				return nil, nil, false
			}
			t, ok := declare(rel.to)
			if !ok {
				return nil, nil, false
			}
			if len(g.edges) >= maxEdges {
				return nil, nil, false
			}
			g.edges = append(g.edges, edge{from: f, to: t, label: rel.label, headTo: rel.headTo, headFrom: rel.headFrom, line: rel.line})
			continue
		}

		if pre, post, ok := splitOnce(st, ":"); ok {
			id := trimJS(pre)
			text := trimJS(post)
			if id == "" || containsJSSpace(id) || text == "" {
				return nil, nil, false
			}
			idx, ok := declare(id)
			if !ok {
				return nil, nil, false
			}
			pushMember(&infos[idx], text)
			continue
		}
		return nil, nil, false
	}

	if len(g.nodes) == 0 {
		return nil, nil, false
	}
	sync()
	return g, infos, true
}

// pushMember adds a member to the attribute or method compartment, eliding past
// the cap.
func pushMember(info *classInfo, raw string) {
	if strings.HasPrefix(raw, "<<") {
		if pre, _, ok := splitOnce(raw[2:], ">>"); ok {
			ann := trimJS(pre)
			info.annotation = &ann
		}
		return
	}
	member := decodeHtmlEntities(displayGenerics(trimJS(raw)))
	if strings.Contains(member, "(") {
		appendMember(&info.methods, member)
	} else {
		appendMember(&info.attrs, member)
	}
}

func appendMember(list *[]string, member string) {
	switch {
	case len(*list) < maxMembers:
		*list = append(*list, member)
	case len(*list) == maxMembers:
		*list = append(*list, "…")
	}
}

type classRelation struct {
	from     string
	to       string
	headFrom head
	headTo   head
	line     lineKind
	label    *string
}

func parseClassRelation(st string) *classRelation {
	chars := []rune(st)
	type foundOp struct {
		pos      int
		op       string
		headFrom head
		headTo   head
		line     lineKind
	}
	var found *foundOp

	for pos := 0; pos < len(chars) && found == nil; pos++ {
		end := min(pos+maxClassOp, len(chars))
		tail := string(chars[pos:end])
		for _, co := range classOps {
			if !strings.HasPrefix(tail, co.op) {
				continue
			}
			if strings.HasPrefix(co.op, "o") && pos > 0 && isIdChar(chars[pos-1]) {
				continue
			}
			after := charAt(chars, pos+len([]rune(co.op)))
			if strings.HasSuffix(co.op, "o") && after != 0 && isIdChar(after) {
				continue
			}
			found = &foundOp{pos: pos, op: co.op, headFrom: co.headFrom, headTo: co.headTo, line: co.line}
			break
		}
	}
	if found == nil {
		return nil
	}

	lhsRaw := trimJS(string(chars[:found.pos]))
	rhsRaw := trimJS(string(chars[found.pos+len([]rune(found.op)):]))

	lhs, cardFrom := stripCardinalitySuffix(lhsRaw)
	rhs, cardTo := stripCardinalityPrefix(rhsRaw)

	toID := rhs
	var relLabel *string
	if pre, post, ok := splitOnce(rhs, ":"); ok {
		toID = pre
		relLabel = nonEmpty(decodeHtmlEntities(trimJS(post)))
	}
	toID = trimJS(toID)

	if lhs == "" || toID == "" || containsJSSpace(lhs) || containsJSSpace(toID) {
		return nil
	}

	rl := ""
	if relLabel != nil {
		rl = *relLabel
	}
	var parts []string
	for _, s := range []string{cardFrom, rl, cardTo} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	label := nonEmpty(strings.Join(parts, " "))
	return &classRelation{from: lhs, to: toID, headFrom: found.headFrom, headTo: found.headTo, line: found.line, label: label}
}

// stripCardinalitySuffix reads `Class "1"`: a quoted cardinality trailing the LHS.
func stripCardinalitySuffix(s string) (string, string) {
	t := trimEndJS(s)
	if strings.HasSuffix(t, "\"") {
		rest := t[:len(t)-1]
		if q := strings.LastIndex(rest, "\""); q != -1 {
			return trimEndJS(rest[:q]), rest[q+1:]
		}
	}
	return t, ""
}

// stripCardinalityPrefix reads `"0..*" Class`: a quoted cardinality leading the RHS.
func stripCardinalityPrefix(s string) (string, string) {
	t := trimStartJS(s)
	if strings.HasPrefix(t, "\"") {
		rest := t[1:]
		if before, after, ok := strings.Cut(rest, "\""); ok {
			return trimStartJS(after), before
		}
	}
	return t, ""
}

// displayGenerics rewrites mermaid `List~T~` as `List<T>`.
func displayGenerics(s string) string {
	var out strings.Builder
	open := false
	for _, c := range s {
		if c == '~' {
			if open {
				out.WriteByte('>')
			} else {
				out.WriteByte('<')
			}
			open = !open
		} else {
			out.WriteRune(c)
		}
	}
	return out.String()
}

// ------------------------------------------------------------------------ ER

func parseEr(src string) (*graph, []classInfo, bool) {
	statements := statementsOf(src)
	if headerKind(statements) != "erdiagram" {
		return nil, nil, false
	}

	g := newGraph(dirDown)
	var infos []classInfo
	curEntity := -1

	for _, st := range statements[1:] {
		if curEntity != -1 {
			if st == "}" {
				curEntity = -1
			} else {
				pushErAttribute(&infos[curEntity], st)
			}
			continue
		}

		if rel, relLabel, ok := splitErRelationship(st); ok {
			tokens := words(rel)
			if len(tokens) != 3 {
				return nil, nil, false
			}
			op, ok := parseErOp(tokens[1])
			if !ok {
				return nil, nil, false
			}
			f, ok := erEntity(g, &infos, tokens[0])
			if !ok {
				return nil, nil, false
			}
			t, ok := erEntity(g, &infos, tokens[2])
			if !ok {
				return nil, nil, false
			}
			if len(g.edges) >= maxEdges {
				return nil, nil, false
			}
			rl := ""
			if relLabel != nil {
				rl = cleanLabel(*relLabel)
			}
			var parts []string
			for _, s := range []string{op.cardL, rl, op.cardR} {
				if s != "" {
					parts = append(parts, s)
				}
			}
			g.edges = append(g.edges, edge{from: f, to: t, label: nonEmpty(strings.Join(parts, " ")), headTo: headNone, headFrom: headNone, line: op.line})
			continue
		}

		open := strings.HasSuffix(st, "{")
		decl := st
		if open {
			decl = trimJS(st[:len(st)-1])
		}
		if decl == "" || len(words(decl)) != 1 {
			return nil, nil, false
		}
		idx, ok := erEntity(g, &infos, decl)
		if !ok {
			return nil, nil, false
		}
		if open {
			curEntity = idx
		}
	}

	if len(g.nodes) == 0 {
		return nil, nil, false
	}
	for len(infos) < len(g.nodes) {
		infos = append(infos, emptyClassInfo())
	}
	return g, infos, true
}

func erEntity(g *graph, infos *[]classInfo, token string) (int, bool) {
	var idx int
	var ok bool
	if before, after, ok0 := strings.Cut(token, "["); ok0 {
		id := before
		label := cleanLabel(strings.TrimRight(after, "]"))
		if id == "" || label == "" {
			return 0, false
		}
		idx, ok = g.nodeLabel(id, label)
	} else {
		idx, ok = g.nodeIndex(token, nil, shapeRect)
	}
	if !ok {
		return 0, false
	}
	for len(*infos) < len(g.nodes) {
		*infos = append(*infos, emptyClassInfo())
	}
	return idx, true
}

func splitErRelationship(st string) (rel string, label *string, ok bool) {
	rel = st
	if pre, post, split := splitOnce(st, ":"); split {
		rel = pre
		l := trimJS(post)
		label = &l
	}
	for _, t := range words(rel) {
		if _, ok := parseErOp(t); ok {
			return rel, label, true
		}
	}
	return "", nil, false
}

func isAscii(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7f {
			return false
		}
	}
	return true
}

type erOp struct {
	cardL string
	cardR string
	line  lineKind
}

// parseErOp reads a crow's-foot operator: two cardinality glyphs around -- or ..
func parseErOp(tok string) (erOp, bool) {
	if len([]rune(tok)) != 6 || !isAscii(tok) {
		return erOp{}, false
	}
	mid := tok[2:4]
	var line lineKind
	switch mid {
	case "--":
		line = lineSolid
	case "..":
		line = lineDotted
	default:
		return erOp{}, false
	}
	cardL, okL := erCard(tok[0:2])
	cardR, okR := erCard(tok[4:6])
	if !okL || !okR {
		return erOp{}, false
	}
	return erOp{cardL: cardL, cardR: cardR, line: line}, true
}

func erCard(tok string) (string, bool) {
	switch tok {
	case "|o", "o|":
		return "0..1", true
	case "||":
		return "1", true
	case "}o", "o{":
		return "0..*", true
	case "}|", "|{":
		return "1..*", true
	}
	return "", false
}

// pushErAttribute reads `type name`; a trailing quoted comment is dropped.
func pushErAttribute(info *classInfo, raw string) {
	var parts []string
	for _, tok := range words(raw) {
		if strings.HasPrefix(tok, "\"") {
			break
		}
		parts = append(parts, tok)
	}
	if len(parts) == 0 {
		return
	}
	line := decodeHtmlEntities(strings.Join(parts, " "))
	appendMember(&info.attrs, line)
}

// ------------------------------------------------------------------ sequence

type seqHead string

const (
	seqHeadArrow seqHead = "arrow"
	seqHeadCross seqHead = "cross"
)

type seqOp struct {
	op     string
	dashed bool
	head   seqHead
}

var seqOps = []seqOp{
	{"-->>", true, seqHeadArrow},
	{"->>", false, seqHeadArrow},
	{"--x", true, seqHeadCross},
	{"-x", false, seqHeadCross},
	{"--)", true, seqHeadArrow},
	{"-)", false, seqHeadArrow},
	{"-->", true, seqHeadArrow},
	{"->", false, seqHeadArrow},
}

const maxSeqOp = 4

type noteKind string

const (
	noteOver  noteKind = "over"
	noteLeft  noteKind = "left"
	noteRight noteKind = "right"
)

type noteAnchor struct {
	kind noteKind
	from int // over
	to   int // over
	at   int // left/right
}

type seqItemKind string

const (
	seqMessage seqItemKind = "message"
	seqNote    seqItemKind = "note"
	seqDivider seqItemKind = "divider"
)

type seqItem struct {
	kind seqItemKind
	// message
	from   int
	to     int
	text   *string
	dashed bool
	head   seqHead
	// note
	anchor noteAnchor
	// note / divider text
	str string
}

type sequence struct {
	labels []string
	index  map[string]int
	items  []seqItem
}

func newSequence() *sequence {
	return &sequence{index: map[string]int{}}
}

func (s *sequence) participant(id string, label *string) (int, bool) {
	if existing, ok := s.index[id]; ok {
		if label != nil {
			s.labels[existing] = *label
		}
		return existing, true
	}
	if len(s.labels) >= maxNodes {
		return 0, false
	}
	s.index[id] = len(s.labels)
	lbl := id
	if label != nil {
		lbl = *label
	}
	s.labels = append(s.labels, lbl)
	return len(s.labels) - 1, true
}

func parseSequence(src string) *sequence {
	statements := statementsOf(src)
	if headerKind(statements) != "sequencediagram" {
		return nil
	}

	seq := newSequence()
	autonumber := false
	msgCount := 0
	var blocks []bool // one per open block; true when it draws a divider on end

	for _, st := range statements[1:] {
		first := firstWord(st)
		lower := asciiLower(first)

		if lower == "participant" || lower == "actor" {
			rest := trimJS(st[len(first):])
			if rest == "" {
				return nil
			}
			id := rest
			var label *string
			if pre, post, ok := splitOnce(rest, " as "); ok {
				id = trimJS(pre)
				l := cleanLabel(post)
				label = &l
			}
			if _, ok := seq.participant(id, label); !ok {
				return nil
			}
			continue
		}
		if lower == "autonumber" {
			autonumber = true
			continue
		}
		switch lower {
		case "activate", "deactivate", "create", "destroy", "title", "acctitle", "accdescr", "links", "link", "properties":
			continue
		}
		if lower == "note" {
			text, anchor, ok := parseNoteAnchor(trimJS(st[len(first):]), seq)
			if !ok {
				return nil
			}
			if len(seq.items) >= maxEdges {
				return nil
			}
			seq.items = append(seq.items, seqItem{kind: seqNote, anchor: anchor, str: text})
			continue
		}
		if isBlockKeyword(lower) {
			if lower == "else" || lower == "and" || lower == "option" {
				if last := lastBool(blocks); last == nil || !*last {
					continue
				}
			} else {
				blocks = append(blocks, true)
			}
			if len(seq.items) >= maxEdges {
				return nil
			}
			seq.items = append(seq.items, seqItem{kind: seqDivider, str: decodeHtmlEntities(st)})
			continue
		}
		if lower == "rect" || lower == "box" {
			blocks = append(blocks, false)
			continue
		}
		if lower == "end" {
			if len(blocks) > 0 {
				last := blocks[len(blocks)-1]
				blocks = blocks[:len(blocks)-1]
				if last {
					if len(seq.items) >= maxEdges {
						return nil
					}
					seq.items = append(seq.items, seqItem{kind: seqDivider, str: "end"})
				}
			}
			continue
		}

		msg := parseSeqMessage(st, seq)
		if msg == nil {
			return nil
		}
		text := msg.text
		if autonumber {
			msgCount++
			var t string
			if text == nil {
				t = itoa(msgCount) + "."
			} else {
				t = itoa(msgCount) + ". " + *text
			}
			text = &t
		}
		if len(seq.items) >= maxEdges {
			return nil
		}
		seq.items = append(seq.items, seqItem{kind: seqMessage, from: msg.from, to: msg.to, text: text, dashed: msg.dashed, head: msg.head})
	}

	if len(seq.labels) == 0 {
		return nil
	}
	return seq
}

func isBlockKeyword(lower string) bool {
	switch lower {
	case "loop", "alt", "opt", "par", "critical", "break", "else", "and", "option":
		return true
	}
	return false
}

func lastBool(b []bool) *bool {
	if len(b) == 0 {
		return nil
	}
	return &b[len(b)-1]
}

func parseNoteAnchor(rest string, seq *sequence) (text string, anchor noteAnchor, ok bool) {
	lower := asciiLower(rest)
	var kind noteKind
	var idsAndText string
	switch {
	case strings.HasPrefix(lower, "over "):
		kind = noteOver
		idsAndText = rest[len("over "):]
	case strings.HasPrefix(lower, "left of "):
		kind = noteLeft
		idsAndText = rest[len("left of "):]
	case strings.HasPrefix(lower, "right of "):
		kind = noteRight
		idsAndText = rest[len("right of "):]
	default:
		return "", noteAnchor{}, false
	}

	pre, post, split := splitOnce(idsAndText, ":")
	if !split {
		return "", noteAnchor{}, false
	}
	text = decodeHtmlEntities(trimJS(post))
	var parts []string
	for s := range strings.SplitSeq(pre, ",") {
		if s := trimJS(s); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "", noteAnchor{}, false
	}
	a, ok := seq.participant(parts[0], nil)
	if !ok {
		return "", noteAnchor{}, false
	}

	if kind != noteOver {
		return text, noteAnchor{kind: kind, at: a}, true
	}
	b := a
	if len(parts) > 1 {
		second, ok := seq.participant(parts[1], nil)
		if !ok {
			return "", noteAnchor{}, false
		}
		b = second
	}
	return text, noteAnchor{kind: noteOver, from: min(a, b), to: max(a, b)}, true
}

type parsedSeqMessage struct {
	from   int
	to     int
	text   *string
	dashed bool
	head   seqHead
}

func parseSeqMessage(st string, seq *sequence) *parsedSeqMessage {
	chars := []rune(st)
	var found *seqOp
	var foundPos int
	for pos := 0; pos < len(chars) && found == nil; pos++ {
		end := min(pos+maxSeqOp, len(chars))
		tail := string(chars[pos:end])
		for i := range seqOps {
			if strings.HasPrefix(tail, seqOps[i].op) {
				found = &seqOps[i]
				foundPos = pos
				break
			}
		}
	}
	if found == nil {
		return nil
	}

	fromID := trimJS(string(chars[:foundPos]))
	if fromID == "" {
		return nil
	}
	rest := strings.TrimLeft(trimStartJS(string(chars[foundPos+len([]rune(found.op)):])), "+-")

	toID := rest
	var text *string
	if pre, post, ok := splitOnce(rest, ":"); ok {
		toID = pre
		text = nonEmpty(decodeHtmlEntities(trimJS(post)))
	}
	toID = trimJS(toID)
	if toID == "" {
		return nil
	}

	from, ok := seq.participant(fromID, nil)
	if !ok {
		return nil
	}
	to, ok := seq.participant(toID, nil)
	if !ok {
		return nil
	}
	return &parsedSeqMessage{from: from, to: to, text: text, dashed: found.dashed, head: found.head}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
